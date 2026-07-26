package priceai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/ifty-r/upstream-ops/backend/storage"
)

const (
	riskCacheTTL      = 6 * time.Hour
	riskPageMaxBytes  = 4 << 20
	riskPageUserAgent = "upstream-ops-priceai-radar/1.0"
)

type riskEndpoints struct {
	productBaseURL string
	webHost        string
	allowHTTP      bool
}

func productionRiskEndpoints() riskEndpoints {
	return riskEndpoints{
		productBaseURL: "https://" + priceAIWebHost,
		webHost:        priceAIWebHost,
	}
}

// RiskClient fetches only canonical public product pages. It deliberately has
// no method that accepts an arbitrary page URL.
type RiskClient struct {
	feed      *FeedClient
	endpoints riskEndpoints
}

func NewRiskClient() *RiskClient {
	return newRiskClient(nil, productionRiskEndpoints())
}

func newRiskClient(client *http.Client, endpoints riskEndpoints) *RiskClient {
	return &RiskClient{
		feed: newFeedClient(client, feedEndpoints{
			webHost:   endpoints.webHost,
			dataHost:  endpoints.webHost,
			allowHTTP: endpoints.allowHTTP,
		}),
		endpoints: endpoints,
	}
}

func (c *RiskClient) ProductURL(slug string) (string, error) {
	if c == nil || c.feed == nil {
		return "", fmt.Errorf("priceai risk client is unavailable")
	}
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return "", fmt.Errorf("priceai product slug is required")
	}
	base := strings.TrimRight(strings.TrimSpace(c.endpoints.productBaseURL), "/")
	parsed, err := c.feed.validateURL(base, c.endpoints.webHost)
	if err != nil {
		return "", fmt.Errorf("validate priceai product base URL: %w", err)
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("priceai product base URL must not contain query or fragment")
	}
	return base + "/products/" + url.PathEscape(slug), nil
}

func (c *RiskClient) Fetch(ctx context.Context, slug string) ([]riskRecord, string, bool, error) {
	pageURL, err := c.ProductURL(slug)
	if err != nil {
		return nil, "", false, err
	}
	headers := make(http.Header)
	headers.Set("Accept", "text/html,application/xhtml+xml")
	headers.Set("User-Agent", riskPageUserAgent)
	body, _, status, err := c.feed.get(ctx, pageURL, c.endpoints.webHost, headers, riskPageMaxBytes)
	if err != nil {
		return nil, pageURL, false, fmt.Errorf("fetch priceai risk page: %w", err)
	}
	if status != http.StatusOK {
		return nil, pageURL, false, fmt.Errorf("fetch priceai risk page: unexpected status %d", status)
	}
	records, found, err := extractRiskFeedback(body)
	if err != nil {
		return nil, pageURL, found, fmt.Errorf("extract priceai risk feedback: %w", err)
	}
	return records, pageURL, found, nil
}

type riskRecord struct {
	Scope           storage.PriceAIRiskScope
	SubjectRemoteID string
	Status          string
	FeedbackCount   int
	Reasons         []string
	Summaries       []string
	LatestAt        *time.Time
	RawJSON         string
}

type RiskRefreshResult struct {
	SnapshotID        string `json:"snapshot_id,omitempty"`
	ProductsAttempted int    `json:"products_attempted"`
	ProductsSkipped   int    `json:"products_skipped"`
	FeedbackUpdated   int    `json:"feedback_updated"`
	Failures          int    `json:"failures"`
}

type riskCall struct {
	done   chan struct{}
	result RiskRefreshResult
	err    error
}

// RefreshRisk is intentionally independent of Feed synchronization. A failed
// product page preserves its prior cache and never changes Feed state.
func (s *Service) RefreshRisk(ctx context.Context) (RiskRefreshResult, error) {
	return s.RefreshRiskWithConcurrency(ctx, 1)
}

func (s *Service) RefreshRiskWithConcurrency(ctx context.Context, concurrency int) (RiskRefreshResult, error) {
	if s == nil || s.repo == nil {
		return RiskRefreshResult{}, fmt.Errorf("priceai repository is unavailable")
	}
	if concurrency <= 0 {
		concurrency = 1
	}
	s.riskMu.Lock()
	if active := s.riskActive; active != nil {
		s.riskMu.Unlock()
		select {
		case <-ctx.Done():
			return RiskRefreshResult{}, ctx.Err()
		case <-active.done:
			return active.result, active.err
		}
	}
	call := &riskCall{done: make(chan struct{})}
	s.riskActive = call
	s.riskMu.Unlock()

	result, err := s.refreshRiskOnce(ctx, concurrency)

	s.riskMu.Lock()
	call.result = result
	call.err = err
	s.riskActive = nil
	close(call.done)
	s.riskMu.Unlock()
	return result, err
}

func (s *Service) refreshRiskOnce(ctx context.Context, concurrency int) (RiskRefreshResult, error) {
	started := s.currentTime()
	state, err := s.repo.FindFeedState(FeedSourceKey)
	if err != nil {
		return RiskRefreshResult{}, err
	}
	result := RiskRefreshResult{}
	if state != nil {
		result.SnapshotID = state.SnapshotID
	}
	products, err := s.listRiskProducts()
	if err != nil {
		return result, err
	}

	type productResult struct {
		attempted int
		skipped   int
		updated   int
		err       error
	}
	results := make(chan productResult, len(products))
	sem := make(chan struct{}, concurrency)
	var workers sync.WaitGroup
launch:
	for i := range products {
		if err := ctx.Err(); err != nil {
			break launch
		}
		product := products[i]
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			break launch
		}
		workers.Add(1)
		go func() {
			defer workers.Done()
			defer func() { <-sem }()
			attempted, skipped, updated, err := s.refreshProductRisk(ctx, product)
			results <- productResult{attempted: attempted, skipped: skipped, updated: updated, err: err}
		}()
	}
	workers.Wait()
	close(results)

	var failures []error
	for item := range results {
		result.ProductsAttempted += item.attempted
		result.ProductsSkipped += item.skipped
		result.FeedbackUpdated += item.updated
		if item.err != nil {
			result.Failures++
			failures = append(failures, item.err)
		}
	}
	if err := ctx.Err(); err != nil {
		failures = append(failures, err)
	}
	finished := s.currentTime()
	refreshErr := errors.Join(failures...)
	log := &storage.PriceAISyncLog{
		JobKind:       storage.PriceAISyncJobRisk,
		SnapshotID:    result.SnapshotID,
		Success:       refreshErr == nil,
		ProductsCount: result.ProductsAttempted,
		OffersCount:   result.FeedbackUpdated,
		ErrorMessage:  errorMessage(refreshErr),
		StartedAt:     started,
		FinishedAt:    finished,
		DurationMS:    finished.Sub(started).Milliseconds(),
	}
	if err := s.repo.AppendSyncLog(log); err != nil {
		if refreshErr != nil {
			return result, fmt.Errorf("priceai risk refresh failed: %w; record log: %v", refreshErr, err)
		}
		return result, err
	}
	return result, refreshErr
}

func (s *Service) listRiskProducts() ([]storage.PriceAIProduct, error) {
	const pageSize = 100
	all := make([]storage.PriceAIProduct, 0)
	for page := 1; ; page++ {
		products, total, err := s.repo.ListProductsPage(storage.PriceAIProductPageOptions{Page: page, PageSize: pageSize})
		if err != nil {
			return nil, err
		}
		all = append(all, products...)
		if len(all) >= int(total) || len(products) == 0 {
			return all, nil
		}
	}
}

func (s *Service) refreshProductRisk(ctx context.Context, product storage.PriceAIProduct) (attempted, skipped, updated int, err error) {
	cache, err := s.repo.ListRiskFeedback(product.ID)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("list risk cache for %q: %w", product.Slug, err)
	}
	if riskCacheIsFresh(cache, s.currentTime()) {
		return 0, 1, 0, nil
	}
	attempted = 1
	client := s.riskClient
	if client == nil {
		return attempted, 0, 0, fmt.Errorf("priceai risk client is unavailable")
	}
	records, pageURL, found, err := client.Fetch(ctx, product.Slug)
	if err != nil {
		_, _ = s.repo.SetRiskFeedbackError(product.ID, err.Error())
		return attempted, 0, 0, fmt.Errorf("refresh risk for %q: %w", product.Slug, err)
	}
	if !found {
		return attempted, 1, 0, nil
	}
	offers, err := s.repo.ListOffers(product.ID)
	if err != nil {
		return attempted, 0, 0, fmt.Errorf("list current offers for %q: %w", product.Slug, err)
	}
	fetchedAt := s.currentTime()
	for _, record := range matchRiskRecords(records, offers) {
		feedback := &storage.PriceAIRiskFeedback{
			ProductID:       product.ID,
			Scope:           record.Scope,
			SubjectRemoteID: record.SubjectRemoteID,
			Status:          record.Status,
			FeedbackCount:   record.FeedbackCount,
			ReasonsJSON:     marshalStringList(record.Reasons),
			SummariesJSON:   marshalStringList(record.Summaries),
			LatestAt:        record.LatestAt,
			PageURL:         pageURL,
			FetchedAt:       &fetchedAt,
			RawJSON:         record.RawJSON,
		}
		if _, err := s.repo.UpsertRiskFeedback(feedback); err != nil {
			return attempted, 0, updated, fmt.Errorf("store risk feedback for %q: %w", product.Slug, err)
		}
		updated++
	}
	return attempted, 0, updated, nil
}

func riskCacheIsFresh(cache []storage.PriceAIRiskFeedback, now time.Time) bool {
	if len(cache) == 0 {
		return false
	}
	for _, item := range cache {
		if item.FetchedAt == nil || strings.TrimSpace(item.LastError) != "" || now.Sub(*item.FetchedAt) >= riskCacheTTL {
			return false
		}
	}
	return true
}

func matchRiskRecords(records []riskRecord, offers []storage.PriceAIOffer) []riskRecord {
	sourceIDs := make(map[string]struct{}, len(offers))
	offerIDs := make(map[string]struct{}, len(offers))
	for _, offer := range offers {
		if value := strings.TrimSpace(offer.SourceID); value != "" {
			sourceIDs[value] = struct{}{}
		}
		if value := strings.TrimSpace(offer.RemoteID); value != "" {
			offerIDs[value] = struct{}{}
		}
	}
	matched := make([]riskRecord, 0, len(records))
	seen := make(map[string]struct{}, len(records))
	for _, record := range records {
		id := strings.TrimSpace(record.SubjectRemoteID)
		if id == "" {
			continue
		}
		valid := false
		switch record.Scope {
		case storage.PriceAIRiskScopeSource:
			_, valid = sourceIDs[id]
		case storage.PriceAIRiskScopeOffer:
			_, valid = offerIDs[id]
		}
		if !valid {
			continue
		}
		key := string(record.Scope) + "\x00" + id
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		matched = append(matched, record)
	}
	return matched
}

func marshalStringList(values []string) string {
	if len(values) == 0 {
		return "[]"
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return "[]"
	}
	return string(encoded)
}

func errorMessage(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func extractRiskFeedback(document []byte) ([]riskRecord, bool, error) {
	values, found, err := jsonValuesForKey(document, "riskFeedback")
	if err != nil || !found {
		return nil, found, err
	}
	records := make([]riskRecord, 0)
	for _, raw := range values {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			continue
		}
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, true, err
		}
		records = append(records, collectRiskRecords(value, "", "")...)
	}
	return records, true, nil
}

func jsonValuesForKey(document []byte, key string) ([][]byte, bool, error) {
	needle := []byte("\"" + key + "\"")
	values := make([][]byte, 0, 1)
	found := false
	for offset := 0; offset < len(document); {
		index := bytes.Index(document[offset:], needle)
		if index < 0 {
			break
		}
		start := offset + index + len(needle)
		start = skipJSONSpace(document, start)
		if start >= len(document) || document[start] != ':' {
			offset = start
			continue
		}
		found = true
		start = skipJSONSpace(document, start+1)
		value, next, err := extractJSONValue(document, start)
		if err != nil {
			return nil, true, err
		}
		values = append(values, value)
		offset = next
	}
	return values, found, nil
}

func skipJSONSpace(value []byte, index int) int {
	for index < len(value) {
		switch value[index] {
		case ' ', '\n', '\r', '\t':
			index++
		default:
			return index
		}
	}
	return index
}

func extractJSONValue(value []byte, start int) ([]byte, int, error) {
	if start >= len(value) {
		return nil, start, fmt.Errorf("riskFeedback value is missing")
	}
	if bytes.HasPrefix(value[start:], []byte("null")) {
		return []byte("null"), start + len("null"), nil
	}
	if value[start] != '{' && value[start] != '[' {
		return nil, start, fmt.Errorf("riskFeedback must be an object, array, or null")
	}
	depth := 0
	inString := false
	escaped := false
	for index := start; index < len(value); index++ {
		char := value[index]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if char == '\\' {
				escaped = true
			} else if char == '"' {
				inString = false
			}
			continue
		}
		switch char {
		case '"':
			inString = true
		case '{', '[':
			depth++
		case '}', ']':
			depth--
			if depth == 0 {
				return append([]byte(nil), value[start:index+1]...), index + 1, nil
			}
			if depth < 0 {
				return nil, index, fmt.Errorf("riskFeedback has unbalanced JSON")
			}
		}
	}
	return nil, len(value), fmt.Errorf("riskFeedback JSON is not closed")
}

func collectRiskRecords(value any, hintedScope storage.PriceAIRiskScope, hintedSubject string) []riskRecord {
	switch value := value.(type) {
	case []any:
		out := make([]riskRecord, 0)
		for _, child := range value {
			out = append(out, collectRiskRecords(child, hintedScope, hintedSubject)...)
		}
		return out
	case map[string]any:
		scope := riskScopeFromMap(value)
		if scope == "" {
			scope = hintedScope
		}
		subject := riskSubjectFromMap(value, scope)
		if subject == "" {
			subject = hintedSubject
		}
		out := make([]riskRecord, 0, 1)
		if scope != "" && subject != "" && hasRiskPayload(value) {
			out = append(out, riskRecordFromMap(value, scope, subject))
		}
		for key, child := range value {
			switch normalizedRiskKey(key) {
			case "sources", "sourcefeedback", "sourcefeedbacks":
				out = append(out, collectScopedRiskChildren(child, storage.PriceAIRiskScopeSource)...)
			case "offers", "offerfeedback", "offerfeedbacks":
				out = append(out, collectScopedRiskChildren(child, storage.PriceAIRiskScopeOffer)...)
			default:
				out = append(out, collectRiskRecords(child, "", "")...)
			}
		}
		return out
	default:
		return nil
	}
}

func collectScopedRiskChildren(value any, scope storage.PriceAIRiskScope) []riskRecord {
	if values, ok := value.(map[string]any); ok {
		out := make([]riskRecord, 0, len(values))
		for subject, child := range values {
			out = append(out, collectRiskRecords(child, scope, strings.TrimSpace(subject))...)
		}
		return out
	}
	return collectRiskRecords(value, scope, "")
}

func riskScopeFromMap(value map[string]any) storage.PriceAIRiskScope {
	switch strings.ToLower(strings.TrimSpace(riskString(value, "scope"))) {
	case string(storage.PriceAIRiskScopeSource):
		return storage.PriceAIRiskScopeSource
	case string(storage.PriceAIRiskScopeOffer):
		return storage.PriceAIRiskScopeOffer
	default:
		return ""
	}
}

func riskSubjectFromMap(value map[string]any, scope storage.PriceAIRiskScope) string {
	keys := []string{"subjectRemoteId", "subject_remote_id"}
	if scope == storage.PriceAIRiskScopeSource {
		keys = append(keys, "sourceId", "source_id", "sourceRemoteId", "source_remote_id")
	} else if scope == storage.PriceAIRiskScopeOffer {
		keys = append(keys, "offerId", "offer_id", "offerRemoteId", "offer_remote_id")
	}
	for _, key := range keys {
		if value := strings.TrimSpace(riskString(value, key)); value != "" {
			return value
		}
	}
	return ""
}

func hasRiskPayload(value map[string]any) bool {
	for _, key := range []string{"status", "riskStatus", "feedbackCount", "feedback_count", "reasons", "summaries", "latestAt", "latest_at"} {
		if _, ok := riskMapValue(value, key); ok {
			return true
		}
	}
	return false
}

func riskRecordFromMap(value map[string]any, scope storage.PriceAIRiskScope, subject string) riskRecord {
	raw, _ := json.Marshal(value)
	return riskRecord{
		Scope:           scope,
		SubjectRemoteID: strings.TrimSpace(subject),
		Status:          strings.TrimSpace(riskString(value, "status", "riskStatus")),
		FeedbackCount:   riskInt(value, "feedbackCount", "feedback_count", "count"),
		Reasons:         riskStrings(value, "reasons"),
		Summaries:       riskStrings(value, "summaries", "summary"),
		LatestAt:        riskTime(value, "latestAt", "latest_at", "updatedAt", "updated_at"),
		RawJSON:         truncatePriceAIRawJSON(raw),
	}
}

func normalizedRiskKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.NewReplacer("_", "", "-", "").Replace(value)
	return value
}

func riskMapValue(value map[string]any, keys ...string) (any, bool) {
	for _, key := range keys {
		needle := normalizedRiskKey(key)
		for candidate, item := range value {
			if normalizedRiskKey(candidate) == needle {
				return item, true
			}
		}
	}
	return nil, false
}

func riskString(value map[string]any, keys ...string) string {
	item, ok := riskMapValue(value, keys...)
	if !ok {
		return ""
	}
	text, _ := item.(string)
	return text
}

func riskInt(value map[string]any, keys ...string) int {
	item, ok := riskMapValue(value, keys...)
	if !ok {
		return 0
	}
	switch number := item.(type) {
	case float64:
		return int(number)
	case json.Number:
		parsed, _ := number.Int64()
		return int(parsed)
	default:
		return 0
	}
}

func riskStrings(value map[string]any, keys ...string) []string {
	item, ok := riskMapValue(value, keys...)
	if !ok {
		return nil
	}
	switch values := item.(type) {
	case string:
		if text := strings.TrimSpace(values); text != "" {
			return []string{text}
		}
	case []any:
		out := make([]string, 0, len(values))
		for _, value := range values {
			if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
				out = append(out, strings.TrimSpace(text))
			}
		}
		return out
	}
	return nil
}

func riskTime(value map[string]any, keys ...string) *time.Time {
	text := strings.TrimSpace(riskString(value, keys...))
	if text == "" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339, text)
	if err != nil {
		return nil
	}
	return &parsed
}
