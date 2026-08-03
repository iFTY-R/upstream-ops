package shopmonitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/ifty-r/upstream-ops/backend/shopprovider"
	"github.com/ifty-r/upstream-ops/backend/storage"
)

const (
	shopSyncJobConcurrency         = 2
	scheduledShopSyncMinTimeout    = 15 * time.Minute
	scheduledShopSyncMaxTimeout    = 2 * time.Hour
	scheduledShopSyncTimeoutBuffer = time.Minute
)

// SyncJobRunner runs manual shop synchronizations after the HTTP request has
// completed. It keeps one active job per target while durable job records make
// progress and failures queryable from the UI.
type SyncJobRunner struct {
	monitor *Service
	jobs    *storage.ShopSyncJobs
	log     *slog.Logger

	mu           sync.Mutex
	active       map[uint]uint
	controls     map[uint]*syncJobControl
	batchCancels map[uint]context.CancelFunc
	slots        chan struct{}

	requestStats sync.Map
}

type syncJobControl struct {
	ctx    context.Context
	cancel context.CancelFunc
}

func NewSyncJobRunner(monitor *Service, jobs *storage.ShopSyncJobs, log *slog.Logger) *SyncJobRunner {
	runner := &SyncJobRunner{
		monitor:      monitor,
		jobs:         jobs,
		log:          log,
		active:       make(map[uint]uint),
		controls:     make(map[uint]*syncJobControl),
		batchCancels: make(map[uint]context.CancelFunc),
		slots:        make(chan struct{}, shopSyncJobConcurrency),
	}
	if jobs != nil {
		if err := jobs.MarkInterrupted(); err != nil && log != nil {
			log.Warn("mark interrupted shop sync jobs failed", "err", err)
		}
	}
	return runner
}

// Start creates a background job or reuses the current job for the target.
func (r *SyncJobRunner) Start(targetID uint) (*storage.ShopSyncJob, bool, error) {
	if r == nil || r.monitor == nil || r.jobs == nil {
		return nil, false, fmt.Errorf("shop sync job runner is unavailable")
	}
	if targetID == 0 {
		return nil, false, fmt.Errorf("shop target id is required")
	}
	if _, err := r.monitor.targets.FindByID(targetID); err != nil {
		return nil, false, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if activeID, ok := r.active[targetID]; ok {
		job, err := r.jobs.FindByTargetAndID(targetID, activeID)
		if err == nil {
			return job, true, nil
		}
		delete(r.active, targetID)
	}
	if job, err := r.jobs.FindActiveByTarget(targetID); err != nil {
		return nil, false, err
	} else if job != nil {
		r.active[targetID] = job.ID
		return job, true, nil
	}

	job := &storage.ShopSyncJob{TargetID: targetID, Status: storage.ShopSyncJobQueued}
	if err := r.jobs.Create(job); err != nil {
		return nil, false, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	control := &syncJobControl{ctx: ctx, cancel: cancel}
	r.active[targetID] = job.ID
	r.controls[job.ID] = control
	go r.run(job.ID, targetID, control)
	return job, false, nil
}

func (r *SyncJobRunner) Get(targetID, jobID uint) (*storage.ShopSyncJob, error) {
	if r == nil || r.jobs == nil {
		return nil, fmt.Errorf("shop sync job runner is unavailable")
	}
	return r.jobs.FindByTargetAndID(targetID, jobID)
}

// Cancel stops one manual shop sync job. The durable status changes before the
// goroutine exits so the UI can stop polling immediately.
func (r *SyncJobRunner) Cancel(targetID, jobID uint) (*storage.ShopSyncJob, error) {
	if r == nil || r.jobs == nil {
		return nil, fmt.Errorf("shop sync job runner is unavailable")
	}
	job, err := r.jobs.FindByTargetAndID(targetID, jobID)
	if err != nil || !isActiveSyncJobStatus(job.Status) {
		return job, err
	}

	r.mu.Lock()
	if control := r.controls[jobID]; control != nil {
		control.cancel()
	}
	r.mu.Unlock()

	if err := r.jobs.Cancel([]uint{jobID}, time.Now()); err != nil {
		return nil, err
	}
	return r.jobs.FindByTargetAndID(targetID, jobID)
}

func (r *SyncJobRunner) GetMany(jobIDs []uint) ([]storage.ShopSyncJob, error) {
	if r == nil || r.jobs == nil {
		return nil, fmt.Errorf("shop sync job runner is unavailable")
	}
	return r.jobs.FindByIDs(jobIDs)
}

func (r *SyncJobRunner) Latest(targetID uint) (*storage.ShopSyncJob, error) {
	if r == nil || r.jobs == nil {
		return nil, fmt.Errorf("shop sync job runner is unavailable")
	}
	return r.jobs.FindLatestByTarget(targetID)
}

// SyncAllScheduled records a cron-triggered sync with the same durable batch
// details as a manual sync while retaining the scheduler's synchronous flow,
// concurrency, and timeout context. Recording failures do not block the sync.
func (r *SyncJobRunner) SyncAllScheduled(ctx context.Context, concurrency int, manualCooldown time.Duration) *SyncAllResult {
	if r == nil || r.monitor == nil {
		return &SyncAllResult{Failed: 1, Targets: []SyncAllTargetResult{{Error: "shop sync job runner is unavailable"}}}
	}
	list, err := r.monitor.targets.ListMonitorEnabled()
	if err != nil {
		return r.monitor.syncAllListError(err)
	}
	list, cooldownSkipped := filterManualCooldownTargets(list, manualCooldown, time.Now())
	if len(cooldownSkipped) > 0 && r.log != nil {
		r.log.Info("scheduled shop sync skipped recent manual targets", "count", len(cooldownSkipped), "cooldown", manualCooldown)
	}
	if len(list) == 0 {
		return scheduledCooldownOnlyResult(cooldownSkipped)
	}
	timeoutCtx, cancelTimeout := context.WithTimeout(ctx, scheduledShopSyncTimeout(len(list), concurrency))
	defer cancelTimeout()

	if r.jobs == nil {
		result := r.monitor.syncTargetsWithConcurrency(timeoutCtx, list, concurrency, nil)
		appendScheduledCooldownSkips(result, cooldownSkipped)
		return result
	}

	startedAt := time.Now()
	batch := &storage.ShopSyncBatch{
		Status:      storage.ShopSyncBatchRunning,
		Source:      storage.ShopSyncBatchSourceCron,
		TotalCount:  len(list),
		QueuedCount: len(list),
		StartedAt:   startedAt,
	}
	items := make([]storage.ShopSyncBatchItem, len(list))
	for i := range list {
		items[i] = storage.ShopSyncBatchItem{TargetID: list[i].ID, TargetName: list[i].Name}
	}
	jobs, err := r.jobs.CreateBatchWithQueuedJobs(batch, items)
	if err != nil {
		if r.log != nil {
			r.log.Warn("create scheduled shop sync batch failed", "err", err)
		}
		return r.monitor.syncTargetsWithConcurrency(timeoutCtx, list, concurrency, nil)
	}
	runCtx, cancel := context.WithCancel(timeoutCtx)
	r.mu.Lock()
	r.batchCancels[batch.ID] = cancel
	r.mu.Unlock()
	defer func() {
		cancel()
		r.mu.Lock()
		delete(r.batchCancels, batch.ID)
		r.mu.Unlock()
	}()

	jobStartedAt := make([]time.Time, len(jobs))
	stats := make([]*shopprovider.RequestStats, len(jobs))
	hooks := &syncAllHooks{
		beforeTarget: func(_ context.Context, index int, _ storage.ShopTarget) context.Context {
			jobStartedAt[index] = time.Now()
			marked, err := r.jobs.TryMarkRunning(jobs[index].ID, jobStartedAt[index])
			if err != nil && r.log != nil {
				r.log.Warn("mark scheduled shop sync running failed", "job_id", jobs[index].ID, "err", err)
			}
			if !marked {
				// The job may have been cancelled while it waited for a worker
				// or for a same-origin queue. Prevent Sync from calling upstream.
				cancelledCtx, cancel := context.WithCancel(runCtx)
				cancel()
				return cancelledCtx
			}
			jobCtx, cancelJob := context.WithCancel(runCtx)
			r.mu.Lock()
			r.controls[jobs[index].ID] = &syncJobControl{ctx: jobCtx, cancel: cancelJob}
			r.mu.Unlock()
			observedCtx, requestStats := shopprovider.WithRequestStats(jobCtx)
			stats[index] = requestStats
			r.requestStats.Store(jobs[index].ID, requestStats)
			return observedCtx
		},
		afterTarget: func(index int, target storage.ShopTarget, result *SyncResult, syncErr error, skipped bool) {
			finishedAt := time.Now()
			status := storage.ShopSyncJobSucceeded
			errorMessage := ""
			if syncErr != nil {
				errorMessage = syncErr.Error()
				if skipped || isSkippedSyncError(syncErr) {
					status = storage.ShopSyncJobSkipped
				} else if errors.Is(runCtx.Err(), context.Canceled) || errors.Is(syncErr, context.Canceled) {
					status = storage.ShopSyncJobCancelled
					errorMessage = "同步已停止"
				} else if errors.Is(syncErr, context.DeadlineExceeded) || errors.Is(runCtx.Err(), context.DeadlineExceeded) {
					status = storage.ShopSyncJobTimedOut
					errorMessage = syncTimeoutMessageFor(runCtx)
				} else {
					status = storage.ShopSyncJobFailed
				}
			}
			goodsCount, changedCount := 0, 0
			events := map[string]int{}
			if result != nil {
				goodsCount = result.GoodsCount
				changedCount = result.ChangedCount
				events = result.Events
			}
			metrics := requestStatsSnapshot(stats[index])
			if err := r.jobs.CompleteWithMetrics(jobs[index].ID, status, goodsCount, changedCount, events, errorMessage, jobStartedAt[index], finishedAt, metrics.Count, metrics.DurationMS); err != nil && r.log != nil {
				r.log.Warn("complete scheduled shop sync job failed", "job_id", jobs[index].ID, "target_id", target.ID, "err", err)
			}
			r.requestStats.Delete(jobs[index].ID)
			r.mu.Lock()
			if control := r.controls[jobs[index].ID]; control != nil {
				control.cancel()
				delete(r.controls, jobs[index].ID)
			}
			if r.active[target.ID] == jobs[index].ID {
				delete(r.active, target.ID)
			}
			r.mu.Unlock()
		},
	}

	result := r.monitor.syncTargetsWithConcurrency(runCtx, list, concurrency, hooks)
	appendScheduledCooldownSkips(result, cooldownSkipped)
	if _, err := r.refreshBatch(batch); err != nil && r.log != nil {
		r.log.Warn("complete scheduled shop sync batch failed", "batch_id", batch.ID, "err", err)
	}
	return result
}

// filterManualCooldownTargets applies cooldown per shop. Manual execution does
// not call this path and therefore remains available during the cooldown.
func filterManualCooldownTargets(list []storage.ShopTarget, cooldown time.Duration, now time.Time) ([]storage.ShopTarget, []storage.ShopTarget) {
	if cooldown <= 0 {
		return list, nil
	}
	ready := make([]storage.ShopTarget, 0, len(list))
	skipped := make([]storage.ShopTarget, 0)
	for i := range list {
		lastManual := list[i].LastManualSyncAt
		if lastManual != nil && now.Sub(*lastManual) < cooldown {
			skipped = append(skipped, list[i])
			continue
		}
		ready = append(ready, list[i])
	}
	return ready, skipped
}

func scheduledCooldownOnlyResult(skipped []storage.ShopTarget) *SyncAllResult {
	result := &SyncAllResult{}
	appendScheduledCooldownSkips(result, skipped)
	return result
}

func appendScheduledCooldownSkips(result *SyncAllResult, skipped []storage.ShopTarget) {
	if result == nil || len(skipped) == 0 {
		return
	}
	result.Total += len(skipped)
	result.Skipped += len(skipped)
	for i := range skipped {
		result.Targets = append(result.Targets, SyncAllTargetResult{
			TargetID: skipped[i].ID,
			Name:     skipped[i].Name,
			Skipped:  true,
		})
	}
}

// scheduledShopSyncTimeout keeps cron-triggered shop batches from inheriting a
// fixed five-minute ceiling. The estimate scales with configured concurrency
// while keeping a practical floor and ceiling for abnormal upstream behavior.
func scheduledShopSyncTimeout(total, concurrency int) time.Duration {
	if total <= 0 {
		return shopSyncDefaultTimeout
	}
	if concurrency <= 0 {
		concurrency = 1
	}
	waves := (total + concurrency - 1) / concurrency
	timeout := time.Duration(waves)*shopSyncDefaultTimeout + scheduledShopSyncTimeoutBuffer
	if timeout < scheduledShopSyncMinTimeout {
		return scheduledShopSyncMinTimeout
	}
	if timeout > scheduledShopSyncMaxTimeout {
		return scheduledShopSyncMaxTimeout
	}
	return timeout
}

func (r *SyncJobRunner) CreateBatch(total, queued, reused, startFailed int, jobIDs []uint, startedAt time.Time) (*storage.ShopSyncBatch, error) {
	return r.createBatch(storage.ShopSyncBatchSourceManual, total, queued, reused, startFailed, jobIDs, nil, startedAt)
}

func (r *SyncJobRunner) CreateBatchWithItems(total, queued, reused, startFailed int, items []storage.ShopSyncBatchItem, startedAt time.Time) (*storage.ShopSyncBatch, error) {
	jobIDs := make([]uint, 0, len(items))
	for i := range items {
		if items[i].JobID != 0 {
			jobIDs = append(jobIDs, items[i].JobID)
		}
	}
	return r.createBatch(storage.ShopSyncBatchSourceManual, total, queued, reused, startFailed, jobIDs, items, startedAt)
}

func (r *SyncJobRunner) createBatch(source storage.ShopSyncBatchSource, total, queued, reused, startFailed int, jobIDs []uint, items []storage.ShopSyncBatchItem, startedAt time.Time) (*storage.ShopSyncBatch, error) {
	if r == nil || r.jobs == nil {
		return nil, fmt.Errorf("shop sync job runner is unavailable")
	}
	jobIDs = uniqueSyncJobIDs(jobIDs)
	encoded, err := json.Marshal(jobIDs)
	if err != nil {
		return nil, fmt.Errorf("encode shop sync batch jobs: %w", err)
	}
	batch := &storage.ShopSyncBatch{
		Status:           storage.ShopSyncBatchRunning,
		Source:           source,
		TotalCount:       total,
		QueuedCount:      queued,
		ReusedCount:      reused,
		StartFailedCount: startFailed,
		FailedCount:      startFailed,
		JobIDsJSON:       string(encoded),
		StartedAt:        startedAt,
	}
	if err := r.jobs.CreateBatchWithItems(batch, items); err != nil {
		return nil, err
	}
	batch, err = r.refreshBatch(batch)
	if err != nil {
		return nil, err
	}
	if batch.Status == storage.ShopSyncBatchRunning {
		go r.trackBatch(batch.ID)
	}
	return batch, nil
}

type SyncBatchItemDetail struct {
	storage.ShopSyncBatchItem
	Job *storage.ShopSyncJob `json:"job,omitempty"`
}

type SyncBatchDetails struct {
	Batch *storage.ShopSyncBatch `json:"batch"`
	Items []SyncBatchItemDetail  `json:"items"`
}

func (r *SyncJobRunner) BatchDetails(batchID uint) (*SyncBatchDetails, error) {
	if r == nil || r.jobs == nil {
		return nil, fmt.Errorf("shop sync job runner is unavailable")
	}
	batch, err := r.jobs.FindBatchByID(batchID)
	if err != nil {
		return nil, err
	}
	batch, err = r.refreshBatch(batch)
	if err != nil {
		return nil, err
	}
	items, err := r.jobs.FindBatchItems(batch.ID)
	if err != nil {
		return nil, err
	}
	jobIDs := make([]uint, 0, len(items))
	for i := range items {
		if items[i].JobID != 0 {
			jobIDs = append(jobIDs, items[i].JobID)
		}
	}
	jobs, err := r.jobs.FindByIDs(uniqueSyncJobIDs(jobIDs))
	if err != nil {
		return nil, err
	}
	jobsByID := make(map[uint]*storage.ShopSyncJob, len(jobs))
	for i := range jobs {
		if raw, ok := r.requestStats.Load(jobs[i].ID); ok {
			if stats, ok := raw.(*shopprovider.RequestStats); ok {
				snapshot := stats.Snapshot()
				jobs[i].RequestCount = snapshot.Count
				jobs[i].RequestDurationMS = snapshot.DurationMS
			}
		}
		jobsByID[jobs[i].ID] = &jobs[i]
	}
	details := &SyncBatchDetails{Batch: batch, Items: make([]SyncBatchItemDetail, 0, len(items))}
	for i := range items {
		details.Items = append(details.Items, SyncBatchItemDetail{
			ShopSyncBatchItem: items[i],
			Job:               jobsByID[items[i].JobID],
		})
	}
	return details, nil
}

func (r *SyncJobRunner) LatestBatch() (*storage.ShopSyncBatch, error) {
	if r == nil || r.jobs == nil {
		return nil, fmt.Errorf("shop sync job runner is unavailable")
	}
	batch, err := r.jobs.FindLatestBatch()
	if err != nil {
		return nil, err
	}
	return r.refreshBatch(batch)
}

func (r *SyncJobRunner) CancelBatch(batchID uint) (*storage.ShopSyncBatch, error) {
	if r == nil || r.jobs == nil {
		return nil, fmt.Errorf("shop sync job runner is unavailable")
	}
	requestedAt := time.Now()
	batch, err := r.jobs.RequestBatchCancel(batchID, requestedAt)
	if err != nil || !isActiveBatchStatus(batch.Status) {
		return batch, err
	}
	jobIDs, err := syncBatchJobIDs(batch)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	if cancel := r.batchCancels[batchID]; cancel != nil {
		cancel()
	}
	for _, jobID := range jobIDs {
		if control := r.controls[jobID]; control != nil {
			control.cancel()
		}
	}
	r.mu.Unlock()
	if err := r.jobs.Cancel(jobIDs, requestedAt); err != nil {
		return nil, err
	}
	return r.refreshBatch(batch)
}

func (r *SyncJobRunner) trackBatch(batchID uint) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		batch, err := r.jobs.FindBatchByID(batchID)
		if err != nil {
			return
		}
		batch, err = r.refreshBatch(batch)
		if err != nil {
			if r.log != nil {
				r.log.Warn("refresh shop sync batch failed", "batch_id", batchID, "err", err)
			}
		} else if !isActiveBatchStatus(batch.Status) {
			return
		}
		<-ticker.C
	}
}

func (r *SyncJobRunner) refreshBatch(batch *storage.ShopSyncBatch) (*storage.ShopSyncBatch, error) {
	if !isActiveBatchStatus(batch.Status) {
		return batch, nil
	}
	jobIDs, err := syncBatchJobIDs(batch)
	if err != nil {
		return nil, err
	}
	jobs, err := r.jobs.FindByIDs(jobIDs)
	if err != nil {
		return nil, err
	}

	succeeded, failed, skipped, cancelled := 0, batch.StartFailedCount, 0, 0
	active := false
	var finishedAt time.Time
	for i := range jobs {
		job := jobs[i]
		switch job.Status {
		case storage.ShopSyncJobQueued, storage.ShopSyncJobRunning:
			active = true
		case storage.ShopSyncJobSucceeded:
			succeeded++
		case storage.ShopSyncJobSkipped:
			skipped++
		case storage.ShopSyncJobCancelled:
			cancelled++
		default:
			failed++
		}
		if job.FinishedAt != nil && job.FinishedAt.After(finishedAt) {
			finishedAt = *job.FinishedAt
		}
	}
	missing := len(jobIDs) - len(jobs)
	if missing > 0 {
		failed += missing
	}

	result := *batch
	result.SucceededCount = succeeded
	result.FailedCount = failed
	result.SkippedCount = skipped
	result.CancelledCount = cancelled
	if active {
		result.DurationMS = max(time.Since(batch.StartedAt).Milliseconds(), 0)
		return &result, nil
	}
	if finishedAt.IsZero() || missing > 0 {
		finishedAt = time.Now()
	}
	if batch.Status == storage.ShopSyncBatchCancelling {
		result.Status = storage.ShopSyncBatchCancelled
		result.CancelledAt = &finishedAt
	} else {
		result.Status = completedBatchStatus(result.TotalCount, succeeded, failed, skipped)
	}
	result.FinishedAt = &finishedAt
	result.DurationMS = max(finishedAt.Sub(batch.StartedAt).Milliseconds(), 0)
	if err := r.jobs.CompleteBatch(&result); err != nil {
		return nil, err
	}
	return &result, nil
}

func syncBatchJobIDs(batch *storage.ShopSyncBatch) ([]uint, error) {
	var jobIDs []uint
	if batch.JobIDsJSON == "" {
		return jobIDs, nil
	}
	if err := json.Unmarshal([]byte(batch.JobIDsJSON), &jobIDs); err != nil {
		return nil, fmt.Errorf("decode shop sync batch %d jobs: %w", batch.ID, err)
	}
	return uniqueSyncJobIDs(jobIDs), nil
}

func isActiveBatchStatus(status storage.ShopSyncBatchStatus) bool {
	return status == storage.ShopSyncBatchRunning || status == storage.ShopSyncBatchCancelling
}

func isActiveSyncJobStatus(status storage.ShopSyncJobStatus) bool {
	return status == storage.ShopSyncJobQueued || status == storage.ShopSyncJobRunning
}

func uniqueSyncJobIDs(ids []uint) []uint {
	seen := make(map[uint]struct{}, len(ids))
	result := make([]uint, 0, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}

func completedBatchStatus(total, succeeded, failed, skipped int) storage.ShopSyncBatchStatus {
	if total == 0 || succeeded == total {
		return storage.ShopSyncBatchSucceeded
	}
	if succeeded == 0 && failed > 0 && skipped == 0 {
		return storage.ShopSyncBatchFailed
	}
	return storage.ShopSyncBatchPartial
}

func (r *SyncJobRunner) run(jobID, targetID uint, control *syncJobControl) {
	// Every terminal manual result starts the cooldown, including failure or
	// cancellation, so cron cannot immediately recreate upstream pressure.
	defer func() {
		if err := r.monitor.targets.SetLastManualSyncAt(targetID, time.Now()); err != nil && r.log != nil {
			r.log.Warn("record manual shop sync completion failed", "job_id", jobID, "target_id", targetID, "err", err)
		}
	}()
	select {
	case r.slots <- struct{}{}:
	case <-control.ctx.Done():
		r.finishCancelledJob(jobID, time.Now())
		r.cleanupJob(targetID, jobID, control)
		return
	}
	defer func() { <-r.slots }()
	startedAt := time.Now()
	var requestStats *shopprovider.RequestStats
	defer func() {
		if recovered := recover(); recovered != nil {
			finishedAt := time.Now()
			message := fmt.Sprintf("同步任务异常终止: %v", recovered)
			metrics := requestStatsSnapshot(requestStats)
			if err := r.jobs.CompleteWithMetrics(jobID, storage.ShopSyncJobFailed, 0, 0, map[string]int{}, message, startedAt, finishedAt, metrics.Count, metrics.DurationMS); err != nil && r.log != nil {
				r.log.Error("record shop sync job panic failed", "job_id", jobID, "target_id", targetID, "err", err)
			}
			if r.log != nil {
				r.log.Error("shop sync job panicked", "job_id", jobID, "target_id", targetID, "panic", recovered)
			}
		}
		r.cleanupJob(targetID, jobID, control)
	}()
	if control.ctx.Err() != nil {
		r.finishCancelledJob(jobID, startedAt)
		return
	}
	marked, err := r.jobs.TryMarkRunning(jobID, startedAt)
	if err != nil {
		finishedAt := time.Now()
		message := fmt.Sprintf("启动同步任务失败: %v", err)
		if completeErr := r.jobs.Complete(jobID, storage.ShopSyncJobFailed, 0, 0, map[string]int{}, message, startedAt, finishedAt); completeErr != nil && r.log != nil {
			r.log.Error("record shop sync job start failure failed", "job_id", jobID, "target_id", targetID, "err", completeErr)
		}
		if r.log != nil {
			r.log.Error("mark shop sync job running failed", "job_id", jobID, "target_id", targetID, "err", err)
		}
		return
	}
	if !marked {
		return
	}

	ctx, requestStats := shopprovider.WithRequestStats(control.ctx)
	r.requestStats.Store(jobID, requestStats)
	defer r.requestStats.Delete(jobID)
	result, err := r.monitor.SyncByID(ctx, targetID)
	finishedAt := time.Now()
	status := storage.ShopSyncJobSucceeded
	errorMessage := ""
	if err != nil {
		errorMessage = err.Error()
		if errors.Is(control.ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
			status = storage.ShopSyncJobCancelled
			errorMessage = "同步已停止"
		} else if errors.Is(err, context.DeadlineExceeded) {
			status = storage.ShopSyncJobTimedOut
			errorMessage = syncTimeoutMessage()
		} else if isSkippedSyncError(err) {
			status = storage.ShopSyncJobSkipped
		} else {
			status = storage.ShopSyncJobFailed
		}
	}

	goodsCount, changedCount := 0, 0
	events := map[string]int{}
	if result != nil {
		goodsCount = result.GoodsCount
		changedCount = result.ChangedCount
		events = result.Events
	}
	metrics := requestStats.Snapshot()
	if err := r.jobs.CompleteWithMetrics(jobID, status, goodsCount, changedCount, events, errorMessage, startedAt, finishedAt, metrics.Count, metrics.DurationMS); err != nil && r.log != nil {
		r.log.Error("complete shop sync job failed", "job_id", jobID, "target_id", targetID, "err", err)
	}
}

func syncTimeoutMessage() string {
	return fmt.Sprintf("同步超过 %s", shopSyncTimeout)
}

func syncTimeoutMessageFor(ctx context.Context) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "同步批次超过上级时限"
	}
	return syncTimeoutMessage()
}

func (r *SyncJobRunner) finishCancelledJob(jobID uint, finishedAt time.Time) {
	if err := r.jobs.Cancel([]uint{jobID}, finishedAt); err != nil && r.log != nil {
		r.log.Warn("record cancelled shop sync job failed", "job_id", jobID, "err", err)
	}
}

func (r *SyncJobRunner) cleanupJob(targetID, jobID uint, control *syncJobControl) {
	control.cancel()
	r.mu.Lock()
	if r.active[targetID] == jobID {
		delete(r.active, targetID)
	}
	delete(r.controls, jobID)
	r.mu.Unlock()
}

func requestStatsSnapshot(stats *shopprovider.RequestStats) shopprovider.RequestStatsSnapshot {
	if stats == nil {
		return shopprovider.RequestStatsSnapshot{}
	}
	return stats.Snapshot()
}
