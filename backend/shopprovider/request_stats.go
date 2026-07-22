package shopprovider

import (
	"context"
	"sync/atomic"
	"time"
)

type requestStatsContextKey struct{}

// RequestStats holds lightweight per-sync HTTP metrics. It stays in memory
// while a job is running and is persisted once when the job completes.
type RequestStats struct {
	count         atomic.Int64
	durationNanos atomic.Int64
}

type RequestStatsSnapshot struct {
	Count      int
	DurationMS int64
}

func WithRequestStats(ctx context.Context) (context.Context, *RequestStats) {
	stats := &RequestStats{}
	return context.WithValue(ctx, requestStatsContextKey{}, stats), stats
}

func ObserveRequest(ctx context.Context, duration time.Duration) {
	if ctx == nil {
		return
	}
	stats, _ := ctx.Value(requestStatsContextKey{}).(*RequestStats)
	if stats == nil {
		return
	}
	if duration < 0 {
		duration = 0
	}
	stats.count.Add(1)
	stats.durationNanos.Add(duration.Nanoseconds())
}

func (s *RequestStats) Snapshot() RequestStatsSnapshot {
	if s == nil {
		return RequestStatsSnapshot{}
	}
	return RequestStatsSnapshot{
		Count:      int(s.count.Load()),
		DurationMS: time.Duration(s.durationNanos.Load()).Milliseconds(),
	}
}
