package shopmonitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ifty-r/upstream-ops/backend/config"
	"github.com/ifty-r/upstream-ops/backend/crypto"
	"github.com/ifty-r/upstream-ops/backend/notify"
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

func TestShopNotificationsUseGlobalChannels(t *testing.T) {
	db := openShopMonitorTestDB(t)
	targets := storage.NewShopTargets(db)
	goods := storage.NewShopGoods(db)
	notifies := storage.NewNotifications(db)
	cipher, err := crypto.NewCipher("shop-monitor-test-secret")
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}

	events := make(chan storage.NotificationEvent, 4)
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Event storage.NotificationEvent `json:"event"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode webhook payload: %v", err)
		} else {
			events <- payload.Event
		}
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer webhook.Close()

	configCipher, err := cipher.Encrypt(fmt.Sprintf(`{"url":%q}`, webhook.URL))
	if err != nil {
		t.Fatalf("encrypt webhook config: %v", err)
	}
	if err := notifies.CreateChannel(&storage.NotificationChannel{
		Name:          "global shop notifications",
		Type:          storage.NotifyWebhook,
		ConfigCipher:  configCipher,
		Subscriptions: `[{"events":["shop_stock_changed","shop_monitor_failed"]}]`,
		Enabled:       true,
	}); err != nil {
		t.Fatalf("create notification channel: %v", err)
	}

	dispatcher := notify.NewDispatcher(
		notifies,
		cipher,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		notify.Policy{SendMaxAttempts: 1},
	)
	rules := storage.NewShopWatchRules(db)
	if err := rules.Create(&storage.ShopWatchRule{
		TargetID:            0,
		Name:                "全局库存关注",
		Enabled:             true,
		EventsJSON:          `["stock_changed","monitor_failed"]`,
		ExcludeKeywordsJSON: `["屏蔽"]`,
	}); err != nil {
		t.Fatalf("create global watch rule: %v", err)
	}
	service := NewService(targets, rules, goods, dispatcher, nil, config.ProxyConfig{}, config.UpstreamConfig{})
	target := createRefreshTarget(t, targets, storage.ShopPlatform("global-notification-test"))
	target.NotifyEnabled = false // Historical per-shop setting must no longer gate global delivery.

	if err := service.dispatchChanges(context.Background(), target, &shopprovider.ShopInfo{Name: "全局店铺"}, []storage.ShopGoodsChangeLog{{
		TargetID:  target.ID,
		GoodsKey:  "goods-1",
		GoodsName: "商品一",
		Event:     storage.ShopChangeStockChanged,
		Summary:   "库存 0 -> 1",
	}}); err != nil {
		t.Fatalf("dispatch shop change: %v", err)
	}
	select {
	case event := <-events:
		if event != storage.EventShopStockChanged {
			t.Fatalf("first event = %q, want %q", event, storage.EventShopStockChanged)
		}
	case <-time.After(time.Second):
		t.Fatal("global shop change notification was not delivered")
	}

	service.notifyFailure(context.Background(), target, errors.New("upstream unavailable"))
	select {
	case event := <-events:
		if event != storage.EventShopMonitorFailed {
			t.Fatalf("second event = %q, want %q", event, storage.EventShopMonitorFailed)
		}
	case <-time.After(time.Second):
		t.Fatal("global shop failure notification was not delivered")
	}
}

func TestGlobalWatchRulesIgnoreLegacyPerShopRules(t *testing.T) {
	db := openShopMonitorTestDB(t)
	targets := storage.NewShopTargets(db)
	goods := storage.NewShopGoods(db)
	rules := storage.NewShopWatchRules(db)
	target := createRefreshTarget(t, targets, storage.ShopPlatform("legacy-rule-test"))
	service := NewService(targets, rules, goods, nil, nil, config.ProxyConfig{}, config.UpstreamConfig{})
	change := storage.ShopGoodsChangeLog{
		TargetID:  target.ID,
		GoodsKey:  "goods-1",
		GoodsName: "商品一",
		Event:     storage.ShopChangeStockChanged,
	}

	if err := rules.Create(&storage.ShopWatchRule{
		TargetID:   target.ID,
		Name:       "历史店铺规则",
		Enabled:    true,
		EventsJSON: `["stock_changed"]`,
	}); err != nil {
		t.Fatalf("create legacy rule: %v", err)
	}
	globalRules, err := service.globalWatchRules()
	if err != nil {
		t.Fatalf("list global rules: %v", err)
	}
	if matched := service.filterGlobalWatchRuleChanges(globalRules, []storage.ShopGoodsChangeLog{change}); len(matched) != 0 {
		t.Fatalf("legacy per-shop rule must not match global dispatch: %#v", matched)
	}

	if err := rules.Create(&storage.ShopWatchRule{
		TargetID:   0,
		Name:       "全局规则",
		Enabled:    true,
		EventsJSON: `["stock_changed"]`,
	}); err != nil {
		t.Fatalf("create global rule: %v", err)
	}
	globalRules, err = service.globalWatchRules()
	if err != nil {
		t.Fatalf("list updated global rules: %v", err)
	}
	if matched := service.filterGlobalWatchRuleChanges(globalRules, []storage.ShopGoodsChangeLog{change}); len(matched) != 1 {
		t.Fatalf("global rule should match dispatch: %#v", matched)
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

func TestSyncSkipsDuplicateActiveTarget(t *testing.T) {
	platform := storage.ShopPlatform("duplicate-active-sync-test")
	provider := &blockingShopProvider{started: make(chan struct{}), release: make(chan struct{})}
	shopprovider.Register(platform, func() shopprovider.Provider { return provider })

	db := openShopMonitorTestDB(t)
	targets := storage.NewShopTargets(db)
	goods := storage.NewShopGoods(db)
	target := createRefreshTarget(t, targets, platform)
	monitor := NewService(targets, storage.NewShopWatchRules(db), goods, nil, nil, config.ProxyConfig{}, config.UpstreamConfig{})

	firstDone := make(chan error, 1)
	go func() {
		_, err := monitor.SyncByID(context.Background(), target.ID)
		firstDone <- err
	}()
	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("first sync did not reach upstream")
	}

	if _, err := monitor.SyncByID(context.Background(), target.ID); !errors.Is(err, ErrShopSyncAlreadyRunning) {
		t.Fatalf("duplicate sync error = %v, want ErrShopSyncAlreadyRunning", err)
	}
	close(provider.release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first sync: %v", err)
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

func TestSyncAllSkipsRemainingTargetsAfterUpstreamBlock(t *testing.T) {
	platform := storage.ShopPlatform("sync-all-circuit-test")
	provider := &batchBlockingShopProvider{infoCalls: map[string]int{}}
	shopprovider.Register(platform, func() shopprovider.Provider { return provider })

	db := openShopMonitorTestDB(t)
	targets := storage.NewShopTargets(db)
	goods := storage.NewShopGoods(db)
	for _, item := range []struct {
		name    string
		baseURL string
	}{
		{name: "blocked-1", baseURL: "https://blocked.example"},
		{name: "blocked-2", baseURL: "https://blocked.example"},
		{name: "blocked-3", baseURL: "https://blocked.example"},
		{name: "healthy", baseURL: "https://healthy.example"},
	} {
		target := &storage.ShopTarget{
			Name:           item.name,
			Platform:       platform,
			SiteURL:        item.baseURL + "/shop/TOKEN",
			BaseURL:        item.baseURL,
			Token:          "TOKEN",
			MonitorEnabled: true,
			ScopeMode:      storage.ShopScopeAll,
			GoodsTypesJSON: `["card"]`,
		}
		if err := targets.Create(target); err != nil {
			t.Fatalf("create target %s: %v", item.name, err)
		}
	}

	service := NewService(targets, storage.NewShopWatchRules(db), goods, nil, nil, config.ProxyConfig{}, config.UpstreamConfig{})
	result := service.SyncAllWithConcurrency(context.Background(), 4)
	if result.Total != 4 || result.Success != 1 || result.Failed != 1 || result.Skipped != 2 {
		t.Fatalf("unexpected sync result: %#v", result)
	}
	var skipped int
	for _, item := range result.Targets {
		if item.Skipped {
			skipped++
		}
	}
	if skipped != 2 {
		t.Fatalf("skipped targets = %d, want 2: %#v", skipped, result.Targets)
	}
	if got := provider.callCount("https://blocked.example"); got != 1 {
		t.Fatalf("blocked upstream calls = %d, want 1", got)
	}
	if got := provider.callCount("https://healthy.example"); got != 1 {
		t.Fatalf("healthy upstream calls = %d, want 1", got)
	}
}

func TestSyncAllReusesProviderAndCachedShopInfo(t *testing.T) {
	platform := storage.ShopPlatform("sync-cache-test")
	provider := &countingShopProvider{}
	var factoryCalls int
	shopprovider.Register(platform, func() shopprovider.Provider {
		factoryCalls++
		return provider
	})

	db := openShopMonitorTestDB(t)
	targets := storage.NewShopTargets(db)
	goods := storage.NewShopGoods(db)
	now := time.Now()
	for _, name := range []string{"cached-1", "cached-2"} {
		target := &storage.ShopTarget{
			Name:           name,
			Platform:       platform,
			SiteURL:        "https://shared.example/shop/" + name,
			BaseURL:        "https://shared.example",
			Token:          name,
			MonitorEnabled: true,
			ScopeMode:      storage.ShopScopeAll,
			GoodsTypesJSON: `["card"]`,
			LastShopName:   name,
			LastInfoAt:     &now,
		}
		if err := targets.Create(target); err != nil {
			t.Fatalf("create target %s: %v", name, err)
		}
	}

	service := NewService(targets, storage.NewShopWatchRules(db), goods, nil, nil, config.ProxyConfig{}, config.UpstreamConfig{
		ShopInfoTTLHours: 24,
	})
	result := service.SyncAllWithConcurrency(context.Background(), 2)
	if result.Success != 2 || result.Failed != 0 || result.Skipped != 0 {
		t.Fatalf("unexpected sync result: %#v", result)
	}
	if factoryCalls != 1 {
		t.Fatalf("provider factory calls = %d, want 1", factoryCalls)
	}
	infoCalls, goodsCalls := provider.counts()
	if infoCalls != 0 || goodsCalls != 2 {
		t.Fatalf("info calls = %d, goods calls = %d; want 0 and 2", infoCalls, goodsCalls)
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

type batchBlockingShopProvider struct {
	mu        sync.Mutex
	infoCalls map[string]int
}

type countingShopProvider struct {
	mu         sync.Mutex
	infoCalls  int
	goodsCalls int
}

func (p *countingShopProvider) Info(_ context.Context, target shopprovider.Target) (*shopprovider.ShopInfo, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.infoCalls++
	return &shopprovider.ShopInfo{Name: target.Name}, nil
}

func (p *countingShopProvider) Categories(context.Context, shopprovider.Target, shopprovider.CategoryRequest) ([]shopprovider.Category, error) {
	return nil, nil
}

func (p *countingShopProvider) Goods(context.Context, shopprovider.Target, shopprovider.GoodsRequest) (*shopprovider.GoodsPage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.goodsCalls++
	return &shopprovider.GoodsPage{}, nil
}

func (p *countingShopProvider) Price(context.Context, shopprovider.Target, shopprovider.PriceRequest) (*shopprovider.PriceResult, error) {
	return &shopprovider.PriceResult{}, nil
}

func (p *countingShopProvider) counts() (int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.infoCalls, p.goodsCalls
}

func (p *batchBlockingShopProvider) Info(_ context.Context, target shopprovider.Target) (*shopprovider.ShopInfo, error) {
	p.mu.Lock()
	p.infoCalls[target.BaseURL]++
	p.mu.Unlock()
	if target.BaseURL == "https://blocked.example" {
		return nil, &shopprovider.UpstreamBlockedError{Err: fmt.Errorf("upstream returned HTML")}
	}
	return &shopprovider.ShopInfo{Name: target.Name}, nil
}

func (p *batchBlockingShopProvider) Categories(context.Context, shopprovider.Target, shopprovider.CategoryRequest) ([]shopprovider.Category, error) {
	return nil, nil
}

func (p *batchBlockingShopProvider) Goods(context.Context, shopprovider.Target, shopprovider.GoodsRequest) (*shopprovider.GoodsPage, error) {
	return &shopprovider.GoodsPage{}, nil
}

func (p *batchBlockingShopProvider) Price(context.Context, shopprovider.Target, shopprovider.PriceRequest) (*shopprovider.PriceResult, error) {
	return &shopprovider.PriceResult{}, nil
}

func (p *batchBlockingShopProvider) callCount(baseURL string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.infoCalls[baseURL]
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
