package shopmonitor

import (
	"context"
	"path/filepath"
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

	service := NewService(targets, goodsRepo, nil, nil, config.ProxyConfig{}, config.UpstreamConfig{})
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

	service := NewService(targets, goodsRepo, nil, nil, config.ProxyConfig{}, config.UpstreamConfig{})
	result, err := service.RefreshGoodsByKey(context.Background(), target.ID, "missing")
	if err != nil {
		t.Fatalf("refresh missing goods: %v", err)
	}
	if result.Found || !result.Changed || result.Snapshot.RemovedAt == nil {
		t.Fatalf("missing snapshot should be marked removed: %#v", result)
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
