package priceai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/ifty-r/upstream-ops/backend/storage"
)

func TestRefreshRiskMatchesOnlyCurrentSourceAndOfferIDsAndPreservesCacheOnFailure(t *testing.T) {
	now := time.Date(2026, time.July, 26, 11, 0, 0, 0, time.UTC)
	feed := newFeedFixture(t, validSnapshot("snapshot-1", now, 20, "chatgpt-plus"))
	defer feed.server.Close()

	var mu sync.Mutex
	pageStatus := http.StatusOK
	pageCalls := 0
	riskPage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		pageCalls++
		if pageStatus != http.StatusOK {
			w.WriteHeader(pageStatus)
			return
		}
		_, _ = w.Write([]byte(`<!doctype html><script>window.__DATA__={"riskFeedback":[
          {"scope":"source","sourceId":"source-1","status":"user_report_pending_verification","feedbackCount":2,"reasons":["重复扣费"],"summaries":["等待核验"],"latestAt":"2026-07-26T10:30:00Z"},
          {"scope":"offer","offerId":"offer-snapshot-1","status":"reported","feedbackCount":1},
          {"scope":"source","sourceId":"unmatched-source","status":"reported","feedbackCount":99}
        ]};</script>`))
	}))
	defer riskPage.Close()

	service, repo := newTestService(t, feed.server.URL, now)
	service.now = func() time.Time { return now }
	parsed, err := url.Parse(riskPage.URL)
	if err != nil {
		t.Fatalf("parse risk page URL: %v", err)
	}
	service.riskClient = newRiskClient(nil, riskEndpoints{
		productBaseURL: riskPage.URL,
		webHost:        parsed.Host,
		allowHTTP:      true,
	})
	if _, err := service.Sync(context.Background()); err != nil {
		t.Fatalf("feed sync: %v", err)
	}

	first, err := service.RefreshRisk(context.Background())
	if err != nil {
		t.Fatalf("first risk refresh: %v", err)
	}
	if first.ProductsAttempted != 1 || first.FeedbackUpdated != 2 || first.Failures != 0 {
		t.Fatalf("unexpected first risk result: %#v", first)
	}
	product, err := repo.FindProductBySlug("chatgpt-plus")
	if err != nil || product == nil {
		t.Fatalf("find product: %v, %#v", err, product)
	}
	feedback, err := repo.ListRiskFeedback(product.ID)
	if err != nil {
		t.Fatalf("list feedback: %v", err)
	}
	if len(feedback) != 2 || feedback[0].SubjectRemoteID != "offer-snapshot-1" || feedback[1].SubjectRemoteID != "source-1" {
		t.Fatalf("risk feedback was not exactly associated: %#v", feedback)
	}
	if feedback[1].Status != "user_report_pending_verification" || feedback[1].FetchedAt == nil || feedback[1].LastError != "" {
		t.Fatalf("source risk cache mismatch: %#v", feedback[1])
	}

	second, err := service.RefreshRisk(context.Background())
	if err != nil {
		t.Fatalf("fresh-cache refresh: %v", err)
	}
	if second.ProductsAttempted != 0 || second.ProductsSkipped != 1 {
		t.Fatalf("fresh cache should skip page fetch: %#v", second)
	}
	mu.Lock()
	if pageCalls != 1 {
		mu.Unlock()
		t.Fatalf("risk page calls=%d, want 1", pageCalls)
	}
	pageStatus = http.StatusServiceUnavailable
	mu.Unlock()

	now = now.Add(riskCacheTTL + time.Minute)
	failed, err := service.RefreshRisk(context.Background())
	if err == nil || failed.Failures != 1 {
		t.Fatalf("stale cache failure was not surfaced: result=%#v err=%v", failed, err)
	}
	feedback, err = repo.ListRiskFeedback(product.ID)
	if err != nil {
		t.Fatalf("list retained feedback: %v", err)
	}
	if len(feedback) != 2 || feedback[0].RawJSON == "" || feedback[0].LastError == "" {
		t.Fatalf("failed risk fetch should retain payload and record error: %#v", feedback)
	}
	logs, total, err := repo.ListSyncLogs(storage.PriceAISyncJobRisk, 1, 10)
	if err != nil || total != 3 || len(logs) != 3 || logs[0].Success {
		t.Fatalf("risk sync logs mismatch: total=%d logs=%#v err=%v", total, logs, err)
	}
}

func TestExtractRiskFeedbackSupportsScopedContainersAndRejectsMalformedPayload(t *testing.T) {
	records, found, err := extractRiskFeedback([]byte(`{"riskFeedback":{"sources":{"source-a":{"status":"reported","feedbackCount":3}},"offers":{"offer-a":{"status":"reported","reasons":["reason"]}}}}`))
	if err != nil || !found || len(records) != 2 {
		t.Fatalf("unexpected structured extraction: records=%#v found=%v err=%v", records, found, err)
	}
	seen := make(map[string]riskRecord, len(records))
	for _, record := range records {
		seen[string(record.Scope)+":"+record.SubjectRemoteID] = record
	}
	if _, ok := seen["source:source-a"]; !ok {
		t.Fatalf("source container extraction mismatch: %#v", records)
	}
	if _, ok := seen["offer:offer-a"]; !ok {
		t.Fatalf("offer container extraction mismatch: %#v", records)
	}
	if _, found, err := extractRiskFeedback([]byte(`{"riskFeedback":{bad-json}`)); err == nil || !found {
		t.Fatalf("malformed risk payload should be rejected: found=%v err=%v", found, err)
	}
	if records, found, err := extractRiskFeedback([]byte(`<html>no structured data</html>`)); err != nil || found || len(records) != 0 {
		t.Fatalf("missing risk field should be non-fatal: records=%#v found=%v err=%v", records, found, err)
	}
}
