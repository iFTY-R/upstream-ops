package shopmonitor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

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

func (r *SyncJobRunner) run(jobID, targetID uint) {
	r.slots <- struct{}{}
	defer func() { <-r.slots }()
	startedAt := time.Now()
	defer func() {
		if recovered := recover(); recovered != nil {
			finishedAt := time.Now()
			message := fmt.Sprintf("同步任务异常终止: %v", recovered)
			if err := r.jobs.Complete(jobID, storage.ShopSyncJobFailed, 0, 0, map[string]int{}, message, startedAt, finishedAt); err != nil && r.log != nil {
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
	if err := r.jobs.Complete(jobID, status, goodsCount, changedCount, events, errorMessage, startedAt, finishedAt); err != nil && r.log != nil {
		r.log.Error("complete shop sync job failed", "job_id", jobID, "target_id", targetID, "err", err)
	}
}
