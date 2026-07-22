package shopprovider

import (
	"context"
	"testing"
	"time"
)

func TestRequestStatsSnapshot(t *testing.T) {
	ctx, stats := WithRequestStats(context.Background())
	ObserveRequest(ctx, 20*time.Millisecond)
	ObserveRequest(ctx, 40*time.Millisecond)

	snapshot := stats.Snapshot()
	if snapshot.Count != 2 {
		t.Fatalf("request count = %d, want 2", snapshot.Count)
	}
	if snapshot.DurationMS != 60 {
		t.Fatalf("request duration = %dms, want 60ms", snapshot.DurationMS)
	}
}
