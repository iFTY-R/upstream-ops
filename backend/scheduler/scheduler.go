// Package scheduler 用 robfig/cron 触发周期性扫描。
package scheduler

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/ifty-r/upstream-ops/backend/autogroup"
	"github.com/ifty-r/upstream-ops/backend/captcha"
	"github.com/ifty-r/upstream-ops/backend/config"
	"github.com/ifty-r/upstream-ops/backend/crypto"
	"github.com/ifty-r/upstream-ops/backend/monitor"
	"github.com/ifty-r/upstream-ops/backend/priceai"
	"github.com/ifty-r/upstream-ops/backend/shopmonitor"
	"github.com/ifty-r/upstream-ops/backend/storage"
	"github.com/robfig/cron/v3"
)

type Scheduler struct {
	cfg            config.SchedulerConfig
	log            *slog.Logger
	cron           *cron.Cron
	monitor        *monitor.Service
	shopMonitor    *shopmonitor.Service
	shopSyncRunner *shopmonitor.SyncJobRunner
	autoGroup      *autogroup.Service
	monLogs        *storage.MonitorLogs
	rates          *storage.Rates
	notifies       *storage.Notifications
	announcements  *storage.UpstreamAnnouncements
	shopGoods      *storage.ShopGoods
	shopSyncJobs   *storage.ShopSyncJobs
	captchas       *storage.Captchas
	cipher         *crypto.Cipher
	proxy          config.ProxyConfig
	priceAI        *priceai.Service
}

// retentionExecutionMu spans scheduler instances so a hot reload cannot start
// manual cleanup while the previous scheduler is still finishing its cron run.
var retentionExecutionMu sync.Mutex

// ShopRetentionResult reports each independent cleanup category so manual
// callers can surface partial success instead of losing completed work.
type ShopRetentionResult struct {
	HighFrequencyChangesDeleted int64             `json:"high_frequency_changes_deleted"`
	OtherChangesDeleted         int64             `json:"other_changes_deleted"`
	MonitorLogsDeleted          int64             `json:"monitor_logs_deleted"`
	SyncJobsDeleted             int64             `json:"sync_jobs_deleted"`
	TotalDeleted                int64             `json:"total_deleted"`
	Errors                      map[string]string `json:"errors,omitempty"`
}

func New(
	cfg config.SchedulerConfig,
	m *monitor.Service,
	shop *shopmonitor.Service,
	autoGroup *autogroup.Service,
	monLogs *storage.MonitorLogs,
	rates *storage.Rates,
	notifies *storage.Notifications,
	announcements *storage.UpstreamAnnouncements,
	shopGoods *storage.ShopGoods,
	shopSyncJobs *storage.ShopSyncJobs,
	captchas *storage.Captchas,
	cipher *crypto.Cipher,
	proxy config.ProxyConfig,
	log *slog.Logger,
) *Scheduler {
	return &Scheduler{
		cfg:           cfg,
		log:           log,
		cron:          cron.New(cron.WithSeconds(), cron.WithChain(cron.SkipIfStillRunning(cron.DefaultLogger))),
		monitor:       m,
		shopMonitor:   shop,
		autoGroup:     autoGroup,
		monLogs:       monLogs,
		rates:         rates,
		notifies:      notifies,
		announcements: announcements,
		shopGoods:     shopGoods,
		shopSyncJobs:  shopSyncJobs,
		captchas:      captchas,
		cipher:        cipher,
		proxy:         proxy,
	}
}

func (s *Scheduler) SetShopSyncRunner(runner *shopmonitor.SyncJobRunner) {
	s.shopSyncRunner = runner
}

// SetPriceAIService keeps the scheduler on the same singleton Feed service
// used by manual API calls, so coalescing and persisted cadence apply equally.
func (s *Scheduler) SetPriceAIService(service *priceai.Service) {
	s.priceAI = service
}

func (s *Scheduler) Start() error {
	if err := config.ValidateSchedulerConfig(s.cfg); err != nil {
		return err
	}
	if s.cfg.BalanceCron != "" {
		if _, err := s.cron.AddFunc(s.cfg.BalanceCron, s.runBalance); err != nil {
			return err
		}
	}
	if s.cfg.RateCron != "" {
		if _, err := s.cron.AddFunc(s.cfg.RateCron, s.runRates); err != nil {
			return err
		}
	}
	if s.cfg.AutoGroup.Enabled && s.cfg.AutoGroup.Cron != "" && s.autoGroup != nil {
		if _, err := s.cron.AddFunc(s.cfg.AutoGroup.Cron, s.runAutoGroups); err != nil {
			return err
		}
	}
	if s.cfg.ShopEnabled && s.cfg.ShopCron != "" && s.shopMonitor != nil {
		if _, err := s.cron.AddFunc(s.cfg.ShopCron, s.runShops); err != nil {
			return err
		}
	}
	if s.cfg.PriceAIFeedCron != "" && s.priceAI != nil {
		if _, err := s.cron.AddFunc(s.cfg.PriceAIFeedCron, s.runPriceAIFeed); err != nil {
			return err
		}
	}
	if s.cfg.PriceAIRiskCron != "" && s.priceAI != nil {
		if _, err := s.cron.AddFunc(s.cfg.PriceAIRiskCron, s.runPriceAIRisk); err != nil {
			return err
		}
	}
	if s.cfg.Retention.Cron != "" && s.hasRetention() {
		if _, err := s.cron.AddFunc(s.cfg.Retention.Cron, s.runRetention); err != nil {
			return err
		}
	}
	s.cron.Start()
	s.log.Info("scheduler started",
		"balanceCron", s.cfg.BalanceCron,
		"rateCron", s.cfg.RateCron,
		"shopEnabled", s.cfg.ShopEnabled,
		"shopCron", s.cfg.ShopCron,
		"priceAIFeedCron", s.cfg.PriceAIFeedCron,
		"priceAIRiskCron", s.cfg.PriceAIRiskCron,
		"autoGroupEnabled", s.cfg.AutoGroup.Enabled,
		"autoGroupCron", s.cfg.AutoGroup.Cron,
		"autoGroupConcurrency", s.cfg.AutoGroup.Concurrency,
		"retentionCron", s.cfg.Retention.Cron,
		"concurrency", s.cfg.Concurrency,
	)
	return nil
}

func (s *Scheduler) Stop() {
	if s.cron != nil {
		<-s.cron.Stop().Done()
	}
}

func (s *Scheduler) runBalance() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	s.monitor.ScanAllBalances(ctx)
	if s.captchas != nil && s.cipher != nil {
		if _, err := captcha.RefreshAllBalancesWithProxy(ctx, s.captchas, s.cipher, s.log, s.proxy); err != nil {
			s.log.Warn("refresh captcha balances failed", "err", err)
		}
	}
}

func (s *Scheduler) runRates() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	s.monitor.ScanAllRates(ctx)
	if s.autoGroup != nil && !s.cfg.AutoGroup.Enabled {
		s.autoGroup.EvaluateAllEnabled(ctx)
	}
}

func (s *Scheduler) runAutoGroups() {
	if s.autoGroup == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	concurrency := s.cfg.AutoGroup.Concurrency
	if concurrency <= 0 {
		concurrency = 1
	}
	s.autoGroup.EvaluateAllEnabledWithConcurrency(ctx, concurrency)
}

func (s *Scheduler) runShops() {
	if s.shopMonitor == nil {
		return
	}
	concurrency := s.cfg.Concurrency
	if concurrency <= 0 {
		concurrency = 1
	}
	if s.shopSyncRunner != nil {
		s.shopSyncRunner.SyncAllScheduled(context.Background(), concurrency)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	s.shopMonitor.SyncAllWithConcurrency(ctx, concurrency)
}

func (s *Scheduler) runPriceAIFeed() {
	if s.priceAI == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	result, err := s.priceAI.Sync(ctx)
	if err != nil {
		if s.log != nil {
			s.log.Warn("priceai feed sync failed", "err", err)
		}
		return
	}
	if s.log != nil {
		s.log.Debug("priceai feed sync completed", "snapshot_id", result.SnapshotID, "not_modified", result.NotModified, "skipped", result.Skipped, "products", result.ProductsCount)
	}
}

func (s *Scheduler) runPriceAIRisk() {
	if s.priceAI == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	concurrency := s.cfg.PriceAIRiskConcurrency
	if concurrency <= 0 {
		concurrency = 1
	}
	result, err := s.priceAI.RefreshRiskWithConcurrency(ctx, concurrency)
	if err != nil {
		if s.log != nil {
			s.log.Warn("priceai risk refresh failed", "err", err, "attempted", result.ProductsAttempted, "failures", result.Failures)
		}
		return
	}
	if s.log != nil {
		s.log.Debug("priceai risk refresh completed", "snapshot_id", result.SnapshotID, "attempted", result.ProductsAttempted, "updated", result.FeedbackUpdated, "skipped", result.ProductsSkipped)
	}
}

func (s *Scheduler) hasRetention() bool {
	r := s.cfg.Retention
	return r.MonitorLogsDays > 0 ||
		r.BalanceSnapshotsDays > 0 ||
		r.NotificationLogsDays > 0 ||
		r.AnnouncementsDays > 0 ||
		r.ShopHighFrequencyChangeLogsDays > 0 ||
		r.ShopOtherChangeLogsDays > 0 ||
		r.ShopMonitorLogsDays > 0 ||
		r.ShopSyncJobsDays > 0
}

// runRetention 按配置删除过期历史。任一表失败不影响其它，全部错误写日志。
func (s *Scheduler) runRetention() {
	retentionExecutionMu.Lock()
	defer retentionExecutionMu.Unlock()

	r := s.cfg.Retention
	now := time.Now()

	if r.MonitorLogsDays > 0 {
		cutoff := now.AddDate(0, 0, -r.MonitorLogsDays)
		n, err := s.monLogs.DeleteBefore(cutoff)
		if err != nil {
			s.log.Warn("retention monitor_logs failed", "err", err)
		} else if n > 0 {
			s.log.Info("retention monitor_logs deleted", "rows", n, "before", cutoff)
		}
	}

	if r.BalanceSnapshotsDays > 0 {
		cutoff := now.AddDate(0, 0, -r.BalanceSnapshotsDays)
		n, err := s.rates.DeleteBalanceSnapshotsBefore(cutoff)
		if err != nil {
			s.log.Warn("retention balance_snapshots failed", "err", err)
		} else if n > 0 {
			s.log.Info("retention balance_snapshots deleted", "rows", n, "before", cutoff)
		}

		n, err = s.rates.DeleteCostSnapshotsBefore(cutoff)
		if err != nil {
			s.log.Warn("retention cost_snapshots failed", "err", err)
		} else if n > 0 {
			s.log.Info("retention cost_snapshots deleted", "rows", n, "before", cutoff)
		}
	}

	if r.NotificationLogsDays > 0 {
		cutoff := now.AddDate(0, 0, -r.NotificationLogsDays)
		n, err := s.notifies.DeleteLogsBefore(cutoff)
		if err != nil {
			s.log.Warn("retention notification_logs failed", "err", err)
		} else if n > 0 {
			s.log.Info("retention notification_logs deleted", "rows", n, "before", cutoff)
		}
	}

	if r.AnnouncementsDays > 0 && s.announcements != nil {
		cutoff := now.AddDate(0, 0, -r.AnnouncementsDays)
		n, err := s.announcements.DeleteBefore(cutoff)
		if err != nil {
			s.log.Warn("retention announcements failed", "err", err)
		} else if n > 0 {
			s.log.Info("retention announcements deleted", "rows", n, "before", cutoff)
		}
	}

	s.runShopRetention(r, now)
}

// RunShopRetention immediately applies the supplied shop-only policy. Manual
// and scheduled cleanup share one process-wide lock so their DELETE statements
// cannot overlap against SQLite.
func (s *Scheduler) RunShopRetention(retention config.RetentionConfig) ShopRetentionResult {
	retentionExecutionMu.Lock()
	defer retentionExecutionMu.Unlock()
	return s.runShopRetention(retention, time.Now())
}

func (s *Scheduler) runShopRetention(retention config.RetentionConfig, now time.Time) ShopRetentionResult {
	result := ShopRetentionResult{}
	highFrequencyEvents := []storage.ShopGoodsChangeEvent{
		storage.ShopChangeStockChanged,
		storage.ShopChangeMonitorFailed,
	}
	if retention.ShopHighFrequencyChangeLogsDays > 0 {
		cutoff := now.AddDate(0, 0, -retention.ShopHighFrequencyChangeLogsDays)
		if s.shopGoods == nil {
			result.addError("high_frequency_changes", "shop goods repository is unavailable")
		} else {
			n, err := s.shopGoods.DeleteChangesBefore(cutoff, highFrequencyEvents)
			result.HighFrequencyChangesDeleted = n
			result.TotalDeleted += n
			s.logShopRetention("high-frequency shop changes", n, cutoff, err)
			result.addErrorFrom("high_frequency_changes", err)
		}
	}
	if retention.ShopOtherChangeLogsDays > 0 {
		cutoff := now.AddDate(0, 0, -retention.ShopOtherChangeLogsDays)
		if s.shopGoods == nil {
			result.addError("other_changes", "shop goods repository is unavailable")
		} else {
			n, err := s.shopGoods.DeleteChangesBeforeExcluding(cutoff, highFrequencyEvents)
			result.OtherChangesDeleted = n
			result.TotalDeleted += n
			s.logShopRetention("other shop changes", n, cutoff, err)
			result.addErrorFrom("other_changes", err)
		}
	}
	if retention.ShopMonitorLogsDays > 0 {
		cutoff := now.AddDate(0, 0, -retention.ShopMonitorLogsDays)
		if s.shopGoods == nil {
			result.addError("monitor_logs", "shop goods repository is unavailable")
		} else {
			n, err := s.shopGoods.DeleteMonitorLogsBefore(cutoff)
			result.MonitorLogsDeleted = n
			result.TotalDeleted += n
			s.logShopRetention("shop monitor logs", n, cutoff, err)
			result.addErrorFrom("monitor_logs", err)
		}
	}
	if retention.ShopSyncJobsDays > 0 {
		cutoff := now.AddDate(0, 0, -retention.ShopSyncJobsDays)
		if s.shopSyncJobs == nil {
			result.addError("sync_jobs", "shop sync jobs repository is unavailable")
		} else {
			n, err := s.shopSyncJobs.DeleteFinishedBefore(cutoff)
			result.SyncJobsDeleted = n
			result.TotalDeleted += n
			s.logShopRetention("shop sync jobs", n, cutoff, err)
			result.addErrorFrom("sync_jobs", err)
		}
	}
	return result
}

func (s *Scheduler) logShopRetention(category string, rows int64, cutoff time.Time, err error) {
	if s.log == nil {
		return
	}
	if err != nil {
		s.log.Warn("retention "+category+" failed", "err", err)
	} else if rows > 0 {
		s.log.Info("retention "+category+" deleted", "rows", rows, "before", cutoff)
	}
}

func (r *ShopRetentionResult) addErrorFrom(category string, err error) {
	if err != nil {
		r.addError(category, err.Error())
	}
}

func (r *ShopRetentionResult) addError(category, message string) {
	if r.Errors == nil {
		r.Errors = make(map[string]string)
	}
	r.Errors[category] = message
}
