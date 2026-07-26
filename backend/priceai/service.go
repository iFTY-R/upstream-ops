package priceai

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ifty-r/upstream-ops/backend/notify"
	"github.com/ifty-r/upstream-ops/backend/storage"
)

const FeedSourceKey = "price-radar"

var defaultWatchSlugs = []string{"chatgpt-plus", "chatgpt-team-business"}

type Service struct {
	repo       *storage.PriceAI
	client     *FeedClient
	riskClient *RiskClient
	dispatcher notificationDispatcher
	log        *slog.Logger
	now        func() time.Time

	mu     sync.Mutex
	active *syncCall

	riskMu     sync.Mutex
	riskActive *riskCall
}

type syncCall struct {
	done   chan struct{}
	result SyncResult
	err    error
}

// notificationDispatcher keeps PriceAI independent from a concrete delivery
// implementation while allowing tests to assert post-commit dispatches.
type notificationDispatcher interface {
	Dispatch(context.Context, notify.Message) error
}

func NewService(repo *storage.PriceAI, log *slog.Logger) *Service {
	return NewServiceWithClient(repo, NewFeedClient(), log)
}

func NewServiceWithClient(repo *storage.PriceAI, client *FeedClient, log *slog.Logger) *Service {
	if client == nil {
		client = NewFeedClient()
	}
	return &Service{
		repo:       repo,
		client:     client,
		riskClient: NewRiskClient(),
		log:        log,
		now:        time.Now,
	}
}

// SetDispatcher is called during runtime construction. Feed import remains
// transactional and only calls the dispatcher after a successful commit.
func (s *Service) SetDispatcher(dispatcher notificationDispatcher) {
	if s == nil {
		return
	}
	s.dispatcher = dispatcher
}

type NotificationCandidate struct {
	ProductID                   uint                        `json:"product_id"`
	ProductName                 string                      `json:"product_name"`
	ProductSlug                 string                      `json:"product_slug"`
	WatchTargetID               uint                        `json:"watch_target_id"`
	SnapshotID                  string                      `json:"snapshot_id"`
	Events                      []storage.NotificationEvent `json:"events"`
	PreviousLowestPrice         *float64                    `json:"previous_lowest_price,omitempty"`
	PreviousLowestPriceCurrency *string                     `json:"previous_lowest_price_currency,omitempty"`
	CurrentLowestPrice          *float64                    `json:"current_lowest_price,omitempty"`
	CurrentLowestPriceCurrency  *string                     `json:"current_lowest_price_currency,omitempty"`
	PreviousInStockCount        int                         `json:"previous_in_stock_count"`
	CurrentInStockCount         int                         `json:"current_in_stock_count"`
	ProductGeneratedAt          time.Time                   `json:"product_generated_at"`
}

type SyncResult struct {
	SnapshotID           string                  `json:"snapshot_id,omitempty"`
	NotModified          bool                    `json:"not_modified"`
	Skipped              bool                    `json:"skipped"`
	ProductsCount        int                     `json:"products_count"`
	OffersCount          int                     `json:"offers_count"`
	ChangedProductsCount int                     `json:"changed_products_count"`
	Notifications        []NotificationCandidate `json:"notifications,omitempty"`
}

// Sync coalesces concurrent manual and scheduled requests. Only the active
// caller owns the network run; later callers wait for its committed result.
func (s *Service) Sync(ctx context.Context) (SyncResult, error) {
	if s.repo == nil {
		return SyncResult{}, fmt.Errorf("priceai repository is unavailable")
	}
	s.mu.Lock()
	if active := s.active; active != nil {
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return SyncResult{}, ctx.Err()
		case <-active.done:
			return active.result, active.err
		}
	}
	call := &syncCall{done: make(chan struct{})}
	s.active = call
	s.mu.Unlock()

	result, err := s.syncOnce(ctx)

	s.mu.Lock()
	call.result = result
	call.err = err
	s.active = nil
	close(call.done)
	s.mu.Unlock()
	return result, err
}

func (s *Service) syncOnce(ctx context.Context) (SyncResult, error) {
	started := s.currentTime()
	state, allowed, err := s.beginAttempt(started)
	if err != nil {
		return SyncResult{}, err
	}
	if !allowed {
		return SyncResult{SnapshotID: state.SnapshotID, Skipped: true}, nil
	}

	doc, err := s.client.FetchDiscovery(ctx)
	if err != nil {
		return s.recordFailure(ctx, state, started, err)
	}
	applyDiscovery(state, doc)

	pointerFetch, err := s.client.FetchPointer(ctx, state.ETag, state.LastModified)
	if err != nil {
		return s.recordFailure(ctx, state, started, err)
	}
	if pointerFetch.NotModified {
		result := SyncResult{SnapshotID: state.SnapshotID, NotModified: true}
		if err := s.persistSuccess(ctx, state, nil, pointerFetch, started, result); err != nil {
			return SyncResult{}, err
		}
		return result, nil
	}
	if pointerFetch.Pointer == nil {
		return s.recordFailure(ctx, state, started, fmt.Errorf("priceai pointer response is empty"))
	}
	if pointerFetch.Pointer.SnapshotID == state.SnapshotID {
		result := SyncResult{SnapshotID: state.SnapshotID, NotModified: true}
		if err := s.persistSuccess(ctx, state, pointerFetch.Pointer, pointerFetch, started, result); err != nil {
			return SyncResult{}, err
		}
		return result, nil
	}

	snapshot, err := s.client.FetchSnapshot(ctx, pointerFetch.Pointer.SnapshotURL)
	if err != nil {
		return s.recordFailure(ctx, state, started, err)
	}
	prepared, err := prepareSnapshot(pointerFetch.Pointer, snapshot)
	if err != nil {
		return s.recordFailure(ctx, state, started, err)
	}
	result, err := s.importSnapshot(ctx, state, pointerFetch.Pointer, pointerFetch, prepared, started)
	if err != nil {
		return s.recordFailure(ctx, state, started, err)
	}
	return result, nil
}

func (s *Service) beginAttempt(at time.Time) (*storage.PriceAIFeedState, bool, error) {
	state, err := s.repo.FindFeedState(FeedSourceKey)
	if err != nil {
		return nil, false, err
	}
	if state == nil {
		state = initialFeedState()
	}
	if state.LastAttemptAt != nil && at.Sub(*state.LastAttemptAt) < minimumRefreshPeriod {
		return state, false, nil
	}
	state.LastAttemptAt = &at
	if err := s.repo.UpsertFeedState(state); err != nil {
		return nil, false, err
	}
	return state, true, nil
}

func initialFeedState() *storage.PriceAIFeedState {
	return &storage.PriceAIFeedState{
		SourceKey:                   FeedSourceKey,
		LatestURL:                   PointerURL,
		SchemaURL:                   SchemaURL,
		DefaultWatchSeededSlugsJSON: "[]",
	}
}

func applyDiscovery(state *storage.PriceAIFeedState, doc *DiscoveryDocument) {
	state.LatestURL = doc.LatestURL
	state.SchemaURL = doc.SchemaURL
}

func (s *Service) persistSuccess(ctx context.Context, state *storage.PriceAIFeedState, pointer *Pointer, fetched *PointerFetch, started time.Time, result SyncResult) error {
	next := *state
	finished := s.currentTime()
	previous := *state
	if pointer != nil {
		applyPointer(&next, pointer)
	}
	applyPointerCacheHeaders(&next, fetched)
	next.LastSuccessAt = &finished
	next.ConsecutiveFailures = 0
	next.LastError = ""
	result.SnapshotID = next.SnapshotID
	healthEvents := feedHealthNotificationEvents(previous, next)
	if err := s.repo.Transaction(func(tx *storage.PriceAI) error {
		if err := tx.UpsertFeedState(&next); err != nil {
			return err
		}
		return tx.AppendSyncLog(&storage.PriceAISyncLog{
			JobKind:     storage.PriceAISyncJobFeed,
			SnapshotID:  next.SnapshotID,
			Success:     true,
			NotModified: result.NotModified,
			StartedAt:   started,
			FinishedAt:  finished,
			DurationMS:  finished.Sub(started).Milliseconds(),
		})
	}); err != nil {
		return err
	}
	s.dispatchFeedHealth(ctx, healthEvents, &next)
	return nil
}

func (s *Service) recordFailure(ctx context.Context, state *storage.PriceAIFeedState, started time.Time, cause error) (SyncResult, error) {
	if state == nil {
		state = initialFeedState()
	}
	next := *state
	previousFailures := next.ConsecutiveFailures
	next.ConsecutiveFailures++
	next.LastError = cause.Error()
	finished := s.currentTime()
	persistErr := s.repo.Transaction(func(tx *storage.PriceAI) error {
		if err := tx.UpsertFeedState(&next); err != nil {
			return err
		}
		return tx.AppendSyncLog(&storage.PriceAISyncLog{
			JobKind:      storage.PriceAISyncJobFeed,
			SnapshotID:   next.SnapshotID,
			Success:      false,
			ErrorMessage: cause.Error(),
			StartedAt:    started,
			FinishedAt:   finished,
			DurationMS:   finished.Sub(started).Milliseconds(),
		})
	})
	if persistErr != nil {
		return SyncResult{SnapshotID: next.SnapshotID}, fmt.Errorf("priceai sync failed: %w; record failure: %v", cause, persistErr)
	}
	if s.log != nil {
		s.log.Warn("priceai feed sync failed", "err", cause, "failures", next.ConsecutiveFailures)
	}
	if previousFailures < 3 && next.ConsecutiveFailures >= 3 {
		s.dispatchFeedHealth(ctx, []storage.NotificationEvent{storage.EventPriceAISyncFailed}, &next)
	}
	return SyncResult{SnapshotID: next.SnapshotID}, cause
}

func feedHealthNotificationEvents(previous, next storage.PriceAIFeedState) []storage.NotificationEvent {
	events := make([]storage.NotificationEvent, 0, 2)
	if previous.SnapshotID != "" && previous.FeedStale != next.FeedStale {
		if next.FeedStale {
			events = appendUniqueNotificationEvent(events, storage.EventPriceAIFeedStale)
		} else {
			events = appendUniqueNotificationEvent(events, storage.EventPriceAISyncRecovered)
		}
	}
	if previous.ConsecutiveFailures >= 3 && next.ConsecutiveFailures == 0 {
		events = appendUniqueNotificationEvent(events, storage.EventPriceAISyncRecovered)
	}
	return events
}

func (s *Service) dispatchFeedHealth(ctx context.Context, events []storage.NotificationEvent, state *storage.PriceAIFeedState) {
	if s == nil || s.dispatcher == nil || state == nil {
		return
	}
	for _, event := range events {
		subject, body := priceAIFeedHealthMessage(event, state)
		if err := s.dispatcher.Dispatch(ctx, notify.Message{
			Event:   event,
			Subject: subject,
			Body:    body,
			Extra: map[string]any{
				"snapshot_id":          state.SnapshotID,
				"generated_at":         state.GeneratedAt,
				"consecutive_failures": state.ConsecutiveFailures,
			},
		}); err != nil && s.log != nil {
			s.log.Warn("dispatch priceai feed health notification failed", "event", event, "err", err)
		}
	}
}

func priceAIFeedHealthMessage(event storage.NotificationEvent, state *storage.PriceAIFeedState) (string, string) {
	snapshot := strings.TrimSpace(state.SnapshotID)
	if snapshot == "" {
		snapshot = "尚无已提交快照"
	}
	generated := "未知"
	if state.GeneratedAt != nil {
		generated = state.GeneratedAt.UTC().Format(time.RFC3339)
	}
	switch event {
	case storage.EventPriceAIFeedStale:
		return "PriceAI Feed 已标记为陈旧", fmt.Sprintf("快照：%s\n生成时间：%s\nPriceAI 已将当前公开 Feed 标记为陈旧，已保留最近一次可用数据。", snapshot, generated)
	case storage.EventPriceAISyncFailed:
		return "PriceAI Feed 连续同步失败", fmt.Sprintf("连续失败次数：%d\n最近快照：%s\n错误：%s", state.ConsecutiveFailures, snapshot, strings.TrimSpace(state.LastError))
	default:
		return "PriceAI Feed 已恢复", fmt.Sprintf("快照：%s\n生成时间：%s\nPriceAI Feed 状态已恢复。", snapshot, generated)
	}
}

func (s *Service) dispatchNotificationCandidates(ctx context.Context, candidates []NotificationCandidate) {
	if s == nil || s.dispatcher == nil || s.repo == nil {
		return
	}
	for _, candidate := range candidates {
		event := primaryPriceAIEvent(candidate.Events)
		if event == "" {
			continue
		}
		message := priceAINotificationMessage(candidate, event)
		if err := s.dispatcher.Dispatch(ctx, message); err != nil {
			if s.log != nil {
				s.log.Warn("dispatch priceai product notification failed", "target_id", candidate.WatchTargetID, "product_id", candidate.ProductID, "event", event, "err", err)
			}
			continue
		}
		if err := s.repo.MarkWatchTargetNotified(candidate.WatchTargetID, candidate.SnapshotID, s.currentTime()); err != nil && s.log != nil {
			s.log.Warn("mark priceai watch target notified failed", "target_id", candidate.WatchTargetID, "snapshot_id", candidate.SnapshotID, "err", err)
		}
	}
}

func primaryPriceAIEvent(events []storage.NotificationEvent) storage.NotificationEvent {
	for _, preferred := range []storage.NotificationEvent{
		storage.EventPriceAITargetPriceHit,
		storage.EventPriceAIOutOfStock,
		storage.EventPriceAIRestocked,
		storage.EventPriceAINewPublicLowestOffer,
		storage.EventPriceAILowestPriceDropped,
	} {
		for _, event := range events {
			if event == preferred {
				return event
			}
		}
	}
	return ""
}

func priceAINotificationMessage(candidate NotificationCandidate, event storage.NotificationEvent) notify.Message {
	generated := "未知"
	if !candidate.ProductGeneratedAt.IsZero() {
		generated = candidate.ProductGeneratedAt.UTC().Format(time.RFC3339)
	}
	lines := []string{
		"PriceAI 公开 Feed 监测到变化。",
		"信号：" + priceAIEventLabels(candidate.Events),
		"快照：" + candidate.SnapshotID,
		"产品时间：" + generated,
		"最低公开价：" + formatPriceAIPrice(candidate.PreviousLowestPrice, candidate.PreviousLowestPriceCurrency) + " -> " + formatPriceAIPrice(candidate.CurrentLowestPrice, candidate.CurrentLowestPriceCurrency),
		fmt.Sprintf("在售报价数：%d -> %d", candidate.PreviousInStockCount, candidate.CurrentInStockCount),
		"查看：/priceai?product=" + url.QueryEscape(candidate.ProductSlug),
	}
	return notify.Message{
		Event:           event,
		PriceAITargetID: candidate.WatchTargetID,
		Subject:         fmt.Sprintf("%s PriceAI 报价变化", candidate.ProductName),
		Body:            strings.Join(lines, "\n"),
		Extra: map[string]any{
			"product_id":        candidate.ProductID,
			"product_slug":      candidate.ProductSlug,
			"snapshot_id":       candidate.SnapshotID,
			"priceai_events":    candidate.Events,
			"public_board_only": true,
		},
	}
}

func priceAIEventLabels(events []storage.NotificationEvent) string {
	labels := make([]string, 0, len(events))
	for _, event := range events {
		switch event {
		case storage.EventPriceAILowestPriceDropped:
			labels = append(labels, "最低公开价下降")
		case storage.EventPriceAITargetPriceHit:
			labels = append(labels, "达到目标价")
		case storage.EventPriceAIOutOfStock:
			labels = append(labels, "公开可售报价归零")
		case storage.EventPriceAIRestocked:
			labels = append(labels, "公开可售报价恢复")
		case storage.EventPriceAINewPublicLowestOffer:
			labels = append(labels, "出现新的公开最低报价")
		}
	}
	return strings.Join(labels, "、")
}

func formatPriceAIPrice(value *float64, currency *string) string {
	if value == nil {
		return "未知"
	}
	price := fmt.Sprintf("%g", *value)
	if code := strings.TrimSpace(stringValue(currency)); code != "" {
		return price + " " + code
	}
	return price
}

func applyPointer(next *storage.PriceAIFeedState, pointer *Pointer) {
	next.SnapshotID = pointer.SnapshotID
	next.SnapshotURL = pointer.SnapshotURL
	next.SchemaVersion = pointer.SchemaVersion
	next.GeneratedAt = &pointer.GeneratedAt
	next.PublishedAt = &pointer.PublishedAt
	next.FeedStale = pointer.Stale
}

func applyPointerCacheHeaders(state *storage.PriceAIFeedState, fetched *PointerFetch) {
	if fetched == nil {
		return
	}
	if fetched.ETag != "" {
		state.ETag = fetched.ETag
	}
	if fetched.LastModified != "" {
		state.LastModified = fetched.LastModified
	}
}

func (s *Service) importSnapshot(ctx context.Context, state *storage.PriceAIFeedState, pointer *Pointer, fetched *PointerFetch, prepared *preparedSnapshot, started time.Time) (SyncResult, error) {
	next := *state
	previousState := *state
	finished := s.currentTime()
	result := SyncResult{
		SnapshotID:    pointer.SnapshotID,
		ProductsCount: len(prepared.products),
		OffersCount:   prepared.offerCount,
	}
	var healthEvents []storage.NotificationEvent
	err := s.repo.Transaction(func(tx *storage.PriceAI) error {
		for _, product := range prepared.products {
			existing, err := tx.FindProductByRemoteID(product.product.ID)
			if err != nil {
				return err
			}
			var oldBoard []storage.PriceAIPublicBoardEntry
			if existing != nil {
				oldBoard, err = tx.ListCurrentBoardEntries(existing.ID)
				if err != nil {
					return err
				}
			}
			stored, err := tx.UpsertProduct(product.toStorageProduct(pointer.SnapshotID, finished))
			if err != nil {
				return err
			}
			if err := importPreparedBoards(tx, stored.ID, pointer.SnapshotID, product, finished); err != nil {
				return err
			}
			changes := productChanges(existing, stored, oldBoard, product.boards)
			watch, err := tx.FindWatchTargetByProductID(stored.ID)
			if err != nil {
				return err
			}
			if targetPriceHit(existing, stored, watch) {
				changes = append(changes, priceAIChange{event: storage.PriceAIChangeTargetPriceHit})
			}
			if len(changes) > 0 {
				if existing != nil {
					result.ChangedProductsCount++
				}
			}
			if candidate := notificationCandidate(existing, stored, watch, pointer.SnapshotID, changes, oldBoard, product.boards, finished); candidate != nil {
				result.Notifications = append(result.Notifications, *candidate)
			}
		}

		if _, err := tx.MarkProductsMissingFromLatest(pointer.SnapshotID, finished); err != nil {
			return err
		}
		if _, err := tx.PruneCurrentBoards(pointer.SnapshotID); err != nil {
			return err
		}
		applyPointer(&next, pointer)
		applyPointerCacheHeaders(&next, fetched)
		next.LastSuccessAt = &finished
		next.ConsecutiveFailures = 0
		next.LastError = ""
		if err := seedDefaultWatchTargets(tx, &next, pointer.SnapshotID); err != nil {
			return err
		}
		if err := tx.UpsertFeedState(&next); err != nil {
			return err
		}
		healthEvents = feedHealthNotificationEvents(previousState, next)
		return tx.AppendSyncLog(&storage.PriceAISyncLog{
			JobKind:              storage.PriceAISyncJobFeed,
			SnapshotID:           pointer.SnapshotID,
			Success:              true,
			ProductsCount:        result.ProductsCount,
			OffersCount:          result.OffersCount,
			ChangedProductsCount: result.ChangedProductsCount,
			StartedAt:            started,
			FinishedAt:           finished,
			DurationMS:           finished.Sub(started).Milliseconds(),
		})
	})
	if err != nil {
		return SyncResult{}, err
	}
	s.dispatchNotificationCandidates(ctx, result.Notifications)
	s.dispatchFeedHealth(ctx, healthEvents, &next)
	return result, nil
}

func seedDefaultWatchTargets(tx *storage.PriceAI, state *storage.PriceAIFeedState, snapshotID string) error {
	seeded, err := decodeSeededSlugs(state.DefaultWatchSeededSlugsJSON)
	if err != nil {
		return err
	}
	for _, slug := range defaultWatchSlugs {
		if _, exists := seeded[slug]; exists {
			continue
		}
		product, err := tx.FindProductBySlug(slug)
		if err != nil {
			return err
		}
		if product == nil || product.LastSnapshotID != snapshotID || product.MissingFromLatestAt != nil {
			continue
		}
		watch, err := tx.FindWatchTargetByProductID(product.ID)
		if err != nil {
			return err
		}
		if watch == nil {
			if err := tx.CreateWatchTarget(&storage.PriceAIWatchTarget{
				ProductID:          product.ID,
				MonitorEnabled:     true,
				NotifyEnabled:      false,
				BaselineSnapshotID: snapshotID,
			}); err != nil {
				return err
			}
		}
		seeded[slug] = struct{}{}
	}
	ordered := make([]string, 0, len(seeded))
	for slug := range seeded {
		ordered = append(ordered, slug)
	}
	sort.Strings(ordered)
	encoded, err := json.Marshal(ordered)
	if err != nil {
		return err
	}
	state.DefaultWatchSeededSlugsJSON = string(encoded)
	return nil
}

func decodeSeededSlugs(raw string) (map[string]struct{}, error) {
	if strings.TrimSpace(raw) == "" {
		return make(map[string]struct{}), nil
	}
	var list []string
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return nil, fmt.Errorf("decode priceai seeded slugs: %w", err)
	}
	set := make(map[string]struct{}, len(list))
	for _, slug := range list {
		slug = strings.TrimSpace(slug)
		if slug != "" {
			set[slug] = struct{}{}
		}
	}
	return set, nil
}

func importPreparedBoards(tx *storage.PriceAI, productID uint, snapshotID string, product preparedProduct, now time.Time) error {
	for _, preset := range product.product.Presets {
		if _, err := tx.UpsertPreset(&storage.PriceAIPreset{
			ProductID:      productID,
			RemoteID:       preset.ID,
			Label:          preset.Label,
			GroupName:      preset.Group,
			Description:    preset.Description,
			Total:          preset.Total,
			GeneratedAt:    preset.GeneratedAt,
			LastSnapshotID: snapshotID,
			RawJSON:        truncatePriceAIRawJSON(preset.RawJSON),
		}); err != nil {
			return err
		}
	}
	offers := make(map[string]uint, len(product.boards))
	for _, board := range product.boards {
		if _, exists := offers[board.dedupeKey]; exists {
			continue
		}
		offer, err := tx.UpsertOffer(&storage.PriceAIOffer{
			ProductID:       productID,
			RemoteID:        board.offer.ID,
			DedupeKey:       board.dedupeKey,
			SourceID:        stringValue(board.offer.SourceID),
			SourceName:      board.offer.SourceName,
			SourceStoreName: stringValue(board.offer.SourceStoreName),
			MerchantKey:     board.merchantKey,
			Title:           strings.TrimSpace(board.offer.Title),
			NormalizedTitle: board.normalizedTitle,
			Price:           board.offer.Price,
			Currency:        strings.TrimSpace(board.offer.Currency),
			Status:          strings.TrimSpace(board.offer.Status),
			URL:             board.canonicalURL,
			LastSnapshotID:  snapshotID,
			FirstSeenAt:     now,
			LastSeenAt:      now,
			RawJSON:         truncatePriceAIRawJSON(board.offer.RawJSON),
		})
		if err != nil {
			return err
		}
		offers[board.dedupeKey] = offer.ID
	}
	for _, board := range product.boards {
		if _, err := tx.UpsertOfferRanking(&storage.PriceAIOfferRanking{
			ProductID:        productID,
			OfferID:          offers[board.dedupeKey],
			BoardKind:        board.kind,
			PresetID:         board.presetID,
			Rank:             board.rank,
			BoardGeneratedAt: board.generatedAt,
			LastSnapshotID:   snapshotID,
		}); err != nil {
			return err
		}
	}
	return nil
}

type preparedSnapshot struct {
	products   []preparedProduct
	offerCount int
}

type preparedProduct struct {
	product FeedProduct
	boards  []preparedBoardOffer
}

type preparedBoardOffer struct {
	offer           FeedOffer
	kind            storage.PriceAIBoardKind
	presetID        string
	rank            int
	generatedAt     time.Time
	dedupeKey       string
	merchantKey     string
	normalizedTitle string
	canonicalURL    string
}

func prepareSnapshot(pointer *Pointer, snapshot *Snapshot) (*preparedSnapshot, error) {
	if pointer == nil || snapshot == nil {
		return nil, fmt.Errorf("priceai snapshot input is empty")
	}
	if snapshot.SchemaVersion != FeedSchemaVersion {
		return nil, fmt.Errorf("unsupported priceai snapshot schema version %q", snapshot.SchemaVersion)
	}
	if snapshot.SnapshotID != pointer.SnapshotID {
		return nil, fmt.Errorf("priceai snapshot id %q does not match pointer %q", snapshot.SnapshotID, pointer.SnapshotID)
	}
	if snapshot.GeneratedAt.IsZero() || snapshot.PublishedAt.IsZero() {
		return nil, fmt.Errorf("priceai snapshot timestamps are required")
	}
	if snapshot.GeneratedAt != pointer.GeneratedAt || snapshot.PublishedAt != pointer.PublishedAt || snapshot.Stale != pointer.Stale {
		return nil, fmt.Errorf("priceai snapshot metadata does not match pointer")
	}
	if len(snapshot.Products) != pointer.ProductCount {
		return nil, fmt.Errorf("priceai snapshot product count %d does not match pointer %d", len(snapshot.Products), pointer.ProductCount)
	}
	prepared := &preparedSnapshot{products: make([]preparedProduct, 0, len(snapshot.Products))}
	remoteIDs := make(map[string]struct{}, len(snapshot.Products))
	slugs := make(map[string]struct{}, len(snapshot.Products))
	for _, product := range snapshot.Products {
		if strings.TrimSpace(product.ID) == "" || strings.TrimSpace(product.Slug) == "" || strings.TrimSpace(product.Name) == "" {
			return nil, fmt.Errorf("priceai snapshot product id, slug, and name are required")
		}
		if _, exists := remoteIDs[product.ID]; exists {
			return nil, fmt.Errorf("duplicate priceai product id %q", product.ID)
		}
		if _, exists := slugs[product.Slug]; exists {
			return nil, fmt.Errorf("duplicate priceai product slug %q", product.Slug)
		}
		remoteIDs[product.ID] = struct{}{}
		slugs[product.Slug] = struct{}{}
		if product.OfferCount < 0 || product.InStockCount < 0 || product.Total < 0 {
			return nil, fmt.Errorf("priceai product %q has negative counts", product.ID)
		}
		if product.LowestPrice != nil && (!isFinite(*product.LowestPrice) || *product.LowestPrice < 0) {
			return nil, fmt.Errorf("priceai product %q has invalid lowest price", product.ID)
		}
		if product.SnapshotGeneratedAt.IsZero() {
			return nil, fmt.Errorf("priceai product %q snapshot_generated_at is required", product.ID)
		}
		if product.LowestOffer != nil {
			if _, err := prepareOffer(*product.LowestOffer); err != nil {
				return nil, fmt.Errorf("priceai product %q lowest_offer: %w", product.ID, err)
			}
		}
		if len(product.TopOffers) > 5 {
			return nil, fmt.Errorf("priceai product %q default board has more than five offers", product.ID)
		}
		item := preparedProduct{product: product}
		boards, err := prepareBoard(product.TopOffers, storage.PriceAIBoardDefault, "", product.SnapshotGeneratedAt)
		if err != nil {
			return nil, fmt.Errorf("priceai product %q default board: %w", product.ID, err)
		}
		item.boards = append(item.boards, boards...)
		presetIDs := make(map[string]struct{}, len(product.Presets))
		for _, preset := range product.Presets {
			if strings.TrimSpace(preset.ID) == "" || preset.GeneratedAt.IsZero() || preset.Total < 0 {
				return nil, fmt.Errorf("priceai product %q has malformed preset", product.ID)
			}
			if _, exists := presetIDs[preset.ID]; exists {
				return nil, fmt.Errorf("priceai product %q has duplicate preset %q", product.ID, preset.ID)
			}
			presetIDs[preset.ID] = struct{}{}
			if len(preset.TopOffers) > 5 {
				return nil, fmt.Errorf("priceai preset %q has more than five offers", preset.ID)
			}
			boards, err := prepareBoard(preset.TopOffers, storage.PriceAIBoardPreset, preset.ID, preset.GeneratedAt)
			if err != nil {
				return nil, fmt.Errorf("priceai preset %q: %w", preset.ID, err)
			}
			item.boards = append(item.boards, boards...)
		}
		uniqueOffers := make(map[string]struct{}, len(item.boards))
		for _, board := range item.boards {
			uniqueOffers[board.dedupeKey] = struct{}{}
		}
		prepared.offerCount += len(uniqueOffers)
		prepared.products = append(prepared.products, item)
	}
	return prepared, nil
}

func prepareBoard(offers []FeedOffer, kind storage.PriceAIBoardKind, presetID string, generatedAt time.Time) ([]preparedBoardOffer, error) {
	result := make([]preparedBoardOffer, 0, len(offers))
	seen := make(map[string]struct{}, len(offers))
	for index, offer := range offers {
		prepared, err := prepareOffer(offer)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[prepared.dedupeKey]; exists {
			return nil, fmt.Errorf("duplicate offer %q in one public board", prepared.dedupeKey)
		}
		seen[prepared.dedupeKey] = struct{}{}
		prepared.kind = kind
		prepared.presetID = presetID
		prepared.rank = index + 1
		prepared.generatedAt = generatedAt
		result = append(result, prepared)
	}
	return result, nil
}

func prepareOffer(offer FeedOffer) (preparedBoardOffer, error) {
	if strings.TrimSpace(offer.Title) == "" || !isFinite(offer.Price) || offer.Price < 0 {
		return preparedBoardOffer{}, fmt.Errorf("offer title and non-negative price are required")
	}
	canonicalURL, err := canonicalOfferURL(offer.URL)
	if err != nil {
		return preparedBoardOffer{}, err
	}
	dedupeKey := strings.TrimSpace(offer.ID)
	if dedupeKey == "" {
		dedupeKey = canonicalURL
	}
	merchantKey := strings.TrimSpace(stringValue(offer.SourceID))
	if merchantKey == "" {
		store := normalizeTitle(stringValue(offer.SourceStoreName))
		if store == "" {
			store = normalizeTitle(offer.SourceName)
		}
		parsed, _ := url.Parse(canonicalURL)
		merchantKey = strings.ToLower(parsed.Host) + "|" + store
	}
	return preparedBoardOffer{
		offer:           offer,
		dedupeKey:       dedupeKey,
		merchantKey:     merchantKey,
		normalizedTitle: normalizeTitle(offer.Title),
		canonicalURL:    canonicalURL,
	}, nil
}

func canonicalOfferURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed == nil || parsed.Host == "" || parsed.User != nil {
		return "", fmt.Errorf("offer URL is malformed")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("offer URL scheme %q is not allowed", parsed.Scheme)
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Fragment = ""
	return parsed.String(), nil
}

func normalizeTitle(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(value))), " ")
}

func isFinite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

func (p preparedProduct) toStorageProduct(snapshotID string, now time.Time) *storage.PriceAIProduct {
	latestSeen := p.product.SnapshotGeneratedAt
	if p.product.LatestSeenAt != nil {
		latestSeen = *p.product.LatestSeenAt
	}
	return &storage.PriceAIProduct{
		RemoteID:                   p.product.ID,
		Slug:                       p.product.Slug,
		Name:                       p.product.Name,
		Platform:                   p.product.Platform,
		ProductType:                p.product.ProductType,
		Spec:                       stringValue(p.product.Spec),
		Summary:                    stringValue(p.product.Summary),
		OfferCount:                 p.product.OfferCount,
		InStockCount:               p.product.InStockCount,
		LowestPrice:                p.product.LowestPrice,
		LowestPriceCurrency:        productCurrency(p.product),
		LatestSeenAt:               latestSeen,
		ProductSnapshotGeneratedAt: p.product.SnapshotGeneratedAt,
		LastSnapshotID:             snapshotID,
		FirstSeenAt:                now,
		LastSeenAt:                 now,
		RawJSON:                    truncatePriceAIRawJSON(p.product.RawJSON),
	}
}

const (
	maxPriceAIRawJSONBytes         = 64 * 1024
	priceAIRawJSONTruncationSuffix = "\n[truncated]"
)

// truncatePriceAIRawJSON keeps audit data bounded per current-state row. A
// truncated value is intentionally marked rather than masquerading as valid
// JSON; runtime code never parses the persisted audit copy.
func truncatePriceAIRawJSON(raw []byte) string {
	if len(raw) <= maxPriceAIRawJSONBytes {
		return string(raw)
	}
	limit := maxPriceAIRawJSONBytes - len(priceAIRawJSONTruncationSuffix)
	for limit > 0 && raw[limit]&0xc0 == 0x80 {
		limit--
	}
	return string(raw[:limit]) + priceAIRawJSONTruncationSuffix
}

func productCurrency(product FeedProduct) *string {
	if product.LowestOffer != nil {
		if value := strings.TrimSpace(product.LowestOffer.Currency); value != "" {
			return &value
		}
	}
	if product.LowestPrice != nil {
		for _, offer := range product.TopOffers {
			if offer.Price == *product.LowestPrice {
				if value := strings.TrimSpace(offer.Currency); value != "" {
					return &value
				}
			}
		}
	}
	return nil
}

type priceAIChange struct {
	event storage.PriceAIChangeEvent
}

func productChanges(previous, current *storage.PriceAIProduct, oldBoard []storage.PriceAIPublicBoardEntry, newBoard []preparedBoardOffer) []priceAIChange {
	if previous == nil {
		return []priceAIChange{{event: storage.PriceAIChangeBaselineCreated}}
	}
	changes := make([]priceAIChange, 0, 5)
	if !sameCurrency(previous.LowestPriceCurrency, current.LowestPriceCurrency) {
		changes = append(changes, priceAIChange{event: storage.PriceAIChangeCurrencyChanged})
	} else if comparablePriceChanged(previous, current) {
		changes = append(changes, priceAIChange{event: storage.PriceAIChangeLowestPriceChanged})
	}
	if previous.InStockCount != current.InStockCount {
		changes = append(changes, priceAIChange{event: storage.PriceAIChangeInStockCountChanged})
	}
	if previous.OfferCount != current.OfferCount {
		changes = append(changes, priceAIChange{event: storage.PriceAIChangeOfferCountChanged})
	}
	if !sameBoardEntries(oldBoard, newBoard) {
		changes = append(changes, priceAIChange{event: storage.PriceAIChangePublicBoardChanged})
	}
	return changes
}

func sameCurrency(left, right *string) bool {
	return strings.EqualFold(strings.TrimSpace(stringValue(left)), strings.TrimSpace(stringValue(right)))
}

func comparablePriceChanged(previous, current *storage.PriceAIProduct) bool {
	if previous.LowestPrice == nil || current.LowestPrice == nil || !sameCurrency(previous.LowestPriceCurrency, current.LowestPriceCurrency) || strings.TrimSpace(stringValue(current.LowestPriceCurrency)) == "" {
		return false
	}
	return *previous.LowestPrice != *current.LowestPrice
}

func sameBoardEntries(previous []storage.PriceAIPublicBoardEntry, current []preparedBoardOffer) bool {
	if len(previous) != len(current) {
		return false
	}
	old := make(map[string]string, len(previous))
	for _, item := range previous {
		old[boardEntryKey(item.BoardKind, item.PresetID, item.DedupeKey)] = fmt.Sprintf("%d|%g|%s|%s", item.Rank, item.Price, item.Status, item.Currency)
	}
	for _, item := range current {
		key := boardEntryKey(item.kind, item.presetID, item.dedupeKey)
		value := fmt.Sprintf("%d|%g|%s|%s", item.rank, item.offer.Price, strings.TrimSpace(item.offer.Status), strings.TrimSpace(item.offer.Currency))
		if old[key] != value {
			return false
		}
	}
	return true
}

func boardEntryKey(kind storage.PriceAIBoardKind, presetID, dedupeKey string) string {
	return string(kind) + "\x00" + presetID + "\x00" + dedupeKey
}

func targetPriceHit(previous, current *storage.PriceAIProduct, target *storage.PriceAIWatchTarget) bool {
	if previous == nil || current == nil || target == nil || target.TargetPrice == nil || target.TargetPriceCurrency == nil || previous.LowestPrice == nil || current.LowestPrice == nil {
		return false
	}
	if !sameCurrency(target.TargetPriceCurrency, previous.LowestPriceCurrency) || !sameCurrency(target.TargetPriceCurrency, current.LowestPriceCurrency) {
		return false
	}
	return *previous.LowestPrice > *target.TargetPrice && *current.LowestPrice <= *target.TargetPrice
}

func notificationCandidate(previous, current *storage.PriceAIProduct, target *storage.PriceAIWatchTarget, snapshotID string, changes []priceAIChange, oldBoard []storage.PriceAIPublicBoardEntry, newBoard []preparedBoardOffer, now time.Time) *NotificationCandidate {
	if previous == nil || current == nil || target == nil || !target.MonitorEnabled || !target.NotifyEnabled || target.BaselineSnapshotID == snapshotID {
		return nil
	}
	if target.LastNotifiedSnapshotID != nil && *target.LastNotifiedSnapshotID == snapshotID {
		return nil
	}
	if target.NotificationCooldownMinutes > 0 && target.LastNotifiedAt != nil && now.Before(target.LastNotifiedAt.Add(time.Duration(target.NotificationCooldownMinutes)*time.Minute)) {
		return nil
	}
	events := make([]storage.NotificationEvent, 0, 4)
	for _, change := range changes {
		switch change.event {
		case storage.PriceAIChangeTargetPriceHit:
			events = appendUniqueNotificationEvent(events, storage.EventPriceAITargetPriceHit)
		case storage.PriceAIChangeLowestPriceChanged:
			if isMeaningfulPriceDrop(previous, current, target) {
				events = appendUniqueNotificationEvent(events, storage.EventPriceAILowestPriceDropped)
			}
		case storage.PriceAIChangeInStockCountChanged:
			if previous.InStockCount > 0 && current.InStockCount == 0 {
				events = appendUniqueNotificationEvent(events, storage.EventPriceAIOutOfStock)
			}
			if previous.InStockCount == 0 && current.InStockCount > 0 {
				events = appendUniqueNotificationEvent(events, storage.EventPriceAIRestocked)
			}
		}
	}
	if hasNewPublicLowestOffer(previous, current, oldBoard, newBoard) {
		events = appendUniqueNotificationEvent(events, storage.EventPriceAINewPublicLowestOffer)
	}
	if len(events) == 0 {
		return nil
	}
	return &NotificationCandidate{
		ProductID:                   current.ID,
		ProductName:                 current.Name,
		ProductSlug:                 current.Slug,
		WatchTargetID:               target.ID,
		SnapshotID:                  snapshotID,
		Events:                      events,
		PreviousLowestPrice:         previous.LowestPrice,
		PreviousLowestPriceCurrency: previous.LowestPriceCurrency,
		CurrentLowestPrice:          current.LowestPrice,
		CurrentLowestPriceCurrency:  current.LowestPriceCurrency,
		PreviousInStockCount:        previous.InStockCount,
		CurrentInStockCount:         current.InStockCount,
		ProductGeneratedAt:          current.ProductSnapshotGeneratedAt,
	}
}

func appendUniqueNotificationEvent(events []storage.NotificationEvent, event storage.NotificationEvent) []storage.NotificationEvent {
	for _, existing := range events {
		if existing == event {
			return events
		}
	}
	return append(events, event)
}

func hasNewPublicLowestOffer(previous, current *storage.PriceAIProduct, oldBoard []storage.PriceAIPublicBoardEntry, newBoard []preparedBoardOffer) bool {
	if previous == nil || current == nil || previous.LowestPrice == nil || current.LowestPrice == nil || !sameCurrency(previous.LowestPriceCurrency, current.LowestPriceCurrency) {
		return false
	}
	seen := make(map[string]struct{}, len(oldBoard))
	for _, board := range oldBoard {
		seen[board.DedupeKey] = struct{}{}
	}
	for _, board := range newBoard {
		if _, exists := seen[board.dedupeKey]; exists {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(board.offer.Currency), strings.TrimSpace(stringValue(current.LowestPriceCurrency))) {
			continue
		}
		if board.offer.Price < *previous.LowestPrice {
			return true
		}
	}
	return false
}

func isMeaningfulPriceDrop(previous, current *storage.PriceAIProduct, target *storage.PriceAIWatchTarget) bool {
	if target.PriceDropPercent == nil || previous.LowestPrice == nil || current.LowestPrice == nil || *previous.LowestPrice <= 0 || !sameCurrency(previous.LowestPriceCurrency, current.LowestPriceCurrency) {
		return false
	}
	drop := (*previous.LowestPrice - *current.LowestPrice) / *previous.LowestPrice * 100
	return drop > 0 && drop >= *target.PriceDropPercent
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (s *Service) currentTime() time.Time {
	if s.now == nil {
		return time.Now().UTC()
	}
	return s.now().UTC()
}
