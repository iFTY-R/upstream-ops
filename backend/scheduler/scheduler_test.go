package scheduler

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/ifty-r/upstream-ops/backend/config"
	"github.com/ifty-r/upstream-ops/backend/monitor"
	"github.com/ifty-r/upstream-ops/backend/storage"
	"gorm.io/gorm"
)

func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := storage.Open(storage.DBConfig{
		Driver: storage.DBDriverSQLite,
		Path:   filepath.Join(t.TempDir(), "scheduler-test.db"),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db handle: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func TestRunRetentionDeletesAnnouncements(t *testing.T) {
	db := openTestDB(t)
	announcements := storage.NewUpstreamAnnouncements(db)
	notifies := storage.NewNotifications(db)
	monLogs := storage.NewMonitorLogs(db)
	rates := storage.NewRates(db)

	oldTime := time.Now().AddDate(0, 0, -10)
	if _, err := announcements.Sync(1, []storage.UpstreamAnnouncement{{
		ChannelID:   1,
		SourceKey:   "old",
		Content:     "old",
		FirstSeenAt: oldTime,
	}}); err != nil {
		t.Fatalf("sync announcement: %v", err)
	}

	s := New(
		config.SchedulerConfig{
			Retention: config.RetentionConfig{
				AnnouncementsDays: 1,
			},
		},
		&monitor.Service{},
		nil,
		nil,
		monLogs,
		rates,
		notifies,
		announcements,
		nil,
		nil,
		nil,
		nil,
		config.ProxyConfig{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	s.runRetention()

	list, total, err := announcements.ListPage(1, 10)
	if err != nil {
		t.Fatalf("list announcements: %v", err)
	}
	if total != 0 || len(list) != 0 {
		t.Fatalf("announcements not cleaned: total=%d list=%#v", total, list)
	}
}

func TestRunRetentionDeletesTieredShopHistory(t *testing.T) {
	db := openTestDB(t)
	goods := storage.NewShopGoods(db)
	jobs := storage.NewShopSyncJobs(db)
	now := time.Now()

	changes := []storage.ShopGoodsChangeLog{
		{TargetID: 1, Event: storage.ShopChangeStockChanged, Summary: "old stock", ChangedAt: now.AddDate(0, 0, -20)},
		{TargetID: 1, Event: storage.ShopChangePriceChanged, Summary: "old price", ChangedAt: now.AddDate(0, 0, -100)},
	}
	for i := range changes {
		if err := goods.AppendChange(&changes[i]); err != nil {
			t.Fatalf("append change: %v", err)
		}
	}
	oldMonitorAt := now.AddDate(0, 0, -31)
	if err := goods.AppendMonitorLog(&storage.ShopMonitorLog{TargetID: 1, Success: true, StartedAt: oldMonitorAt, FinishedAt: oldMonitorAt.Add(time.Second)}); err != nil {
		t.Fatalf("append shop monitor log: %v", err)
	}
	oldFinishedAt := now.AddDate(0, 0, -31)
	if err := jobs.Create(&storage.ShopSyncJob{TargetID: 1, Status: storage.ShopSyncJobSucceeded, FinishedAt: &oldFinishedAt, CreatedAt: oldFinishedAt}); err != nil {
		t.Fatalf("create shop sync job: %v", err)
	}

	s := New(
		config.SchedulerConfig{Retention: config.RetentionConfig{
			ShopHighFrequencyChangeLogsDays: 15,
			ShopOtherChangeLogsDays:         90,
			ShopMonitorLogsDays:             30,
			ShopSyncJobsDays:                30,
		}},
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		goods,
		jobs,
		nil,
		nil,
		config.ProxyConfig{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	s.runRetention()

	for _, model := range []any{&storage.ShopGoodsChangeLog{}, &storage.ShopMonitorLog{}, &storage.ShopSyncJob{}} {
		var count int64
		if err := db.Model(model).Count(&count).Error; err != nil {
			t.Fatalf("count %T: %v", model, err)
		}
		if count != 0 {
			t.Fatalf("remaining %T rows = %d", model, count)
		}
	}
}
