package priceai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ifty-r/upstream-ops/backend/notify"
	"github.com/ifty-r/upstream-ops/backend/storage"
)

func TestServiceImportsChangedSnapshotsAndUsesConditionalPointer(t *testing.T) {
	now := time.Date(2026, time.July, 26, 6, 0, 0, 0, time.UTC)
	fixture := newFeedFixture(t, validSnapshot("snapshot-1", now, 20, "chatgpt-plus"))
	defer fixture.server.Close()
	service, repo := newTestService(t, fixture.server.URL, now)
	service.now = func() time.Time { return now }

	first, err := service.Sync(context.Background())
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}
	if first.ProductsCount != 1 || first.OffersCount != 1 || first.ChangedProductsCount != 0 {
		t.Fatalf("unexpected first sync result: %#v", first)
	}
	if fixture.pointerCalls() != 1 || fixture.snapshotCalls() != 1 {
		t.Fatalf("unexpected first request counts: pointer=%d snapshot=%d", fixture.pointerCalls(), fixture.snapshotCalls())
	}
	product, err := repo.FindProductBySlug("chatgpt-plus")
	if err != nil {
		t.Fatalf("find imported product: %v", err)
	}
	if product == nil || product.LowestPrice == nil || *product.LowestPrice != 20 {
		t.Fatalf("unexpected imported product: %#v", product)
	}
	watch, err := repo.FindWatchTargetByProductID(product.ID)
	if err != nil {
		t.Fatalf("find seeded watch: %v", err)
	}
	if watch == nil || !watch.MonitorEnabled || watch.NotifyEnabled || watch.BaselineSnapshotID != "snapshot-1" {
		t.Fatalf("unexpected seeded target: %#v", watch)
	}

	now = now.Add(2 * time.Minute)
	second, err := service.Sync(context.Background())
	if err != nil {
		t.Fatalf("conditional sync: %v", err)
	}
	if !second.NotModified || fixture.pointerCalls() != 2 || fixture.snapshotCalls() != 1 {
		t.Fatalf("conditional request did not reuse pointer metadata: result=%#v pointer=%d snapshot=%d", second, fixture.pointerCalls(), fixture.snapshotCalls())
	}
	if fixture.lastIfNoneMatch() != fixture.etagFor("snapshot-1") {
		t.Fatalf("If-None-Match = %q, want %q", fixture.lastIfNoneMatch(), fixture.etagFor("snapshot-1"))
	}

	now = now.Add(2 * time.Minute)
	fixture.setSnapshot(validSnapshot("snapshot-2", now, 18, "chatgpt-plus"))
	third, err := service.Sync(context.Background())
	if err != nil {
		t.Fatalf("changed snapshot sync: %v", err)
	}
	if third.SnapshotID != "snapshot-2" || third.ChangedProductsCount != 1 || fixture.snapshotCalls() != 2 {
		t.Fatalf("unexpected changed sync result: %#v; snapshots=%d", third, fixture.snapshotCalls())
	}
	product, err = repo.FindProductBySlug("chatgpt-plus")
	if err != nil {
		t.Fatalf("find updated product: %v", err)
	}
	if product.LowestPrice == nil || *product.LowestPrice != 18 || product.LastSnapshotID != "snapshot-2" {
		t.Fatalf("updated product mismatch: %#v", product)
	}
}

func TestTruncatePriceAIRawJSONKeepsAuditRowsBounded(t *testing.T) {
	tooLarge := []byte(strings.Repeat("x", maxPriceAIRawJSONBytes+1024))
	truncated := truncatePriceAIRawJSON(tooLarge)
	if len(truncated) != maxPriceAIRawJSONBytes {
		t.Fatalf("truncated length = %d, want %d", len(truncated), maxPriceAIRawJSONBytes)
	}
	if !strings.HasSuffix(truncated, priceAIRawJSONTruncationSuffix) {
		t.Fatalf("truncated payload missing suffix: %q", truncated[len(truncated)-len(priceAIRawJSONTruncationSuffix):])
	}
	if got := truncatePriceAIRawJSON([]byte(`{"id":"small"}`)); got != `{"id":"small"}` {
		t.Fatalf("small payload changed to %q", got)
	}
}

func TestServiceRejectsInvalidSnapshotWithoutReplacingCurrentState(t *testing.T) {
	now := time.Date(2026, time.July, 26, 7, 0, 0, 0, time.UTC)
	fixture := newFeedFixture(t, validSnapshot("snapshot-1", now, 20, "chatgpt-plus"))
	defer fixture.server.Close()
	service, repo := newTestService(t, fixture.server.URL, now)
	service.now = func() time.Time { return now }
	if _, err := service.Sync(context.Background()); err != nil {
		t.Fatalf("baseline sync: %v", err)
	}

	now = now.Add(2 * time.Minute)
	invalid := validSnapshot("snapshot-2", now, 18, "chatgpt-plus")
	duplicate := invalid.Products[0]
	duplicate.Name = "Duplicate"
	invalid.Products = append(invalid.Products, duplicate)
	fixture.setSnapshot(invalid)
	if _, err := service.Sync(context.Background()); err == nil {
		t.Fatal("invalid snapshot sync unexpectedly succeeded")
	}
	product, err := repo.FindProductBySlug("chatgpt-plus")
	if err != nil {
		t.Fatalf("find preserved product: %v", err)
	}
	if product == nil || product.LastSnapshotID != "snapshot-1" || product.LowestPrice == nil || *product.LowestPrice != 20 {
		t.Fatalf("invalid snapshot replaced current product: %#v", product)
	}
	state, err := repo.FindFeedState(FeedSourceKey)
	if err != nil {
		t.Fatalf("find feed state: %v", err)
	}
	if state == nil || state.ConsecutiveFailures != 1 || state.LastError == "" {
		t.Fatalf("failure state was not recorded: %#v", state)
	}
	logs, total, err := repo.ListSyncLogs(storage.PriceAISyncJobFeed, 1, 10)
	if err != nil {
		t.Fatalf("list sync logs: %v", err)
	}
	if total != 2 || len(logs) != 2 || logs[0].Success {
		t.Fatalf("failed sync log missing: total=%d logs=%#v", total, logs)
	}
}

func TestServiceEnforcesMinimumAttemptIntervalAndCoalesces(t *testing.T) {
	now := time.Date(2026, time.July, 26, 8, 0, 0, 0, time.UTC)
	fixture := newFeedFixture(t, validSnapshot("snapshot-1", now, 20, "chatgpt-plus"))
	defer fixture.server.Close()
	service, _ := newTestService(t, fixture.server.URL, now)
	service.now = func() time.Time { return now }
	if _, err := service.Sync(context.Background()); err != nil {
		t.Fatalf("baseline sync: %v", err)
	}
	now = now.Add(30 * time.Second)
	skipped, err := service.Sync(context.Background())
	if err != nil {
		t.Fatalf("sub-minute sync: %v", err)
	}
	if !skipped.Skipped || fixture.pointerCalls() != 1 {
		t.Fatalf("sub-minute attempt should not fetch pointer: %#v calls=%d", skipped, fixture.pointerCalls())
	}

	now = now.Add(2 * time.Minute)
	fixture.blockNextPointer()
	firstDone := make(chan struct{})
	var first SyncResult
	var firstErr error
	go func() {
		first, firstErr = service.Sync(context.Background())
		close(firstDone)
	}()
	fixture.waitForBlockedPointer(t)
	secondDone := make(chan struct{})
	var second SyncResult
	var secondErr error
	go func() {
		second, secondErr = service.Sync(context.Background())
		close(secondDone)
	}()
	select {
	case <-secondDone:
		t.Fatal("coalesced caller returned before the active pointer request completed")
	case <-time.After(50 * time.Millisecond):
	}
	fixture.releaseBlockedPointer()
	<-firstDone
	<-secondDone
	if firstErr != nil || secondErr != nil || first.SnapshotID != second.SnapshotID || fixture.pointerCalls() != 2 {
		t.Fatalf("concurrent sync did not coalesce: first=%#v/%v second=%#v/%v calls=%d", first, firstErr, second, secondErr, fixture.pointerCalls())
	}
}

func TestFeedClientRejectsRedirectAndOversizedBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://unexpected.example/discovery", http.StatusFound)
	}))
	defer server.Close()
	client := newTestFeedClient(t, server.URL)
	if _, err := client.FetchDiscovery(context.Background()); err == nil {
		t.Fatal("redirected discovery unexpectedly succeeded")
	}
	if _, err := readLimited(strings.NewReader("abcdef"), 3); err == nil {
		t.Fatal("oversized response unexpectedly succeeded")
	}
}

func TestServiceDispatchesCommittedCandidateAndPersistsTargetCooldown(t *testing.T) {
	now := time.Date(2026, time.July, 26, 9, 0, 0, 0, time.UTC)
	fixture := newFeedFixture(t, validSnapshot("snapshot-1", now, 20, "chatgpt-plus"))
	defer fixture.server.Close()
	service, repo := newTestService(t, fixture.server.URL, now)
	dispatcher := &recordingDispatcher{}
	service.SetDispatcher(dispatcher)
	service.now = func() time.Time { return now }

	if _, err := service.Sync(context.Background()); err != nil {
		t.Fatalf("baseline sync: %v", err)
	}
	product, err := repo.FindProductBySlug("chatgpt-plus")
	if err != nil || product == nil {
		t.Fatalf("find product: %v, %#v", err, product)
	}
	target, err := repo.FindWatchTargetByProductID(product.ID)
	if err != nil || target == nil {
		t.Fatalf("find watch target: %v, %#v", err, target)
	}
	threshold := 5.0
	target.NotifyEnabled = true
	target.PriceDropPercent = &threshold
	target.NotificationCooldownMinutes = 10
	if err := repo.UpdateWatchTarget(target); err != nil {
		t.Fatalf("update watch target: %v", err)
	}

	now = now.Add(2 * time.Minute)
	fixture.setSnapshot(validSnapshot("snapshot-2", now, 18, "chatgpt-plus"))
	result, err := service.Sync(context.Background())
	if err != nil {
		t.Fatalf("changed sync: %v", err)
	}
	if len(result.Notifications) != 1 || len(result.Notifications[0].Events) != 2 {
		t.Fatalf("unexpected notification candidates: %#v", result.Notifications)
	}
	messages := dispatcher.Messages()
	if len(messages) != 1 || messages[0].Event != storage.EventPriceAINewPublicLowestOffer {
		t.Fatalf("unexpected dispatches: %#v", messages)
	}
	target, err = repo.FindWatchTargetByID(target.ID)
	if err != nil || target == nil || target.LastNotifiedAt == nil || target.LastNotifiedSnapshotID == nil || *target.LastNotifiedSnapshotID != "snapshot-2" {
		t.Fatalf("target notification state was not persisted: %v, %#v", err, target)
	}

	now = now.Add(2 * time.Minute)
	fixture.setSnapshot(validSnapshot("snapshot-3", now, 16, "chatgpt-plus"))
	result, err = service.Sync(context.Background())
	if err != nil {
		t.Fatalf("cooldown sync: %v", err)
	}
	if len(result.Notifications) != 0 || len(dispatcher.Messages()) != 1 {
		t.Fatalf("target cooldown did not suppress repeated dispatch: result=%#v messages=%#v", result, dispatcher.Messages())
	}
}

func TestServiceDoesNotDispatchFailedImportAndOnlySignalsFeedHealthTransitions(t *testing.T) {
	now := time.Date(2026, time.July, 26, 10, 0, 0, 0, time.UTC)
	fixture := newFeedFixture(t, validSnapshot("snapshot-1", now, 20, "chatgpt-plus"))
	defer fixture.server.Close()
	service, _ := newTestService(t, fixture.server.URL, now)
	dispatcher := &recordingDispatcher{}
	service.SetDispatcher(dispatcher)
	service.now = func() time.Time { return now }
	if _, err := service.Sync(context.Background()); err != nil {
		t.Fatalf("baseline sync: %v", err)
	}

	invalid := validSnapshot("snapshot-2", now.Add(2*time.Minute), 18, "chatgpt-plus")
	invalid.Products = append(invalid.Products, invalid.Products[0])
	fixture.setSnapshot(invalid)
	for i := 0; i < 3; i++ {
		now = now.Add(2 * time.Minute)
		if _, err := service.Sync(context.Background()); err == nil {
			t.Fatalf("invalid sync %d unexpectedly succeeded", i+1)
		}
	}
	messages := dispatcher.Messages()
	if len(messages) != 1 || messages[0].Event != storage.EventPriceAISyncFailed {
		t.Fatalf("failed imports should only emit threshold health signal: %#v", messages)
	}

	now = now.Add(2 * time.Minute)
	fixture.setSnapshot(validSnapshot("snapshot-2", now, 18, "chatgpt-plus"))
	if _, err := service.Sync(context.Background()); err != nil {
		t.Fatalf("recovery sync: %v", err)
	}
	messages = dispatcher.Messages()
	if len(messages) != 2 || messages[1].Event != storage.EventPriceAISyncRecovered {
		t.Fatalf("recovered feed health was not dispatched after commit: %#v", messages)
	}
}

type recordingDispatcher struct {
	mu       sync.Mutex
	messages []notify.Message
	err      error
}

func (d *recordingDispatcher) Dispatch(_ context.Context, message notify.Message) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.messages = append(d.messages, message)
	return d.err
}

func (d *recordingDispatcher) Messages() []notify.Message {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]notify.Message(nil), d.messages...)
}

func newTestService(t *testing.T, serverURL string, now time.Time) (*Service, *storage.PriceAI) {
	t.Helper()
	db, err := storage.Open(storage.DBConfig{
		Driver:       storage.DBDriverSQLite,
		Path:         filepath.Join(t.TempDir(), "priceai.db"),
		MaxOpenConns: 5,
		MaxIdleConns: 2,
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get test sql db: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	repo := storage.NewPriceAI(db)
	service := NewServiceWithClient(repo, newTestFeedClient(t, serverURL), nil)
	service.now = func() time.Time { return now }
	return service, repo
}

func newTestFeedClient(t *testing.T, serverURL string) *FeedClient {
	t.Helper()
	parsed, err := url.Parse(serverURL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	return newFeedClient(nil, feedEndpoints{
		discoveryURL: serverURL + "/.well-known/price-radar.json",
		pointerURL:   serverURL + "/latest.json",
		schemaURL:    serverURL + "/price-radar-v1.schema.json",
		webHost:      parsed.Host,
		dataHost:     parsed.Host,
		allowHTTP:    true,
	})
}

type feedFixture struct {
	t      *testing.T
	server *httptest.Server

	mu             sync.Mutex
	snapshot       Snapshot
	pointerCallsN  int
	snapshotCallsN int
	lastETag       string
	blockPointer   chan struct{}
	pointerHit     chan struct{}
}

func newFeedFixture(t *testing.T, snapshot Snapshot) *feedFixture {
	fixture := &feedFixture{t: t, snapshot: snapshot}
	fixture.server = httptest.NewServer(http.HandlerFunc(fixture.handle))
	return fixture
}

func (f *feedFixture) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.URL.Path {
	case "/.well-known/price-radar.json":
		writeJSON(w, DiscoveryDocument{
			SchemaVersion:          DiscoverySchemaVersion,
			LatestURL:              f.server.URL + "/latest.json",
			SchemaURL:              f.server.URL + "/price-radar-v1.schema.json",
			RefreshIntervalSeconds: 300,
		})
	case "/price-radar-v1.schema.json":
		writeJSON(w, map[string]string{"schema": "test"})
	case "/latest.json":
		f.pointerCallsN++
		f.lastETag = r.Header.Get("If-None-Match")
		if f.blockPointer != nil {
			select {
			case f.pointerHit <- struct{}{}:
			default:
			}
			block := f.blockPointer
			f.mu.Unlock()
			<-block
			f.mu.Lock()
		}
		etag := f.etagForLocked(f.snapshot.SnapshotID)
		w.Header().Set("ETag", etag)
		w.Header().Set("Last-Modified", f.snapshot.PublishedAt.Format(http.TimeFormat))
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		writeJSON(w, Pointer{
			SchemaVersion: FeedSchemaVersion,
			SnapshotID:    f.snapshot.SnapshotID,
			GeneratedAt:   f.snapshot.GeneratedAt,
			PublishedAt:   f.snapshot.PublishedAt,
			Stale:         f.snapshot.Stale,
			SnapshotURL:   f.server.URL + "/v1/snapshots/" + f.snapshot.SnapshotID + ".json",
			ProductCount:  len(f.snapshot.Products),
		})
	default:
		if r.URL.Path == "/v1/snapshots/"+f.snapshot.SnapshotID+".json" {
			f.snapshotCallsN++
			writeJSON(w, f.snapshot)
			return
		}
		http.NotFound(w, r)
	}
}

func (f *feedFixture) setSnapshot(snapshot Snapshot) {
	f.mu.Lock()
	f.snapshot = snapshot
	f.mu.Unlock()
}

func (f *feedFixture) pointerCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pointerCallsN
}

func (f *feedFixture) snapshotCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.snapshotCallsN
}

func (f *feedFixture) lastIfNoneMatch() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastETag
}

func (f *feedFixture) etagFor(snapshotID string) string { return `"` + snapshotID + `"` }

func (f *feedFixture) etagForLocked(snapshotID string) string { return f.etagFor(snapshotID) }

func (f *feedFixture) blockNextPointer() {
	f.mu.Lock()
	f.blockPointer = make(chan struct{})
	f.pointerHit = make(chan struct{}, 1)
	f.mu.Unlock()
}

func (f *feedFixture) waitForBlockedPointer(t *testing.T) {
	t.Helper()
	f.mu.Lock()
	hit := f.pointerHit
	f.mu.Unlock()
	select {
	case <-hit:
	case <-time.After(5 * time.Second):
		t.Fatal("pointer did not block")
	}
}

func (f *feedFixture) releaseBlockedPointer() {
	f.mu.Lock()
	block := f.blockPointer
	f.blockPointer = nil
	f.mu.Unlock()
	close(block)
}

func validSnapshot(snapshotID string, at time.Time, price float64, slug string) Snapshot {
	currency := "USD"
	offer := FeedOffer{
		ID:         "offer-" + snapshotID,
		SourceID:   stringPointer("source-1"),
		SourceName: "Merchant",
		Title:      "ChatGPT Plus",
		Price:      price,
		Currency:   currency,
		Status:     "in_stock",
		URL:        "https://merchant.example/item/" + snapshotID,
	}
	return Snapshot{
		SchemaVersion: FeedSchemaVersion,
		SnapshotID:    snapshotID,
		GeneratedAt:   at,
		PublishedAt:   at,
		Products: []FeedProduct{{
			ID:                  "product-1",
			Slug:                slug,
			Name:                "ChatGPT Plus",
			Platform:            "ChatGPT",
			ProductType:         "订阅/会员",
			OfferCount:          1,
			InStockCount:        1,
			LowestPrice:         &price,
			LowestOffer:         &offer,
			LatestSeenAt:        &at,
			SnapshotGeneratedAt: at,
			Total:               1,
			TopOffers:           []FeedOffer{offer},
		}},
	}
}

func stringPointer(value string) *string { return &value }

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
