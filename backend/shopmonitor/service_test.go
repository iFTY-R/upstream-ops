package shopmonitor

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ifty-r/upstream-ops/backend/config"
	"github.com/ifty-r/upstream-ops/backend/shopprovider"
	"github.com/ifty-r/upstream-ops/backend/storage"
	"gorm.io/gorm"
)

func openShopMonitorTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := storage.Open(storage.DBConfig{
		Driver:       storage.DBDriverSQLite,
		Path:         filepath.Join(t.TempDir(), "test.db"),
		MaxOpenConns: 4,
		MaxIdleConns: 2,
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatalf("migrate db: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func TestRefreshGoodsByKeyUpdatesOnlyOneSnapshot(t *testing.T) {
	platform := storage.ShopPlatform("refresh-stock-test")
	shopprovider.Register(platform, func() shopprovider.Provider {
		return fakeShopProvider{goods: []shopprovider.Goods{
			{
				GoodsKey:     "abc",
				GoodsType:    "card",
				Name:         "GPT Pro",
				Link:         "https://example.invalid/item/abc",
				Price:        2.5,
				CategoryID:   10,
				CategoryName: "GPT",
				StockCount:   6,
				LimitCount:   1,
			},
		}}
	})

	db := openShopMonitorTestDB(t)
	targets := storage.NewShopTargets(db)
	goodsRepo := storage.NewShopGoods(db)
	target := createRefreshTarget(t, targets, platform)
	now := time.Now().Add(-time.Hour)
	snapshot := storage.ShopGoodsSnapshot{
		TargetID:     target.ID,
		GoodsKey:     "abc",
		GoodsType:    "card",
		Name:         "GPT Pro",
		CategoryID:   10,
		CategoryName: "GPT",
		Link:         "https://example.invalid/item/abc",
		Price:        2.5,
		StockCount:   0,
		FirstSeenAt:  now,
		LastSeenAt:   now,
	}
	other := storage.ShopGoodsSnapshot{
		TargetID:     target.ID,
		GoodsKey:     "other",
		GoodsType:    "card",
		Name:         "Other",
		CategoryID:   10,
		CategoryName: "GPT",
		Price:        1,
		StockCount:   3,
		FirstSeenAt:  now,
		LastSeenAt:   now,
	}
	if err := goodsRepo.CreateSnapshot(&snapshot); err != nil {
		t.Fatalf("create snapshot: %v", err)
	}
	if err := goodsRepo.CreateSnapshot(&other); err != nil {
		t.Fatalf("create other snapshot: %v", err)
	}

	service := NewService(targets, storage.NewShopWatchRules(db), goodsRepo, nil, nil, config.ProxyConfig{}, config.UpstreamConfig{})
	result, err := service.RefreshGoodsByKey(context.Background(), target.ID, "abc")
	if err != nil {
		t.Fatalf("refresh goods: %v", err)
	}
	if !result.Found || !result.Changed || result.Snapshot.StockCount != 6 {
		t.Fatalf("unexpected refresh result: %#v", result)
	}
	got, err := goodsRepo.FindSnapshot(target.ID, "abc")
	if err != nil {
		t.Fatalf("find refreshed snapshot: %v", err)
	}
	if got.StockCount != 6 || got.RemovedAt != nil {
		t.Fatalf("snapshot not refreshed: %#v", got)
	}
	gotOther, err := goodsRepo.FindSnapshot(target.ID, "other")
	if err != nil {
		t.Fatalf("find other snapshot: %v", err)
	}
	if gotOther.StockCount != 3 || gotOther.RemovedAt != nil {
		t.Fatalf("other snapshot should be untouched: %#v", gotOther)
	}
}

func TestRefreshGoodsByKeyMarksMissingSnapshotRemoved(t *testing.T) {
	platform := storage.ShopPlatform("refresh-missing-test")
	shopprovider.Register(platform, func() shopprovider.Provider { return fakeShopProvider{} })

	db := openShopMonitorTestDB(t)
	targets := storage.NewShopTargets(db)
	goodsRepo := storage.NewShopGoods(db)
	target := createRefreshTarget(t, targets, platform)
	now := time.Now().Add(-time.Hour)
	snapshot := storage.ShopGoodsSnapshot{
		TargetID:    target.ID,
		GoodsKey:    "missing",
		GoodsType:   "card",
		Name:        "Missing",
		Price:       1,
		StockCount:  2,
		FirstSeenAt: now,
		LastSeenAt:  now,
	}
	if err := goodsRepo.CreateSnapshot(&snapshot); err != nil {
		t.Fatalf("create snapshot: %v", err)
	}

	service := NewService(targets, storage.NewShopWatchRules(db), goodsRepo, nil, nil, config.ProxyConfig{}, config.UpstreamConfig{})
	result, err := service.RefreshGoodsByKey(context.Background(), target.ID, "missing")
	if err != nil {
		t.Fatalf("refresh missing goods: %v", err)
	}
	if result.Found || !result.Changed || result.Snapshot.RemovedAt == nil {
		t.Fatalf("missing snapshot should be marked removed: %#v", result)
	}
}

func TestSyncJobRunnerReusesActiveTargetJob(t *testing.T) {
	platform := storage.ShopPlatform("sync-job-runner-test")
	provider := &blockingShopProvider{started: make(chan struct{}), release: make(chan struct{})}
	shopprovider.Register(platform, func() shopprovider.Provider { return provider })

	db := openShopMonitorTestDB(t)
	targets := storage.NewShopTargets(db)
	goods := storage.NewShopGoods(db)
	target := createRefreshTarget(t, targets, platform)
	monitor := NewService(targets, storage.NewShopWatchRules(db), goods, nil, nil, config.ProxyConfig{}, config.UpstreamConfig{})
	runner := NewSyncJobRunner(monitor, storage.NewShopSyncJobs(db), nil)

	first, reused, err := runner.Start(target.ID)
	if err != nil || reused {
		t.Fatalf("start first job: job=%#v reused=%v err=%v", first, reused, err)
	}
	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("sync job did not start")
	}
	second, reused, err := runner.Start(target.ID)
	if err != nil || !reused || second.ID != first.ID {
		t.Fatalf("start duplicate job: first=%#v second=%#v reused=%v err=%v", first, second, reused, err)
	}
	close(provider.release)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		job, err := runner.Get(target.ID, first.ID)
		if err != nil {
			t.Fatalf("get job: %v", err)
		}
		if job.Status == storage.ShopSyncJobSucceeded {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("sync job did not complete")
}

func TestDeleteTargetWaitsForActiveSync(t *testing.T) {
	platform := storage.ShopPlatform("delete-target-lock-test")
	provider := &blockingShopProvider{started: make(chan struct{}), release: make(chan struct{})}
	shopprovider.Register(platform, func() shopprovider.Provider { return provider })

	db := openShopMonitorTestDB(t)
	targets := storage.NewShopTargets(db)
	goods := storage.NewShopGoods(db)
	target := createRefreshTarget(t, targets, platform)
	monitor := NewService(targets, storage.NewShopWatchRules(db), goods, nil, nil, config.ProxyConfig{}, config.UpstreamConfig{})

	syncDone := make(chan error, 1)
	go func() {
		_, err := monitor.SyncByID(context.Background(), target.ID)
		syncDone <- err
	}()
	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("sync did not reach upstream call")
	}

	deleteDone := make(chan error, 1)
	go func() { deleteDone <- monitor.DeleteTarget(target.ID) }()
	select {
	case err := <-deleteDone:
		t.Fatalf("delete completed before sync released: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(provider.release)
	if err := <-syncDone; err != nil {
		t.Fatalf("sync: %v", err)
	}
	if err := <-deleteDone; err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := targets.FindByID(target.ID); err == nil {
		t.Fatal("target still exists after delete")
	}
}

func TestFetchGoodsReadsPastFiftyPages(t *testing.T) {
	items := make([]shopprovider.Goods, 2501)
	for i := range items {
		items[i] = shopprovider.Goods{GoodsKey: fmt.Sprintf("goods-%d", i), Name: "item"}
	}
	service := NewService(nil, nil, nil, nil, nil, config.ProxyConfig{}, config.UpstreamConfig{})
	result, err := service.fetchGoods(context.Background(), pagedShopProvider{goods: items}, shopprovider.Target{}, &storage.ShopTarget{
		ScopeMode:      storage.ShopScopeAll,
		GoodsTypesJSON: `["card"]`,
	})
	if err != nil {
		t.Fatalf("fetch goods: %v", err)
	}
	if len(result) != len(items) {
		t.Fatalf("goods count = %d, want %d", len(result), len(items))
	}
}

func createRefreshTarget(t *testing.T, targets *storage.ShopTargets, platform storage.ShopPlatform) *storage.ShopTarget {
	t.Helper()
	target := &storage.ShopTarget{
		Name:           string(platform),
		Platform:       platform,
		SiteURL:        "https://example.invalid/shop/TOKEN",
		BaseURL:        "https://example.invalid",
		Token:          "TOKEN",
		MonitorEnabled: true,
		ScopeMode:      storage.ShopScopeAll,
		GoodsTypesJSON: `["card"]`,
	}
	if err := targets.Create(target); err != nil {
		t.Fatalf("create target: %v", err)
	}
	return target
}

type fakeShopProvider struct {
	goods []shopprovider.Goods
}

type pagedShopProvider struct {
	goods []shopprovider.Goods
}

func TestHasMoreShopGoodsPagesUsesTotalAndFailsClosedAtLimit(t *testing.T) {
	if more, err := hasMoreShopGoodsPages(2, shopGoodsPageSize, 75, 25); err != nil || more {
		t.Fatalf("final short page: more=%v err=%v, want no more", more, err)
	}
	if more, err := hasMoreShopGoodsPages(1, shopGoodsPageSize, 0, shopGoodsPageSize); err != nil || !more {
		t.Fatalf("unknown total first page: more=%v err=%v, want more", more, err)
	}
	if more, err := hasMoreShopGoodsPages(maxShopGoodsPages, shopGoodsPageSize, 0, shopGoodsPageSize); err == nil || more {
		t.Fatalf("page limit: more=%v err=%v, want an error", more, err)
	}
}

type blockingShopProvider struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (p *blockingShopProvider) Info(ctx context.Context, _ shopprovider.Target) (*shopprovider.ShopInfo, error) {
	p.once.Do(func() { close(p.started) })
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.release:
		return &shopprovider.ShopInfo{Name: "background"}, nil
	}
}

func (p *blockingShopProvider) Categories(context.Context, shopprovider.Target, shopprovider.CategoryRequest) ([]shopprovider.Category, error) {
	return nil, nil
}

func (p *blockingShopProvider) Goods(context.Context, shopprovider.Target, shopprovider.GoodsRequest) (*shopprovider.GoodsPage, error) {
	return &shopprovider.GoodsPage{}, nil
}

func (p *blockingShopProvider) Price(context.Context, shopprovider.Target, shopprovider.PriceRequest) (*shopprovider.PriceResult, error) {
	return &shopprovider.PriceResult{}, nil
}

func (p fakeShopProvider) Info(context.Context, shopprovider.Target) (*shopprovider.ShopInfo, error) {
	return &shopprovider.ShopInfo{Name: "fake"}, nil
}

func (p fakeShopProvider) Categories(context.Context, shopprovider.Target, shopprovider.CategoryRequest) ([]shopprovider.Category, error) {
	return nil, nil
}

func (p fakeShopProvider) Goods(context.Context, shopprovider.Target, shopprovider.GoodsRequest) (*shopprovider.GoodsPage, error) {
	return &shopprovider.GoodsPage{Total: len(p.goods), List: p.goods}, nil
}

func (p fakeShopProvider) Price(context.Context, shopprovider.Target, shopprovider.PriceRequest) (*shopprovider.PriceResult, error) {
	return nil, nil
}

func (p pagedShopProvider) Info(context.Context, shopprovider.Target) (*shopprovider.ShopInfo, error) {
	return &shopprovider.ShopInfo{}, nil
}

func (p pagedShopProvider) Categories(context.Context, shopprovider.Target, shopprovider.CategoryRequest) ([]shopprovider.Category, error) {
	return nil, nil
}

func (p pagedShopProvider) Goods(_ context.Context, _ shopprovider.Target, req shopprovider.GoodsRequest) (*shopprovider.GoodsPage, error) {
	page := req.Page
	if page <= 0 {
		page = 1
	}
	pageSize := req.PageSize
	if pageSize <= 0 {
		pageSize = shopGoodsPageSize
	}
	start := (page - 1) * pageSize
	if start >= len(p.goods) {
		return &shopprovider.GoodsPage{Total: len(p.goods)}, nil
	}
	end := start + pageSize
	if end > len(p.goods) {
		end = len(p.goods)
	}
	return &shopprovider.GoodsPage{Total: len(p.goods), List: p.goods[start:end]}, nil
}

func (p pagedShopProvider) Price(context.Context, shopprovider.Target, shopprovider.PriceRequest) (*shopprovider.PriceResult, error) {
	return nil, nil
}
