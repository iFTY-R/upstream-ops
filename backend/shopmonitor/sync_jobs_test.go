package shopmonitor

import (
	"context"
	"testing"
	"time"

	"github.com/ifty-r/upstream-ops/backend/config"
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
	if batch.Source != storage.ShopSyncBatchSourceManual {
		t.Fatalf("batch source = %s, want manual", batch.Source)
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

func TestCancelBatchCancelsQueuedJobsAndConverges(t *testing.T) {
	db := openShopMonitorTestDB(t)
	jobs := storage.NewShopSyncJobs(db)
	first := &storage.ShopSyncJob{TargetID: 1, Status: storage.ShopSyncJobQueued}
	second := &storage.ShopSyncJob{TargetID: 2, Status: storage.ShopSyncJobQueued}
	for _, job := range []*storage.ShopSyncJob{first, second} {
		if err := jobs.Create(job); err != nil {
			t.Fatalf("create job: %v", err)
		}
	}
	runner := &SyncJobRunner{
		jobs:         jobs,
		controls:     make(map[uint]*syncJobControl),
		batchCancels: make(map[uint]context.CancelFunc),
	}
	batch, err := runner.CreateBatch(2, 2, 0, 0, []uint{first.ID, second.ID}, time.Now())
	if err != nil {
		t.Fatalf("create batch: %v", err)
	}
	cancelled, err := runner.CancelBatch(batch.ID)
	if err != nil {
		t.Fatalf("cancel batch: %v", err)
	}
	if cancelled.Status != storage.ShopSyncBatchCancelled || cancelled.CancelledCount != 2 || cancelled.CancelledAt == nil {
		t.Fatalf("cancelled batch = %#v", cancelled)
	}
	stored, err := jobs.FindByIDs([]uint{first.ID, second.ID})
	if err != nil {
		t.Fatalf("find jobs: %v", err)
	}
	for _, job := range stored {
		if job.Status != storage.ShopSyncJobCancelled {
			t.Fatalf("job %d status = %q", job.ID, job.Status)
		}
	}
}

func TestCancelBatchPreventsSlotBlockedJobFromCallingProvider(t *testing.T) {
	platform := storage.ShopPlatform("cancel-slot-blocked-test")
	provider := &countingShopProvider{}
	shopprovider.Register(platform, func() shopprovider.Provider { return provider })

	db := openShopMonitorTestDB(t)
	targets := storage.NewShopTargets(db)
	target := createRefreshTarget(t, targets, platform)
	monitor := NewService(targets, storage.NewShopWatchRules(db), storage.NewShopGoods(db), nil, nil, config.ProxyConfig{}, config.UpstreamConfig{})
	runner := NewSyncJobRunner(monitor, storage.NewShopSyncJobs(db), nil)
	for i := 0; i < cap(runner.slots); i++ {
		runner.slots <- struct{}{}
	}
	defer func() {
		for i := 0; i < cap(runner.slots); i++ {
			<-runner.slots
		}
	}()

	job, reused, err := runner.Start(target.ID)
	if err != nil || reused {
		t.Fatalf("start job: reused=%v err=%v", reused, err)
	}
	batch, err := runner.CreateBatch(1, 1, 0, 0, []uint{job.ID}, time.Now())
	if err != nil {
		t.Fatalf("create batch: %v", err)
	}
	if _, err := runner.CancelBatch(batch.ID); err != nil {
		t.Fatalf("cancel batch: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		stored, findErr := runner.Get(target.ID, job.ID)
		if findErr == nil && stored.Status == storage.ShopSyncJobCancelled {
			infoCalls, goodsCalls := provider.counts()
			if infoCalls != 0 || goodsCalls != 0 {
				t.Fatalf("provider calls after queued cancellation: info=%d goods=%d", infoCalls, goodsCalls)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("slot-blocked job did not become cancelled")
}

func TestQueuedJobDoesNotInheritExecutionTimeout(t *testing.T) {
	platform := storage.ShopPlatform("queued-job-timeout-test")
	shopprovider.Register(platform, func() shopprovider.Provider { return fakeShopProvider{} })

	db := openShopMonitorTestDB(t)
	targets := storage.NewShopTargets(db)
	target := createRefreshTarget(t, targets, platform)
	monitor := NewService(targets, storage.NewShopWatchRules(db), storage.NewShopGoods(db), nil, nil, config.ProxyConfig{}, config.UpstreamConfig{})
	runner := NewSyncJobRunner(monitor, storage.NewShopSyncJobs(db), nil)
	for i := 0; i < cap(runner.slots); i++ {
		runner.slots <- struct{}{}
	}
	defer func() {
		for i := 0; i < cap(runner.slots); i++ {
			<-runner.slots
		}
	}()

	job, reused, err := runner.Start(target.ID)
	if err != nil || reused {
		t.Fatalf("start job: reused=%v err=%v", reused, err)
	}

	runner.mu.Lock()
	control := runner.controls[job.ID]
	runner.mu.Unlock()
	if control == nil {
		t.Fatal("queued job control was not registered")
	}
	if deadline, ok := control.ctx.Deadline(); ok {
		t.Fatalf("queued job has execution deadline %v before acquiring a slot", deadline)
	}

	batch, err := runner.CreateBatch(1, 1, 0, 0, []uint{job.ID}, time.Now())
	if err != nil {
		t.Fatalf("create batch: %v", err)
	}
	if _, err := runner.CancelBatch(batch.ID); err != nil {
		t.Fatalf("cancel batch: %v", err)
	}
}

func TestCancelJobPreventsSlotBlockedJobFromCallingProvider(t *testing.T) {
	platform := storage.ShopPlatform("cancel-job-slot-blocked-test")
	provider := &countingShopProvider{}
	shopprovider.Register(platform, func() shopprovider.Provider { return provider })

	db := openShopMonitorTestDB(t)
	targets := storage.NewShopTargets(db)
	target := createRefreshTarget(t, targets, platform)
	monitor := NewService(targets, storage.NewShopWatchRules(db), storage.NewShopGoods(db), nil, nil, config.ProxyConfig{}, config.UpstreamConfig{})
	runner := NewSyncJobRunner(monitor, storage.NewShopSyncJobs(db), nil)
	for i := 0; i < cap(runner.slots); i++ {
		runner.slots <- struct{}{}
	}
	defer func() {
		for i := 0; i < cap(runner.slots); i++ {
			<-runner.slots
		}
	}()

	job, reused, err := runner.Start(target.ID)
	if err != nil || reused {
		t.Fatalf("start job: reused=%v err=%v", reused, err)
	}
	cancelled, err := runner.Cancel(target.ID, job.ID)
	if err != nil {
		t.Fatalf("cancel job: %v", err)
	}
	if cancelled.Status != storage.ShopSyncJobCancelled {
		t.Fatalf("cancelled status = %q, want %q", cancelled.Status, storage.ShopSyncJobCancelled)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		runner.mu.Lock()
		_, active := runner.controls[job.ID]
		runner.mu.Unlock()
		if !active {
			infoCalls, goodsCalls := provider.counts()
			if infoCalls != 0 || goodsCalls != 0 {
				t.Fatalf("provider calls after job cancellation: info=%d goods=%d", infoCalls, goodsCalls)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("slot-blocked job was not cleaned up after cancellation")
}

func TestScheduledSyncCreatesReadableCronBatch(t *testing.T) {
	platform := storage.ShopPlatform("scheduled-sync-batch-test")
	shopprovider.Register(platform, func() shopprovider.Provider {
		return fakeShopProvider{goods: []shopprovider.Goods{{GoodsKey: "cron-item", Name: "Cron Item", StockCount: 3}}}
	})

	db := openShopMonitorTestDB(t)
	targets := storage.NewShopTargets(db)
	target := createRefreshTarget(t, targets, platform)
	monitor := NewService(targets, storage.NewShopWatchRules(db), storage.NewShopGoods(db), nil, nil, config.ProxyConfig{}, config.UpstreamConfig{})
	runner := NewSyncJobRunner(monitor, storage.NewShopSyncJobs(db), nil)

	result := runner.SyncAllScheduled(context.Background(), 1, 15*time.Minute)
	if result.Total != 1 || result.Success != 1 || result.Failed != 0 {
		t.Fatalf("scheduled sync result = %#v", result)
	}
	batch, err := runner.LatestBatch()
	if err != nil {
		t.Fatalf("latest scheduled batch: %v", err)
	}
	if batch.Source != storage.ShopSyncBatchSourceCron || batch.Status != storage.ShopSyncBatchSucceeded {
		t.Fatalf("scheduled batch = %#v", batch)
	}
	details, err := runner.BatchDetails(batch.ID)
	if err != nil {
		t.Fatalf("scheduled batch details: %v", err)
	}
	if len(details.Items) != 1 || details.Items[0].TargetID != target.ID || details.Items[0].Job == nil {
		t.Fatalf("scheduled batch details = %#v", details)
	}
	if details.Items[0].Job.Status != storage.ShopSyncJobSucceeded {
		t.Fatalf("scheduled job = %#v", details.Items[0].Job)
	}
}

func TestManualSyncCompletionPersistsCooldownTimestamp(t *testing.T) {
	platform := storage.ShopPlatform("manual-sync-cooldown-test")
	shopprovider.Register(platform, func() shopprovider.Provider { return fakeShopProvider{} })

	db := openShopMonitorTestDB(t)
	targets := storage.NewShopTargets(db)
	target := createRefreshTarget(t, targets, platform)
	monitor := NewService(targets, storage.NewShopWatchRules(db), storage.NewShopGoods(db), nil, nil, config.ProxyConfig{}, config.UpstreamConfig{})
	runner := NewSyncJobRunner(monitor, storage.NewShopSyncJobs(db), nil)

	job, reused, err := runner.Start(target.ID)
	if err != nil || reused {
		t.Fatalf("start manual sync: reused=%v err=%v", reused, err)
	}
	waitForSyncJobTerminal(t, runner, target.ID, job.ID)
	waitForManualSyncTimestamp(t, targets, target.ID)
}

func TestScheduledSyncSkipsOnlyTargetsInManualCooldown(t *testing.T) {
	platform := storage.ShopPlatform("scheduled-manual-cooldown-test")
	provider := &countingShopProvider{}
	shopprovider.Register(platform, func() shopprovider.Provider { return provider })

	db := openShopMonitorTestDB(t)
	targets := storage.NewShopTargets(db)
	recent := createCooldownTarget(t, targets, platform, "recent", time.Now().Add(-time.Minute))
	ready := createCooldownTarget(t, targets, platform, "ready", time.Now().Add(-16*time.Minute))
	monitor := NewService(targets, storage.NewShopWatchRules(db), storage.NewShopGoods(db), nil, nil, config.ProxyConfig{}, config.UpstreamConfig{})
	runner := NewSyncJobRunner(monitor, storage.NewShopSyncJobs(db), nil)

	result := runner.SyncAllScheduled(context.Background(), 1, 15*time.Minute)
	if result.Total != 2 || result.Success != 1 || result.Skipped != 1 || result.Failed != 0 {
		t.Fatalf("scheduled cooldown result = %#v", result)
	}
	infoCalls, goodsCalls := provider.counts()
	if infoCalls != 1 || goodsCalls != 1 {
		t.Fatalf("provider calls: info=%d goods=%d", infoCalls, goodsCalls)
	}
	batch, err := runner.LatestBatch()
	if err != nil {
		t.Fatalf("latest batch: %v", err)
	}
	details, err := runner.BatchDetails(batch.ID)
	if err != nil {
		t.Fatalf("batch details: %v", err)
	}
	if len(details.Items) != 1 || details.Items[0].TargetID != ready.ID || details.Items[0].TargetID == recent.ID {
		t.Fatalf("scheduled batch items = %#v", details.Items)
	}
}

func TestManualCooldownZeroKeepsAllScheduledTargets(t *testing.T) {
	now := time.Now()
	list := []storage.ShopTarget{{ID: 1, LastManualSyncAt: &now}}
	ready, skipped := filterManualCooldownTargets(list, 0, now)
	if len(ready) != 1 || len(skipped) != 0 {
		t.Fatalf("disabled cooldown: ready=%d skipped=%d", len(ready), len(skipped))
	}
}

func createCooldownTarget(t *testing.T, targets *storage.ShopTargets, platform storage.ShopPlatform, token string, lastManual time.Time) *storage.ShopTarget {
	t.Helper()
	target := &storage.ShopTarget{
		Name:             token,
		Platform:         platform,
		SiteURL:          "https://example.invalid/shop/" + token,
		BaseURL:          "https://example.invalid",
		Token:            token,
		MonitorEnabled:   true,
		ScopeMode:        storage.ShopScopeAll,
		GoodsTypesJSON:   `["card"]`,
		LastManualSyncAt: &lastManual,
	}
	if err := targets.Create(target); err != nil {
		t.Fatalf("create cooldown target: %v", err)
	}
	return target
}

func waitForSyncJobTerminal(t *testing.T, runner *SyncJobRunner, targetID, jobID uint) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		job, err := runner.Get(targetID, jobID)
		if err == nil && !isActiveSyncJobStatus(job.Status) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("sync job did not reach a terminal state")
}

func waitForManualSyncTimestamp(t *testing.T, targets *storage.ShopTargets, targetID uint) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		target, err := targets.FindByID(targetID)
		if err == nil && target.LastManualSyncAt != nil {
			if time.Since(*target.LastManualSyncAt) > time.Second {
				t.Fatalf("last manual sync at = %v", target.LastManualSyncAt)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("manual sync completion timestamp was not persisted")
}

func TestScheduledShopSyncTimeoutScalesWithBatchSize(t *testing.T) {
	if got := scheduledShopSyncTimeout(1, 4); got != scheduledShopSyncMinTimeout {
		t.Fatalf("small batch timeout = %v, want %v", got, scheduledShopSyncMinTimeout)
	}
	got := scheduledShopSyncTimeout(65, 4)
	if got <= 5*time.Minute {
		t.Fatalf("large batch timeout = %v, want more than the old fixed 5m ceiling", got)
	}
	if got != 35*time.Minute {
		t.Fatalf("large batch timeout = %v, want 35m", got)
	}
	if got := scheduledShopSyncTimeout(10000, 1); got != scheduledShopSyncMaxTimeout {
		t.Fatalf("huge batch timeout = %v, want %v", got, scheduledShopSyncMaxTimeout)
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
