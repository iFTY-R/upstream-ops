package priceai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

const (
	DiscoveryURL = "https://priceai.cc/.well-known/price-radar.json"
	PointerURL   = "https://data.priceai.cc/latest.json"
	SchemaURL    = "https://priceai.cc/price-radar-v1.schema.json"

	priceAIWebHost  = "priceai.cc"
	priceAIDataHost = "data.priceai.cc"

	defaultHTTPTimeout   = 20 * time.Second
	discoveryMaxBytes    = 1 << 20
	pointerMaxBytes      = 1 << 20
	snapshotMaxBytes     = 16 << 20
	minimumRefreshPeriod = time.Minute
)

type feedEndpoints struct {
	discoveryURL string
	pointerURL   string
	schemaURL    string
	webHost      string
	dataHost     string
	allowHTTP    bool
}

func productionFeedEndpoints() feedEndpoints {
	return feedEndpoints{
		discoveryURL: DiscoveryURL,
		pointerURL:   PointerURL,
		schemaURL:    SchemaURL,
		webHost:      priceAIWebHost,
		dataHost:     priceAIDataHost,
	}
}

type FeedClient struct {
	http      *http.Client
	endpoints feedEndpoints
}

func NewFeedClient() *FeedClient {
	return newFeedClient(nil, productionFeedEndpoints())
}

func newFeedClient(client *http.Client, endpoints feedEndpoints) *FeedClient {
	if client == nil {
		client = &http.Client{}
	}
	copyClient := *client
	if copyClient.Timeout <= 0 {
		copyClient.Timeout = defaultHTTPTimeout
	}
	// Never follow a redirect before checking its status and Location. A 3xx is
	// rejected by get rather than silently changing hosts or schemes.
	copyClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &FeedClient{http: &copyClient, endpoints: endpoints}
}

type PointerFetch struct {
	Pointer      *Pointer
	ETag         string
	LastModified string
	NotModified  bool
}

func (c *FeedClient) FetchDiscovery(ctx context.Context) (*DiscoveryDocument, error) {
	body, _, status, err := c.get(ctx, c.endpoints.discoveryURL, c.endpoints.webHost, nil, discoveryMaxBytes)
	if err != nil {
		return nil, fmt.Errorf("fetch priceai discovery: %w", err)
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("fetch priceai discovery: unexpected status %d", status)
	}
	var doc DiscoveryDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("decode priceai discovery: %w", err)
	}
	if err := c.validateDiscovery(&doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

func (c *FeedClient) FetchPointer(ctx context.Context, etag, lastModified string) (*PointerFetch, error) {
	headers := make(http.Header)
	if value := strings.TrimSpace(etag); value != "" {
		headers.Set("If-None-Match", value)
	}
	if value := strings.TrimSpace(lastModified); value != "" {
		headers.Set("If-Modified-Since", value)
	}
	body, responseHeaders, status, err := c.get(ctx, c.endpoints.pointerURL, c.endpoints.dataHost, headers, pointerMaxBytes)
	if err != nil {
		return nil, fmt.Errorf("fetch priceai pointer: %w", err)
	}
	result := &PointerFetch{
		ETag:         responseHeaders.Get("ETag"),
		LastModified: responseHeaders.Get("Last-Modified"),
	}
	if status == http.StatusNotModified {
		result.NotModified = true
		return result, nil
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("fetch priceai pointer: unexpected status %d", status)
	}
	var pointer Pointer
	if err := json.Unmarshal(body, &pointer); err != nil {
		return nil, fmt.Errorf("decode priceai pointer: %w", err)
	}
	if err := c.validatePointer(&pointer); err != nil {
		return nil, err
	}
	result.Pointer = &pointer
	return result, nil
}

func (c *FeedClient) FetchSnapshot(ctx context.Context, snapshotURL string) (*Snapshot, error) {
	body, _, status, err := c.get(ctx, snapshotURL, c.endpoints.dataHost, nil, snapshotMaxBytes)
	if err != nil {
		return nil, fmt.Errorf("fetch priceai snapshot: %w", err)
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("fetch priceai snapshot: unexpected status %d", status)
	}
	var snapshot Snapshot
	if err := json.Unmarshal(body, &snapshot); err != nil {
		return nil, fmt.Errorf("decode priceai snapshot: %w", err)
	}
	return &snapshot, nil
}

func (c *FeedClient) get(ctx context.Context, rawURL, expectedHost string, headers http.Header, maxBytes int64) ([]byte, http.Header, int, error) {
	if _, err := c.validateURL(rawURL, expectedHost); err != nil {
		return nil, nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, nil, 0, err
	}
	if headers.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}
	req.Header.Set("User-Agent", "upstream-ops-priceai-radar/1.0")
	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	response, err := c.http.Do(req)
	if err != nil {
		return nil, nil, 0, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotModified {
		return nil, response.Header, response.StatusCode, nil
	}
	if response.StatusCode >= http.StatusMultipleChoices && response.StatusCode < http.StatusBadRequest {
		return nil, response.Header, response.StatusCode, fmt.Errorf("redirect response %d rejected", response.StatusCode)
	}
	body, err := readLimited(response.Body, maxBytes)
	if err != nil {
		return nil, response.Header, response.StatusCode, err
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, response.Header, response.StatusCode, fmt.Errorf("unexpected status %d", response.StatusCode)
	}
	return body, response.Header, response.StatusCode, nil
}

func readLimited(reader io.Reader, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		return nil, fmt.Errorf("response size limit must be positive")
	}
	body, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("response exceeds %d byte limit", maxBytes)
	}
	return body, nil
}

func (c *FeedClient) validateDiscovery(doc *DiscoveryDocument) error {
	if doc == nil {
		return fmt.Errorf("priceai discovery is empty")
	}
	if doc.SchemaVersion != DiscoverySchemaVersion {
		return fmt.Errorf("unsupported priceai discovery schema version %q", doc.SchemaVersion)
	}
	if _, err := c.validateURL(doc.LatestURL, c.endpoints.dataHost); err != nil {
		return fmt.Errorf("priceai discovery latest_url: %w", err)
	}
	if _, err := c.validateURL(doc.SchemaURL, c.endpoints.webHost); err != nil {
		return fmt.Errorf("priceai discovery schema_url: %w", err)
	}
	if !sameURL(doc.LatestURL, c.endpoints.pointerURL) {
		return fmt.Errorf("priceai discovery latest_url is not the documented pointer")
	}
	if !sameURL(doc.SchemaURL, c.endpoints.schemaURL) {
		return fmt.Errorf("priceai discovery schema_url is not the documented schema")
	}
	if doc.RefreshIntervalSeconds < int(minimumRefreshPeriod/time.Second) {
		return fmt.Errorf("priceai discovery refresh interval %d is below one minute", doc.RefreshIntervalSeconds)
	}
	return nil
}

func (c *FeedClient) validatePointer(pointer *Pointer) error {
	if pointer == nil {
		return fmt.Errorf("priceai pointer is empty")
	}
	if pointer.SchemaVersion != FeedSchemaVersion {
		return fmt.Errorf("unsupported priceai feed schema version %q", pointer.SchemaVersion)
	}
	if strings.TrimSpace(pointer.SnapshotID) == "" {
		return fmt.Errorf("priceai pointer snapshot_id is required")
	}
	if pointer.GeneratedAt.IsZero() || pointer.PublishedAt.IsZero() {
		return fmt.Errorf("priceai pointer timestamps are required")
	}
	if pointer.ProductCount < 0 || pointer.ResourceCount < 0 {
		return fmt.Errorf("priceai pointer counts must not be negative")
	}
	snapshot, err := c.validateURL(pointer.SnapshotURL, c.endpoints.dataHost)
	if err != nil {
		return fmt.Errorf("priceai pointer snapshot_url: %w", err)
	}
	expectedPath := "/v1/snapshots/" + url.PathEscape(pointer.SnapshotID) + ".json"
	if snapshot.Path != expectedPath || snapshot.RawQuery != "" {
		return fmt.Errorf("priceai pointer snapshot_url is not immutable for snapshot %q", pointer.SnapshotID)
	}
	return nil
}

func (c *FeedClient) validateURL(rawURL, expectedHost string) (*url.URL, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse URL: %w", err)
	}
	if parsed.User != nil || parsed.Fragment != "" || parsed.Host == "" {
		return nil, fmt.Errorf("URL is malformed")
	}
	if !c.endpoints.allowHTTP && parsed.Scheme != "https" {
		return nil, fmt.Errorf("URL scheme %q is not allowed", parsed.Scheme)
	}
	if c.endpoints.allowHTTP && parsed.Scheme != "https" && parsed.Scheme != "http" {
		return nil, fmt.Errorf("URL scheme %q is not allowed", parsed.Scheme)
	}
	if !strings.EqualFold(parsed.Host, expectedHost) {
		return nil, fmt.Errorf("URL host %q is not allowed", parsed.Host)
	}
	return parsed, nil
}

func sameURL(left, right string) bool {
	a, errA := url.Parse(left)
	b, errB := url.Parse(right)
	if errA != nil || errB != nil {
		return false
	}
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Host, b.Host) && path.Clean(a.Path) == path.Clean(b.Path) && a.RawQuery == b.RawQuery
}
