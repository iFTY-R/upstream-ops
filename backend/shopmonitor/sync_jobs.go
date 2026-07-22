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
	shopSyncJobTimeout     = 2 * time.Minute
	shopSyncJobConcurrency = 2
)

// SyncJobRunner runs manual shop synchronizations after the HTTP request has
// completed. It keeps one active job per target while durable job records make
// progress and failures queryable from the UI.
type SyncJobRunner struct {
	monitor *Service
	jobs    *storage.ShopSyncJobs
	log     *slog.Logger

	mu     sync.Mutex
	active map[uint]uint
	slots  chan struct{}

	requestStats sync.Map
}

func NewSyncJobRunner(monitor *Service, jobs *storage.ShopSyncJobs, log *slog.Logger) *SyncJobRunner {
	runner := &SyncJobRunner{
		monitor: monitor,
		jobs:    jobs,
		log:     log,
		active:  make(map[uint]uint),
		slots:   make(chan struct{}, shopSyncJobConcurrency),
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
	r.active[targetID] = job.ID
	go r.run(job.ID, targetID)
	return job, false, nil
}

func (r *SyncJobRunner) Get(targetID, jobID uint) (*storage.ShopSyncJob, error) {
	if r == nil || r.jobs == nil {
		return nil, fmt.Errorf("shop sync job runner is unavailable")
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
func (r *SyncJobRunner) SyncAllScheduled(ctx context.Context, concurrency int) *SyncAllResult {
	if r == nil || r.monitor == nil {
		return &SyncAllResult{Failed: 1, Targets: []SyncAllTargetResult{{Error: "shop sync job runner is unavailable"}}}
	}
	list, err := r.monitor.targets.ListMonitorEnabled()
	if err != nil {
		return r.monitor.syncAllListError(err)
	}
	if r.jobs == nil {
		return r.monitor.syncTargetsWithConcurrency(ctx, list, concurrency, nil)
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
		return r.monitor.syncTargetsWithConcurrency(ctx, list, concurrency, nil)
	}

	jobStartedAt := make([]time.Time, len(jobs))
	stats := make([]*shopprovider.RequestStats, len(jobs))
	hooks := &syncAllHooks{
		beforeTarget: func(parent context.Context, index int, _ storage.ShopTarget) context.Context {
			jobStartedAt[index] = time.Now()
			if err := r.jobs.MarkRunning(jobs[index].ID, jobStartedAt[index]); err != nil && r.log != nil {
				r.log.Warn("mark scheduled shop sync running failed", "job_id", jobs[index].ID, "err", err)
			}
			observedCtx, requestStats := shopprovider.WithRequestStats(parent)
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
				} else if errors.Is(ctx.Err(), context.DeadlineExceeded) {
					status = storage.ShopSyncJobTimedOut
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
			metrics := stats[index].Snapshot()
			if err := r.jobs.CompleteWithMetrics(jobs[index].ID, status, goodsCount, changedCount, events, errorMessage, jobStartedAt[index], finishedAt, metrics.Count, metrics.DurationMS); err != nil && r.log != nil {
				r.log.Warn("complete scheduled shop sync job failed", "job_id", jobs[index].ID, "target_id", target.ID, "err", err)
			}
			r.requestStats.Delete(jobs[index].ID)
			r.mu.Lock()
			if r.active[target.ID] == jobs[index].ID {
				delete(r.active, target.ID)
			}
			r.mu.Unlock()
		},
	}

	result := r.monitor.syncTargetsWithConcurrency(ctx, list, concurrency, hooks)
	if _, err := r.refreshBatch(batch); err != nil && r.log != nil {
		r.log.Warn("complete scheduled shop sync batch failed", "batch_id", batch.ID, "err", err)
	}
	return result
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
		} else if batch.Status != storage.ShopSyncBatchRunning {
			return
		}
		<-ticker.C
	}
}

func (r *SyncJobRunner) refreshBatch(batch *storage.ShopSyncBatch) (*storage.ShopSyncBatch, error) {
	if batch.Status != storage.ShopSyncBatchRunning {
		return batch, nil
	}
	var jobIDs []uint
	if batch.JobIDsJSON != "" {
		if err := json.Unmarshal([]byte(batch.JobIDsJSON), &jobIDs); err != nil {
			return nil, fmt.Errorf("decode shop sync batch %d jobs: %w", batch.ID, err)
		}
	}
	jobs, err := r.jobs.FindByIDs(jobIDs)
	if err != nil {
		return nil, err
	}

	succeeded, failed, skipped := 0, batch.StartFailedCount, 0
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
	if active {
		result.DurationMS = max(time.Since(batch.StartedAt).Milliseconds(), 0)
		return &result, nil
	}
	if finishedAt.IsZero() || missing > 0 {
		finishedAt = time.Now()
	}
	result.Status = completedBatchStatus(result.TotalCount, succeeded, failed, skipped)
	result.FinishedAt = &finishedAt
	result.DurationMS = max(finishedAt.Sub(batch.StartedAt).Milliseconds(), 0)
	if err := r.jobs.CompleteBatch(&result); err != nil {
		return nil, err
	}
	return &result, nil
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

func (r *SyncJobRunner) run(jobID, targetID uint) {
	r.slots <- struct{}{}
	defer func() { <-r.slots }()
	startedAt := time.Now()
	var requestStats *shopprovider.RequestStats
	defer func() {
		if recovered := recover(); recovered != nil {
			finishedAt := time.Now()
			message := fmt.Sprintf("同步任务异常终止: %v", recovered)
			metrics := requestStats.Snapshot()
			if err := r.jobs.CompleteWithMetrics(jobID, storage.ShopSyncJobFailed, 0, 0, map[string]int{}, message, startedAt, finishedAt, metrics.Count, metrics.DurationMS); err != nil && r.log != nil {
				r.log.Error("record shop sync job panic failed", "job_id", jobID, "target_id", targetID, "err", err)
			}
			if r.log != nil {
				r.log.Error("shop sync job panicked", "job_id", jobID, "target_id", targetID, "panic", recovered)
			}
		}
		r.mu.Lock()
		delete(r.active, targetID)
		r.mu.Unlock()
	}()
	if err := r.jobs.MarkRunning(jobID, startedAt); err != nil {
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

	ctx, cancel := context.WithTimeout(context.Background(), shopSyncJobTimeout)
	defer cancel()
	ctx, requestStats = shopprovider.WithRequestStats(ctx)
	r.requestStats.Store(jobID, requestStats)
	defer r.requestStats.Delete(jobID)
	result, err := r.monitor.SyncByID(ctx, targetID)
	finishedAt := time.Now()
	status := storage.ShopSyncJobSucceeded
	errorMessage := ""
	if err != nil {
		if isSkippedSyncError(err) {
			status = storage.ShopSyncJobSkipped
		} else {
			status = storage.ShopSyncJobFailed
		}
		errorMessage = err.Error()
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			status = storage.ShopSyncJobTimedOut
			errorMessage = fmt.Sprintf("同步超过 %s", shopSyncJobTimeout)
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
