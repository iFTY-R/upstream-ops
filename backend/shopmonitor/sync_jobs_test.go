package shopmonitor

import (
	"context"
	"testing"
	"time"

	"github.com/ifty-r/upstream-ops/backend/shopprovider"
	"github.com/ifty-r/upstream-ops/backend/storage"
)

func TestSyncBatchDurationCoversAllJobs(t *testing.T) {
	db := openShopMonitorTestDB(t)
	jobs := storage.NewShopSyncJobs(db)
	first := &storage.ShopSyncJob{TargetID: 1, Status: storage.ShopSyncJobQueued}
	second := &storage.ShopSyncJob{TargetID: 2, Status: storage.ShopSyncJobQueued}
	for _, job := range []*storage.ShopSyncJob{first, second} {
		if err := jobs.Create(job); err != nil {
			t.Fatalf("create job: %v", err)
		}
	}

	runner := &SyncJobRunner{jobs: jobs}
	batchStartedAt := time.Now().Add(-10 * time.Second).Round(time.Millisecond)
	batch, err := runner.CreateBatch(2, 2, 0, 0, []uint{first.ID, second.ID}, batchStartedAt)
	if err != nil {
		t.Fatalf("create batch: %v", err)
	}
	if batch.Status != storage.ShopSyncBatchRunning {
		t.Fatalf("initial batch status = %s", batch.Status)
	}

	firstStartedAt := batchStartedAt.Add(time.Second)
	firstFinishedAt := batchStartedAt.Add(3 * time.Second)
	if err := jobs.Complete(first.ID, storage.ShopSyncJobSucceeded, 2, 0, nil, "", firstStartedAt, firstFinishedAt); err != nil {
		t.Fatalf("complete first job: %v", err)
	}
	secondStartedAt := batchStartedAt.Add(2 * time.Second)
	secondFinishedAt := batchStartedAt.Add(7 * time.Second)
	if err := jobs.Complete(second.ID, storage.ShopSyncJobSucceeded, 3, 1, nil, "", secondStartedAt, secondFinishedAt); err != nil {
		t.Fatalf("complete second job: %v", err)
	}

	latest, err := runner.LatestBatch()
	if err != nil {
		t.Fatalf("latest batch: %v", err)
	}
	if latest.Status != storage.ShopSyncBatchSucceeded || latest.SucceededCount != 2 {
		t.Fatalf("completed batch = %#v", latest)
	}
	if latest.FinishedAt == nil || !latest.FinishedAt.Equal(secondFinishedAt) {
		t.Fatalf("batch finished_at = %v, want %v", latest.FinishedAt, secondFinishedAt)
	}
	if latest.DurationMS != 7000 {
		t.Fatalf("batch duration_ms = %d, want 7000", latest.DurationMS)
	}
}

func TestSyncBatchDetailsIncludeLiveRequestStats(t *testing.T) {
	db := openShopMonitorTestDB(t)
	jobs := storage.NewShopSyncJobs(db)
	job := &storage.ShopSyncJob{TargetID: 7, Status: storage.ShopSyncJobRunning}
	if err := jobs.Create(job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	runner := &SyncJobRunner{jobs: jobs}
	batchStartedAt := time.Now().Add(-time.Second).Round(time.Millisecond)
	batch, err := runner.CreateBatchWithItems(1, 1, 0, 0, []storage.ShopSyncBatchItem{{
		TargetID:   7,
		TargetName: "实时店铺",
		JobID:      job.ID,
	}}, batchStartedAt)
	if err != nil {
		t.Fatalf("create batch: %v", err)
	}

	ctx, stats := shopprovider.WithRequestStats(context.Background())
	shopprovider.ObserveRequest(ctx, 25*time.Millisecond)
	shopprovider.ObserveRequest(ctx, 35*time.Millisecond)
	runner.requestStats.Store(job.ID, stats)

	details, err := runner.BatchDetails(batch.ID)
	if err != nil {
		t.Fatalf("batch details: %v", err)
	}
	if len(details.Items) != 1 || details.Items[0].TargetName != "实时店铺" || details.Items[0].Job == nil {
		t.Fatalf("details = %#v", details)
	}
	if details.Items[0].Job.RequestCount != 2 || details.Items[0].Job.RequestDurationMS != 60 {
		t.Fatalf("live job metrics = %#v", details.Items[0].Job)
	}
}
