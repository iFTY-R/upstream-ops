package shopmonitor

import (
	"context"
	"errors"
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

func TestSyncCollectsDefaultChannelQuotesSerially(t *testing.T) {
	platform := storage.ShopPlatform("payment-quote-sync-test")
	provider := &paymentQuoteShopProvider{
		goods: []shopprovider.Goods{
			{GoodsKey: "limit-zero", GoodsType: "card", Name: "Zero", Price: 1, StockCount: 3, LimitCount: 0},
			{GoodsKey: "limit-one", GoodsType: "card", Name: "One", Price: 2, StockCount: 3, LimitCount: 1},
			{GoodsKey: "limit-five", GoodsType: "card", Name: "Five", Price: 3, StockCount: 10, LimitCount: 5},
		},
		channels: []shopprovider.PaymentChannel{
			{ID: 7, Name: "支付宝电脑收款", DisplayName: "支付宝", Rate: 3},
			{ID: 8, Name: "微信收款", DisplayName: "微信", Rate: 2},
		},
		prices: map[string]shopprovider.PriceResult{
			"limit-zero": {OriginalAmount: 1, Fee: 0.03, FeePayer: 1, TotalAmount: 1.03},
			"limit-one":  {OriginalAmount: 2, Fee: 0.06, FeePayer: 1, TotalAmount: 2.06},
			"limit-five": {OriginalAmount: 15, Fee: 0.45, FeePayer: 1, TotalAmount: 15.45},
		},
		priceErrors: map[string]error{},
	}
	shopprovider.Register(platform, func() shopprovider.Provider { return provider })

	db := openShopMonitorTestDB(t)
	targets := storage.NewShopTargets(db)
	goodsRepo := storage.NewShopGoods(db)
	target := createRefreshTarget(t, targets, platform)
	service := NewService(targets, storage.NewShopWatchRules(db), goodsRepo, nil, nil, config.ProxyConfig{}, config.UpstreamConfig{})
	result, err := service.SyncByID(context.Background(), target.ID)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if result.GoodsCount != 3 {
		t.Fatalf("goods count = %d, want 3", result.GoodsCount)
	}

	requests, maxActive := provider.priceRequestSnapshot()
	if len(requests) != 3 || maxActive != 1 {
		t.Fatalf("price requests = %#v, max active = %d", requests, maxActive)
	}
	wantKeys := []string{"limit-five", "limit-one", "limit-zero"}
	wantQuantities := map[string]int{"limit-zero": 1, "limit-one": 1, "limit-five": 5}
	for index, request := range requests {
		if request.GoodsKey != wantKeys[index] || request.ChannelID != 7 || request.Quantity != wantQuantities[request.GoodsKey] {
			t.Fatalf("price request[%d] = %#v", index, request)
		}
	}

	for key, quantity := range wantQuantities {
		snapshot, err := goodsRepo.FindSnapshot(target.ID, key)
		if err != nil {
			t.Fatalf("find snapshot %s: %v", key, err)
		}
		if snapshot == nil || snapshot.PaymentChannelID == nil || *snapshot.PaymentChannelID != 7 ||
			snapshot.PaymentChannelName == nil || *snapshot.PaymentChannelName != "支付宝" ||
			snapshot.PaymentQuoteQuantity == nil || *snapshot.PaymentQuoteQuantity != quantity ||
			snapshot.PaymentFee == nil || snapshot.PaymentFeePayer == nil || *snapshot.PaymentFeePayer != 1 ||
			snapshot.PaymentTotalAmount == nil || snapshot.PaymentQuotedAt == nil {
			t.Fatalf("snapshot quote %s = %#v", key, snapshot)
		}
	}
}

func TestQuoteFailuresClearStaleDataWithoutFailingSync(t *testing.T) {
	platform := storage.ShopPlatform("payment-quote-failure-test")
	provider := &paymentQuoteShopProvider{
		goods:       []shopprovider.Goods{{GoodsKey: "goods", GoodsType: "card", Name: "Goods", Price: 2, StockCount: 5, LimitCount: 1}},
		channels:    []shopprovider.PaymentChannel{{ID: 7, DisplayName: "支付宝", Rate: 3}},
		prices:      map[string]shopprovider.PriceResult{"goods": {OriginalAmount: 2, Fee: 0.06, FeePayer: 1, TotalAmount: 2.06}},
		priceErrors: map[string]error{},
	}
	shopprovider.Register(platform, func() shopprovider.Provider { return provider })

	db := openShopMonitorTestDB(t)
	targets := storage.NewShopTargets(db)
	goodsRepo := storage.NewShopGoods(db)
	target := createRefreshTarget(t, targets, platform)
	service := NewService(targets, storage.NewShopWatchRules(db), goodsRepo, nil, nil, config.ProxyConfig{}, config.UpstreamConfig{})
	if _, err := service.SyncByID(context.Background(), target.ID); err != nil {
		t.Fatalf("initial sync: %v", err)
	}

	provider.setPriceError("goods", errors.New("quote failed"))
	if _, err := service.SyncByID(context.Background(), target.ID); err != nil {
		t.Fatalf("sync with quote failure: %v", err)
	}
	assertPaymentQuoteCleared(t, findPaymentSnapshot(t, goodsRepo, target.ID, "goods"))

	provider.setPriceError("goods", nil)
	if _, err := service.SyncByID(context.Background(), target.ID); err != nil {
		t.Fatalf("restore quote: %v", err)
	}
	provider.setChannelError(errors.New("channels failed"))
	if _, err := service.SyncByID(context.Background(), target.ID); err != nil {
		t.Fatalf("sync with channel failure: %v", err)
	}
	assertPaymentQuoteCleared(t, findPaymentSnapshot(t, goodsRepo, target.ID, "goods"))

	provider.setChannelError(nil)
	if _, err := service.SyncByID(context.Background(), target.ID); err != nil {
		t.Fatalf("restore channel quote: %v", err)
	}
	provider.setPriceError("goods", errors.New("refresh quote failed"))
	refresh, err := service.RefreshGoodsByKey(context.Background(), target.ID, "goods")
	if err != nil {
		t.Fatalf("refresh with quote failure: %v", err)
	}
	assertPaymentQuoteCleared(t, refresh.Snapshot)
}

func TestCollectPaymentQuotesHonorsContextCancellation(t *testing.T) {
	provider := &paymentQuoteShopProvider{
		channels: []shopprovider.PaymentChannel{{ID: 7, DisplayName: "支付宝"}},
		prices:   map[string]shopprovider.PriceResult{},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	service := NewService(nil, nil, nil, nil, nil, config.ProxyConfig{}, config.UpstreamConfig{})
	err := service.collectPaymentQuotes(ctx, provider, shopprovider.Target{}, map[string]shopprovider.Goods{
		"goods": {GoodsKey: "goods"},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("collect quotes error = %v, want context canceled", err)
	}
}

func TestCollectPaymentQuotesReturnsCancellationFromPriceRequest(t *testing.T) {
	started := make(chan struct{})
	provider := &paymentQuoteShopProvider{
		channels:                 []shopprovider.PaymentChannel{{ID: 7, DisplayName: "支付宝"}},
		prices:                   map[string]shopprovider.PriceResult{},
		priceStarted:             started,
		waitForPriceCancellation: true,
	}
	ctx, cancel := context.WithCancel(context.Background())
	service := NewService(nil, nil, nil, nil, nil, config.ProxyConfig{}, config.UpstreamConfig{})
	errCh := make(chan error, 1)
	go func() {
		errCh <- service.collectPaymentQuotes(ctx, provider, shopprovider.Target{}, map[string]shopprovider.Goods{
			"goods": {GoodsKey: "goods"},
		})
	}()
	<-started
	cancel()

	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("collect quotes error = %v, want context canceled", err)
	}
}

func findPaymentSnapshot(t *testing.T, goods *storage.ShopGoods, targetID uint, goodsKey string) *storage.ShopGoodsSnapshot {
	t.Helper()
	snapshot, err := goods.FindSnapshot(targetID, goodsKey)
	if err != nil {
		t.Fatalf("find snapshot: %v", err)
	}
	if snapshot == nil {
		t.Fatal("snapshot is nil")
	}
	return snapshot
}

func assertPaymentQuoteCleared(t *testing.T, snapshot *storage.ShopGoodsSnapshot) {
	t.Helper()
	if snapshot.PaymentChannelID != nil || snapshot.PaymentChannelName != nil || snapshot.PaymentChannelRate != nil ||
		snapshot.PaymentQuoteQuantity != nil || snapshot.PaymentOriginalAmount != nil || snapshot.PaymentFee != nil ||
		snapshot.PaymentFeePayer != nil || snapshot.PaymentTotalAmount != nil || snapshot.PaymentQuotedAt != nil {
		t.Fatalf("payment quote was not cleared: %#v", snapshot)
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

type paymentQuoteShopProvider struct {
	mu                       sync.Mutex
	goods                    []shopprovider.Goods
	channels                 []shopprovider.PaymentChannel
	channelErr               error
	prices                   map[string]shopprovider.PriceResult
	priceErrors              map[string]error
	priceRequests            []shopprovider.PriceRequest
	activePrices             int
	maxActive                int
	priceStarted             chan struct{}
	waitForPriceCancellation bool
}

func (p *paymentQuoteShopProvider) Info(context.Context, shopprovider.Target) (*shopprovider.ShopInfo, error) {
	return &shopprovider.ShopInfo{Name: "payment shop"}, nil
}

func (p *paymentQuoteShopProvider) Categories(context.Context, shopprovider.Target, shopprovider.CategoryRequest) ([]shopprovider.Category, error) {
	return nil, nil
}

func (p *paymentQuoteShopProvider) Goods(context.Context, shopprovider.Target, shopprovider.GoodsRequest) (*shopprovider.GoodsPage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	list := append([]shopprovider.Goods(nil), p.goods...)
	return &shopprovider.GoodsPage{Total: len(list), List: list}, nil
}

func (p *paymentQuoteShopProvider) PaymentChannels(context.Context, shopprovider.Target) ([]shopprovider.PaymentChannel, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.channelErr != nil {
		return nil, p.channelErr
	}
	return append([]shopprovider.PaymentChannel(nil), p.channels...), nil
}

func (p *paymentQuoteShopProvider) Price(ctx context.Context, _ shopprovider.Target, request shopprovider.PriceRequest) (*shopprovider.PriceResult, error) {
	p.mu.Lock()
	p.priceRequests = append(p.priceRequests, request)
	p.activePrices++
	if p.activePrices > p.maxActive {
		p.maxActive = p.activePrices
	}
	result := p.prices[request.GoodsKey]
	err := p.priceErrors[request.GoodsKey]
	started := p.priceStarted
	waitForCancellation := p.waitForPriceCancellation
	p.mu.Unlock()
	if started != nil {
		close(started)
	}
	if waitForCancellation {
		<-ctx.Done()
		err = ctx.Err()
	} else {
		time.Sleep(time.Millisecond)
	}
	p.mu.Lock()
	p.activePrices--
	p.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (p *paymentQuoteShopProvider) setPriceError(goodsKey string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err == nil {
		delete(p.priceErrors, goodsKey)
		return
	}
	p.priceErrors[goodsKey] = err
}

func (p *paymentQuoteShopProvider) setChannelError(err error) {
	p.mu.Lock()
	p.channelErr = err
	p.mu.Unlock()
}

func (p *paymentQuoteShopProvider) priceRequestSnapshot() ([]shopprovider.PriceRequest, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]shopprovider.PriceRequest(nil), p.priceRequests...), p.maxActive
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
