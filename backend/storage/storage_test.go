package storage

import (
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
)

func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := Open(DBConfig{
		Driver:       DBDriverSQLite,
		Path:         filepath.Join(t.TempDir(), "test.db"),
		MaxOpenConns: 20,
		MaxIdleConns: 5,
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := AutoMigrate(db); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	return db
}

func TestAggregateBalanceTrend(t *testing.T) {
	db := openTestDB(t)
	rates := NewRates(db)

	now := time.Now().In(trendLocation)
	day0 := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, trendLocation)
	day1 := day0.AddDate(0, 0, -1)
	day2 := day0.AddDate(0, 0, -2)

	snapshots := []BalanceSnapshot{
		{ChannelID: 1, Balance: 10, SampledAt: day2.Add(9 * time.Hour)},
		{ChannelID: 1, Balance: 20, SampledAt: day2.Add(12 * time.Hour)},
		{ChannelID: 2, Balance: 5, SampledAt: day2.Add(10 * time.Hour)},
		{ChannelID: 1, Balance: 7, SampledAt: day1.Add(11 * time.Hour)},
		{ChannelID: 2, Balance: 3, SampledAt: day1.Add(18 * time.Hour)},
		{ChannelID: 2, Balance: 9, SampledAt: day0.Add(8 * time.Hour)},
		{ChannelID: 2, Balance: 11, SampledAt: day0.Add(22 * time.Hour)},
	}
	for _, snapshot := range snapshots {
		snapshot := snapshot
		if err := rates.AppendBalance(&snapshot); err != nil {
			t.Fatalf("append balance: %v", err)
		}
	}

	got, err := rates.AggregateBalanceTrend(3)
	if err != nil {
		t.Fatalf("aggregate balance trend: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 days, got %d", len(got))
	}

	want := []DailyAggregate{
		{Day: day2, Balance: 25},
		{Day: day1, Balance: 10},
		{Day: day0, Balance: 11},
	}
	for i := range want {
		if !got[i].Day.Equal(want[i].Day) {
			t.Fatalf("day %d mismatch: got %s want %s", i, got[i].Day, want[i].Day)
		}
		if got[i].Balance != want[i].Balance {
			t.Fatalf("balance %d mismatch: got %v want %v", i, got[i].Balance, want[i].Balance)
		}
	}
}

func TestShopSyncJobsLifecycle(t *testing.T) {
	db := openTestDB(t)
	jobs := NewShopSyncJobs(db)
	job := &ShopSyncJob{TargetID: 9, Status: ShopSyncJobQueued}
	if err := jobs.Create(job); err != nil {
		t.Fatalf("create job: %v", err)
	}
	active, err := jobs.FindActiveByTarget(9)
	if err != nil {
		t.Fatalf("find active job: %v", err)
	}
	if active == nil || active.ID != job.ID || active.Status != ShopSyncJobQueued {
		t.Fatalf("active job = %#v", active)
	}

	startedAt := time.Now().Add(-2 * time.Second)
	if err := jobs.MarkRunning(job.ID, startedAt); err != nil {
		t.Fatalf("mark running: %v", err)
	}
	finishedAt := time.Now()
	if err := jobs.Complete(job.ID, ShopSyncJobSucceeded, 3, 1, map[string]int{"stock_changed": 1}, "", startedAt, finishedAt); err != nil {
		t.Fatalf("complete job: %v", err)
	}
	latest, err := jobs.FindLatestByTarget(9)
	if err != nil {
		t.Fatalf("find latest job: %v", err)
	}
	if latest.Status != ShopSyncJobSucceeded || latest.GoodsCount != 3 || latest.ChangedCount != 1 || latest.DurationMS <= 0 {
		t.Fatalf("completed job = %#v", latest)
	}
	if active, err := jobs.FindActiveByTarget(9); err != nil || active != nil {
		t.Fatalf("active after completion = %#v, err = %v", active, err)
	}

	interrupted := &ShopSyncJob{TargetID: 9, Status: ShopSyncJobRunning}
	if err := jobs.Create(interrupted); err != nil {
		t.Fatalf("create interrupted job: %v", err)
	}
	if err := jobs.MarkInterrupted(); err != nil {
		t.Fatalf("mark interrupted: %v", err)
	}
	restored, err := jobs.FindByTargetAndID(9, interrupted.ID)
	if err != nil {
		t.Fatalf("find interrupted job: %v", err)
	}
	if restored.Status != ShopSyncJobFailed || restored.ErrorMessage != "服务重启前同步未完成" || restored.FinishedAt == nil {
		t.Fatalf("restored job = %#v", restored)
	}
}

func TestShopHistoryRetentionKeepsRecentAndActiveRecords(t *testing.T) {
	db := openTestDB(t)
	goods := NewShopGoods(db)
	jobs := NewShopSyncJobs(db)
	now := time.Now()

	changes := []ShopGoodsChangeLog{
		{TargetID: 1, GoodsKey: "old-stock", Event: ShopChangeStockChanged, Summary: "old stock", ChangedAt: now.AddDate(0, 0, -20)},
		{TargetID: 1, Event: ShopChangeMonitorFailed, Summary: "old failure", ChangedAt: now.AddDate(0, 0, -20)},
		{TargetID: 1, GoodsKey: "old-price", Event: ShopChangePriceChanged, Summary: "old price", ChangedAt: now.AddDate(0, 0, -100)},
		{TargetID: 1, GoodsKey: "recent-stock", Event: ShopChangeStockChanged, Summary: "recent stock", ChangedAt: now.AddDate(0, 0, -5)},
		{TargetID: 1, GoodsKey: "recent-price", Event: ShopChangePriceChanged, Summary: "recent price", ChangedAt: now.AddDate(0, 0, -30)},
	}
	for i := range changes {
		if err := goods.AppendChange(&changes[i]); err != nil {
			t.Fatalf("append change: %v", err)
		}
	}

	frequentEvents := []ShopGoodsChangeEvent{ShopChangeStockChanged, ShopChangeMonitorFailed}
	deleted, err := goods.DeleteChangesBefore(now.AddDate(0, 0, -15), frequentEvents)
	if err != nil || deleted != 2 {
		t.Fatalf("delete frequent changes = %d, err = %v", deleted, err)
	}
	deleted, err = goods.DeleteChangesBeforeExcluding(now.AddDate(0, 0, -90), frequentEvents)
	if err != nil || deleted != 1 {
		t.Fatalf("delete other changes = %d, err = %v", deleted, err)
	}
	var remainingChanges int64
	if err := db.Model(&ShopGoodsChangeLog{}).Count(&remainingChanges).Error; err != nil {
		t.Fatalf("count remaining changes: %v", err)
	}
	if remainingChanges != 2 {
		t.Fatalf("remaining changes = %d", remainingChanges)
	}

	for _, startedAt := range []time.Time{now.AddDate(0, 0, -31), now.AddDate(0, 0, -1)} {
		if err := goods.AppendMonitorLog(&ShopMonitorLog{TargetID: 1, Success: true, StartedAt: startedAt, FinishedAt: startedAt.Add(time.Second)}); err != nil {
			t.Fatalf("append shop monitor log: %v", err)
		}
	}
	deleted, err = goods.DeleteMonitorLogsBefore(now.AddDate(0, 0, -30))
	if err != nil || deleted != 1 {
		t.Fatalf("delete shop monitor logs = %d, err = %v", deleted, err)
	}

	oldFinishedAt := now.AddDate(0, 0, -31)
	recentFinishedAt := now.AddDate(0, 0, -1)
	oldFinished := &ShopSyncJob{TargetID: 1, Status: ShopSyncJobSucceeded, FinishedAt: &oldFinishedAt, CreatedAt: oldFinishedAt}
	recentFinished := &ShopSyncJob{TargetID: 1, Status: ShopSyncJobSucceeded, FinishedAt: &recentFinishedAt, CreatedAt: recentFinishedAt}
	oldActive := &ShopSyncJob{TargetID: 1, Status: ShopSyncJobQueued, FinishedAt: &oldFinishedAt, CreatedAt: oldFinishedAt}
	for _, job := range []*ShopSyncJob{oldFinished, recentFinished, oldActive} {
		if err := jobs.Create(job); err != nil {
			t.Fatalf("create sync job: %v", err)
		}
	}
	deleted, err = jobs.DeleteFinishedBefore(now.AddDate(0, 0, -30))
	if err != nil || deleted != 1 {
		t.Fatalf("delete finished sync jobs = %d, err = %v", deleted, err)
	}
	var remainingJobs int64
	if err := db.Model(&ShopSyncJob{}).Count(&remainingJobs).Error; err != nil {
		t.Fatalf("count remaining jobs: %v", err)
	}
	if remainingJobs != 2 {
		t.Fatalf("remaining jobs = %d", remainingJobs)
	}
}

func TestChannelProxyEnabledPersists(t *testing.T) {
	db := openTestDB(t)
	channels := NewChannels(db)
	ch := &Channel{
		Name:           "proxy-channel",
		Type:           ChannelTypeNewAPI,
		SiteURL:        "https://example.com",
		Username:       "u",
		PasswordCipher: "x",
		ProxyEnabled:   true,
		MonitorEnabled: true,
	}
	if err := channels.Create(ch); err != nil {
		t.Fatalf("create channel: %v", err)
	}
	got, err := channels.FindByID(ch.ID)
	if err != nil {
		t.Fatalf("find channel: %v", err)
	}
	if !got.ProxyEnabled {
		t.Fatal("proxy_enabled = false, want true")
	}
}

func TestAutoGroupPolicyAllowsMultipleTargetsPerChannel(t *testing.T) {
	db := openTestDB(t)
	channels := NewChannels(db)
	ch := &Channel{
		Name:           "multi-target",
		Type:           ChannelTypeSub2API,
		SiteURL:        "https://example.com",
		Username:       "u",
		PasswordCipher: "x",
		MonitorEnabled: true,
	}
	if err := channels.Create(ch); err != nil {
		t.Fatalf("create channel: %v", err)
	}

	repo := NewAutoGroups(db)
	if err := repo.CreatePolicy(&AutoGroupPolicy{ChannelID: ch.ID, Name: "auto", TargetKeyName: "auto", ProbeKeyName: "probe-a"}); err != nil {
		t.Fatalf("create auto policy: %v", err)
	}
	if err := repo.CreatePolicy(&AutoGroupPolicy{ChannelID: ch.ID, Name: "team", TargetKeyName: "team-a", ProbeKeyName: "probe-b"}); err != nil {
		t.Fatalf("create second target policy: %v", err)
	}
	if err := repo.CreatePolicy(&AutoGroupPolicy{ChannelID: ch.ID, Name: "dup", TargetKeyName: "auto", ProbeKeyName: "probe-c"}); err == nil {
		t.Fatalf("duplicate target policy should fail")
	}

	list, err := repo.ListPoliciesByChannel(ch.ID)
	if err != nil {
		t.Fatalf("list policies by channel: %v", err)
	}
	if len(list) != 2 || list[0].TargetKeyName != "auto" || list[1].TargetKeyName != "team-a" {
		t.Fatalf("unexpected policy list: %#v", list)
	}
	defaultPolicy, err := repo.FindPolicyByChannel(ch.ID)
	if err != nil {
		t.Fatalf("find default policy: %v", err)
	}
	if defaultPolicy.TargetKeyName != "auto" {
		t.Fatalf("default policy = %q, want auto", defaultPolicy.TargetKeyName)
	}
}

func TestAutoGroupPolicyReorderPersistsOrder(t *testing.T) {
	db := openTestDB(t)
	channels := NewChannels(db)
	ch := &Channel{
		Name:           "sortable",
		Type:           ChannelTypeSub2API,
		SiteURL:        "https://example.com",
		Username:       "u",
		PasswordCipher: "x",
		MonitorEnabled: true,
	}
	if err := channels.Create(ch); err != nil {
		t.Fatalf("create channel: %v", err)
	}

	repo := NewAutoGroups(db)
	first := &AutoGroupPolicy{ChannelID: ch.ID, Name: "first", TargetKeyName: "first", ProbeKeyName: "probe-a", Enabled: true}
	second := &AutoGroupPolicy{ChannelID: ch.ID, Name: "second", TargetKeyName: "second", ProbeKeyName: "probe-b", Enabled: true}
	third := &AutoGroupPolicy{ChannelID: ch.ID, Name: "third", TargetKeyName: "third", ProbeKeyName: "probe-c", Enabled: true}
	for _, policy := range []*AutoGroupPolicy{first, second, third} {
		if err := repo.CreatePolicy(policy); err != nil {
			t.Fatalf("create policy %s: %v", policy.Name, err)
		}
	}

	if err := repo.ReorderPolicies([]uint{third.ID, first.ID, second.ID}); err != nil {
		t.Fatalf("reorder policies: %v", err)
	}
	list, err := repo.ListPolicies()
	if err != nil {
		t.Fatalf("list policies: %v", err)
	}
	if len(list) < 3 || list[0].ID != third.ID || list[1].ID != first.ID || list[2].ID != second.ID {
		t.Fatalf("unexpected list order: %#v", list)
	}
	enabled, err := repo.ListEnabledPolicies()
	if err != nil {
		t.Fatalf("list enabled policies: %v", err)
	}
	if len(enabled) < 3 || enabled[0].ID != third.ID || enabled[1].ID != first.ID || enabled[2].ID != second.ID {
		t.Fatalf("unexpected enabled order: %#v", enabled)
	}
}

func TestProxyEnabledPersistsForCaptchaAndNotification(t *testing.T) {
	db := openTestDB(t)

	captchas := NewCaptchas(db)
	cfg := &CaptchaConfig{
		Name:         "solver-proxy",
		Type:         CaptchaCapSolver,
		APIKeyCipher: "x",
		Enabled:      true,
		ProxyEnabled: true,
	}
	if err := captchas.Create(cfg); err != nil {
		t.Fatalf("create captcha: %v", err)
	}
	gotCaptcha, err := captchas.FindByID(cfg.ID)
	if err != nil {
		t.Fatalf("find captcha: %v", err)
	}
	if !gotCaptcha.ProxyEnabled {
		t.Fatal("captcha proxy_enabled = false, want true")
	}

	notifies := NewNotifications(db)
	notify := &NotificationChannel{
		Name:         "notify-proxy",
		Type:         NotifyTelegram,
		ConfigCipher: "x",
		Enabled:      true,
		ProxyEnabled: true,
	}
	if err := notifies.CreateChannel(notify); err != nil {
		t.Fatalf("create notification: %v", err)
	}
	gotNotify, err := notifies.FindChannel(notify.ID)
	if err != nil {
		t.Fatalf("find notification: %v", err)
	}
	if !gotNotify.ProxyEnabled {
		t.Fatal("notification proxy_enabled = false, want true")
	}
}

func TestShopTargetSyncResultUsesNicknameAsName(t *testing.T) {
	db := openTestDB(t)
	targets := NewShopTargets(db)
	target := &ShopTarget{
		Name:           "7FCVUA4X",
		Platform:       ShopPlatformLDXP,
		SiteURL:        "https://pay.ldxp.cn/shop/7FCVUA4X",
		BaseURL:        "https://pay.ldxp.cn",
		Token:          "7FCVUA4X",
		MonitorEnabled: true,
		ScopeMode:      ShopScopeAll,
	}
	if err := targets.Create(target); err != nil {
		t.Fatalf("create target: %v", err)
	}

	now := time.Now()
	if err := targets.SetSyncResult(target.ID, &now, "", "全网最低Team", 15, 7, 0); err != nil {
		t.Fatalf("set sync result: %v", err)
	}
	got, err := targets.FindByID(target.ID)
	if err != nil {
		t.Fatalf("find target: %v", err)
	}
	if got.Name != "全网最低Team" || got.LastShopName != "全网最低Team" {
		t.Fatalf("target name mismatch: name=%q last=%q", got.Name, got.LastShopName)
	}
	if got.LastGoodsCount != 15 || got.LastLowStockGoods != 7 {
		t.Fatalf("sync counts mismatch: %#v", got)
	}
}

func TestShopTargetSyncResultKeepsCountsWhenNicknameConflicts(t *testing.T) {
	db := openTestDB(t)
	targets := NewShopTargets(db)
	first := &ShopTarget{
		Name:           "重复店铺",
		Platform:       ShopPlatformLDXP,
		SiteURL:        "https://pay.ldxp.cn/shop/A",
		BaseURL:        "https://pay.ldxp.cn",
		Token:          "A",
		MonitorEnabled: true,
		ScopeMode:      ShopScopeAll,
	}
	second := &ShopTarget{
		Name:           "B",
		Platform:       ShopPlatformLDXP,
		SiteURL:        "https://pay.ldxp.cn/shop/B",
		BaseURL:        "https://pay.ldxp.cn",
		Token:          "B",
		MonitorEnabled: true,
		ScopeMode:      ShopScopeAll,
	}
	if err := targets.Create(first); err != nil {
		t.Fatalf("create first target: %v", err)
	}
	if err := targets.Create(second); err != nil {
		t.Fatalf("create second target: %v", err)
	}

	now := time.Now()
	if err := targets.SetSyncResult(second.ID, &now, "", "重复店铺", 9, 1, 2); err != nil {
		t.Fatalf("set sync result with duplicate nickname: %v", err)
	}
	got, err := targets.FindByID(second.ID)
	if err != nil {
		t.Fatalf("find second target: %v", err)
	}
	if got.Name != "B" {
		t.Fatalf("conflicting name should stay unchanged, got %q", got.Name)
	}
	if got.LastShopName != "重复店铺" || got.LastGoodsCount != 9 || got.LastChangedCount != 2 {
		t.Fatalf("sync result should still persist: %#v", got)
	}
}

func TestShopTargetsUpdateSortOrders(t *testing.T) {
	db := openTestDB(t)
	targets := NewShopTargets(db)
	items := []*ShopTarget{
		{Name: "shop-a", Platform: ShopPlatformLDXP, SiteURL: "https://example.invalid/shop/A", BaseURL: "https://example.invalid", Token: "A", MonitorEnabled: true, ScopeMode: ShopScopeAll, SortOrder: 1},
		{Name: "shop-b", Platform: ShopPlatformLDXP, SiteURL: "https://example.invalid/shop/B", BaseURL: "https://example.invalid", Token: "B", MonitorEnabled: true, ScopeMode: ShopScopeAll, SortOrder: 2},
		{Name: "shop-c", Platform: ShopPlatformLDXP, SiteURL: "https://example.invalid/shop/C", BaseURL: "https://example.invalid", Token: "C", MonitorEnabled: true, ScopeMode: ShopScopeAll, SortOrder: 3},
	}
	for _, item := range items {
		if err := targets.Create(item); err != nil {
			t.Fatalf("create %s: %v", item.Name, err)
		}
	}
	if err := targets.UpdateSortOrders(map[uint]int{
		items[0].ID: 30,
		items[1].ID: 10,
		items[2].ID: 20,
	}); err != nil {
		t.Fatalf("update sort orders: %v", err)
	}
	list, err := targets.List()
	if err != nil {
		t.Fatalf("list targets: %v", err)
	}
	if len(list) != 3 || list[0].Name != "shop-b" || list[1].Name != "shop-c" || list[2].Name != "shop-a" {
		t.Fatalf("unexpected order: %#v", list)
	}
}

func TestShopTargetsCreateAssignsNextSortOrder(t *testing.T) {
	db := openTestDB(t)
	targets := NewShopTargets(db)
	first := &ShopTarget{
		Name:           "shop-a",
		Platform:       ShopPlatformLDXP,
		SiteURL:        "https://example.invalid/shop/A",
		BaseURL:        "https://example.invalid",
		Token:          "A",
		MonitorEnabled: true,
		ScopeMode:      ShopScopeAll,
		SortOrder:      7,
	}
	second := &ShopTarget{
		Name:           "shop-b",
		Platform:       ShopPlatformLDXP,
		SiteURL:        "https://example.invalid/shop/B",
		BaseURL:        "https://example.invalid",
		Token:          "B",
		MonitorEnabled: true,
		ScopeMode:      ShopScopeAll,
	}
	if err := targets.Create(first); err != nil {
		t.Fatalf("create first target: %v", err)
	}
	if err := targets.Create(second); err != nil {
		t.Fatalf("create second target: %v", err)
	}

	if second.SortOrder != 8 {
		t.Fatalf("second sort_order = %d, want 8", second.SortOrder)
	}
	list, err := targets.List()
	if err != nil {
		t.Fatalf("list targets: %v", err)
	}
	if len(list) != 2 || list[0].Name != "shop-a" || list[1].Name != "shop-b" {
		t.Fatalf("unexpected order after create: %#v", list)
	}
}

func TestShopWatchRuleMatchesSnapshotAndChange(t *testing.T) {
	rule := ShopWatchRule{
		Enabled:           true,
		GoodsKeysJSON:     `["sku-1"]`,
		CategoryNamesJSON: `["会员"]`,
		KeywordsJSON:      `["Claude"]`,
		EventsJSON:        `["stock_low","goods_restocked"]`,
		StockThreshold:    3,
	}
	snapshot := &ShopGoodsSnapshot{
		GoodsKey:     "sku-2",
		Name:         "Claude Pro 月卡",
		CategoryName: "AI 会员",
		StockCount:   2,
	}

	if !ShopWatchRuleMatchesChange(rule, ShopChangeStockLow, snapshot, snapshot.GoodsKey, snapshot.Name) {
		t.Fatalf("watch rule should match keyword and stock threshold")
	}
	snapshot.StockCount = 4
	if ShopWatchRuleMatchesChange(rule, ShopChangeStockLow, snapshot, snapshot.GoodsKey, snapshot.Name) {
		t.Fatalf("watch rule should not match stock_low above threshold")
	}
	if ShopWatchRuleMatchesChange(rule, ShopChangePriceChanged, snapshot, snapshot.GoodsKey, snapshot.Name) {
		t.Fatalf("watch rule should not match events outside configured set")
	}
}

func TestShopWatchRulesPreviewAndDeleteWithTarget(t *testing.T) {
	db := openTestDB(t)
	targets := NewShopTargets(db)
	rules := NewShopWatchRules(db)
	goods := NewShopGoods(db)

	target := &ShopTarget{
		Name:           "watch-shop",
		Platform:       ShopPlatformLDXP,
		SiteURL:        "https://pay.ldxp.cn/shop/watch",
		BaseURL:        "https://pay.ldxp.cn",
		Token:          "watch",
		MonitorEnabled: true,
		NotifyEnabled:  true,
		ScopeMode:      ShopScopeAll,
	}
	if err := targets.Create(target); err != nil {
		t.Fatalf("create target: %v", err)
	}
	snapshots := []ShopGoodsSnapshot{
		{TargetID: target.ID, GoodsKey: "a", GoodsType: "card", Name: "重点套餐", CategoryID: 1, CategoryName: "A", StockCount: 1, FirstSeenAt: time.Now(), LastSeenAt: time.Now()},
		{TargetID: target.ID, GoodsKey: "b", GoodsType: "card", Name: "普通套餐", CategoryID: 2, CategoryName: "B", StockCount: 5, FirstSeenAt: time.Now(), LastSeenAt: time.Now()},
	}
	for i := range snapshots {
		if err := goods.CreateSnapshot(&snapshots[i]); err != nil {
			t.Fatalf("create snapshot: %v", err)
		}
	}
	rule := &ShopWatchRule{
		TargetID:       target.ID,
		Name:           "重点商品",
		Enabled:        true,
		KeywordsJSON:   `["重点"]`,
		EventsJSON:     `["stock_changed"]`,
		StockThreshold: 1,
	}
	if err := rules.Create(rule); err != nil {
		t.Fatalf("create rule: %v", err)
	}
	preview, total, err := goods.ListFirstMatchingWatchRule(*rule, 10)
	if err != nil {
		t.Fatalf("preview rule: %v", err)
	}
	if total != 1 || len(preview) != 1 || preview[0].GoodsKey != "a" {
		t.Fatalf("preview = total %d rows %#v, want goods a", total, preview)
	}
	if err := targets.Delete(target.ID); err != nil {
		t.Fatalf("delete target: %v", err)
	}
	list, err := rules.ListByTarget(target.ID)
	if err != nil {
		t.Fatalf("list rules after delete: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("rules after target delete = %d, want 0", len(list))
	}
}

func TestShopGoodsSnapshotCategoriesAndFilters(t *testing.T) {
	db := openTestDB(t)
	goods := NewShopGoods(db)
	now := time.Now()
	removedAt := now.Add(time.Hour)

	snapshots := []ShopGoodsSnapshot{
		{
			TargetID:     1,
			GoodsKey:     "gpt-live",
			GoodsType:    "card",
			Name:         "GPT Pro",
			CategoryID:   10,
			CategoryName: "GPT",
			Price:        1.2,
			StockCount:   0,
			FirstSeenAt:  now,
			LastSeenAt:   now,
		},
		{
			TargetID:     1,
			GoodsKey:     "gpt-old",
			GoodsType:    "card",
			Name:         "GPT Old",
			CategoryID:   10,
			CategoryName: "GPT",
			Price:        1.2,
			StockCount:   8,
			FirstSeenAt:  now,
			LastSeenAt:   now,
			RemovedAt:    &removedAt,
		},
		{
			TargetID:     1,
			GoodsKey:     "k12-live",
			GoodsType:    "card",
			Name:         "K12 Pack",
			CategoryID:   20,
			CategoryName: "K12",
			Price:        3.4,
			StockCount:   5,
			FirstSeenAt:  now,
			LastSeenAt:   now,
		},
		{
			TargetID:    1,
			GoodsKey:    "uncat-live",
			GoodsType:   "card",
			Name:        "No Category",
			Price:       2.3,
			StockCount:  1,
			FirstSeenAt: now,
			LastSeenAt:  now,
		},
		{
			TargetID:     2,
			GoodsKey:     "other-shop",
			GoodsType:    "card",
			Name:         "Other Shop",
			CategoryID:   10,
			CategoryName: "GPT",
			Price:        9.9,
			StockCount:   0,
			FirstSeenAt:  now,
			LastSeenAt:   now,
		},
	}
	for _, snapshot := range snapshots {
		snapshot := snapshot
		if err := goods.CreateSnapshot(&snapshot); err != nil {
			t.Fatalf("create snapshot %s: %v", snapshot.GoodsKey, err)
		}
	}

	categories, err := goods.SnapshotCategories(1, 1)
	if err != nil {
		t.Fatalf("snapshot categories: %v", err)
	}
	byName := map[string]ShopSnapshotCategory{}
	for _, category := range categories {
		byName[category.CategoryName] = category
	}
	if len(byName) != 3 {
		t.Fatalf("category count = %d, want 3: %#v", len(byName), categories)
	}
	if got := byName["GPT"]; got.GoodsCount != 2 || got.ActiveCount != 1 || got.RemovedCount != 1 || got.LowStockCount != 1 || got.OutOfStockCount != 1 {
		t.Fatalf("GPT category mismatch: %#v", got)
	}
	if got := byName["K12"]; got.GoodsCount != 1 || got.ActiveCount != 1 || got.LowStockCount != 0 {
		t.Fatalf("K12 category mismatch: %#v", got)
	}
	if got := byName[""]; got.GoodsCount != 1 || got.CategoryID != 0 || got.LowStockCount != 1 {
		t.Fatalf("uncategorized mismatch: %#v", got)
	}

	categoryID := int64(10)
	activeGPT, total, err := goods.ListPageFiltered(1, 1, 20, ShopGoodsFilter{CategoryID: &categoryID, Status: "active", StockThreshold: 1})
	if err != nil {
		t.Fatalf("list active GPT: %v", err)
	}
	if total != 1 || len(activeGPT) != 1 || activeGPT[0].GoodsKey != "gpt-live" {
		t.Fatalf("active GPT mismatch: total=%d list=%#v", total, activeGPT)
	}

	lowStock, total, err := goods.ListPageFiltered(1, 1, 20, ShopGoodsFilter{Status: "low_stock", StockThreshold: 1})
	if err != nil {
		t.Fatalf("list low stock: %v", err)
	}
	if total != 2 || len(lowStock) != 2 {
		t.Fatalf("low stock mismatch: total=%d list=%#v", total, lowStock)
	}

	inStock, total, err := goods.ListPageFiltered(1, 1, 20, ShopGoodsFilter{Status: "in_stock"})
	if err != nil {
		t.Fatalf("list in stock: %v", err)
	}
	if total != 2 || len(inStock) != 2 {
		t.Fatalf("in stock mismatch: total=%d list=%#v", total, inStock)
	}
	for _, item := range inStock {
		if item.StockCount <= 0 || item.RemovedAt != nil {
			t.Fatalf("in stock item should be active with stock > 0: %#v", item)
		}
	}

	removed, total, err := goods.ListPageFiltered(1, 1, 20, ShopGoodsFilter{Status: "removed"})
	if err != nil {
		t.Fatalf("list removed: %v", err)
	}
	if total != 1 || len(removed) != 1 || removed[0].GoodsKey != "gpt-old" {
		t.Fatalf("removed mismatch: total=%d list=%#v", total, removed)
	}

	keyword, total, err := goods.ListPageFiltered(1, 1, 20, ShopGoodsFilter{Keyword: "Pack"})
	if err != nil {
		t.Fatalf("list keyword: %v", err)
	}
	if total != 1 || len(keyword) != 1 || keyword[0].GoodsKey != "k12-live" {
		t.Fatalf("keyword mismatch: total=%d list=%#v", total, keyword)
	}

	defaultSorted, total, err := goods.ListPageFiltered(1, 1, 20, ShopGoodsFilter{})
	if err != nil {
		t.Fatalf("list default sort: %v", err)
	}
	if total != 4 || len(defaultSorted) != 4 || defaultSorted[0].GoodsKey != "uncat-live" {
		t.Fatalf("default sort should use category/name, total=%d list=%#v", total, defaultSorted)
	}

	stockDesc, total, err := goods.ListPageFiltered(1, 1, 20, ShopGoodsFilter{Sort: "stock_desc"})
	if err != nil {
		t.Fatalf("list stock desc: %v", err)
	}
	if total != 4 || len(stockDesc) != 4 || stockDesc[0].GoodsKey != "k12-live" {
		t.Fatalf("stock desc sort mismatch: total=%d list=%#v", total, stockDesc)
	}
}

func TestShopGoodsListAllPageFilteredIncludesTargetAndFilters(t *testing.T) {
	db := openTestDB(t)
	targets := NewShopTargets(db)
	goods := NewShopGoods(db)
	first := &ShopTarget{
		Name:           "shop-a",
		Platform:       ShopPlatformLDXP,
		SiteURL:        "https://example.invalid/shop/A",
		BaseURL:        "https://example.invalid",
		Token:          "A",
		MonitorEnabled: true,
		ScopeMode:      ShopScopeAll,
		StockThreshold: 2,
	}
	second := &ShopTarget{
		Name:           "shop-b",
		Platform:       ShopPlatformLDXP,
		SiteURL:        "https://example.invalid/shop/B",
		BaseURL:        "https://example.invalid",
		Token:          "B",
		MonitorEnabled: true,
		ScopeMode:      ShopScopeAll,
		StockThreshold: 5,
	}
	if err := targets.Create(first); err != nil {
		t.Fatalf("create first target: %v", err)
	}
	if err := targets.Create(second); err != nil {
		t.Fatalf("create second target: %v", err)
	}
	now := time.Now()
	rows := []ShopGoodsSnapshot{
		{TargetID: first.ID, GoodsKey: "a-low", GoodsType: "card", Name: "A Low", CategoryName: "GPT", Price: 1, StockCount: 2, FirstSeenAt: now, LastSeenAt: now},
		{TargetID: first.ID, GoodsKey: "a-ok", GoodsType: "card", Name: "A OK", CategoryName: "GPT", Price: 2, StockCount: 9, FirstSeenAt: now, LastSeenAt: now},
		{TargetID: second.ID, GoodsKey: "b-low", GoodsType: "card", Name: "B Low", CategoryName: "Claude", Price: 3, StockCount: 4, FirstSeenAt: now, LastSeenAt: now},
	}
	for i := range rows {
		if err := goods.CreateSnapshot(&rows[i]); err != nil {
			t.Fatalf("create snapshot: %v", err)
		}
	}

	all, total, err := goods.ListAllPageFiltered(1, 20, ShopGoodsFilter{Sort: "price_desc"})
	if err != nil {
		t.Fatalf("list all goods: %v", err)
	}
	if total != 3 || len(all) != 3 {
		t.Fatalf("all goods total=%d len=%d", total, len(all))
	}
	if all[0].TargetName == "" || all[0].TargetSiteURL == "" {
		t.Fatalf("target metadata missing: %#v", all[0])
	}
	if all[0].GoodsKey != "b-low" || all[1].GoodsKey != "a-ok" || all[2].GoodsKey != "a-low" {
		t.Fatalf("price_desc should sort across shops, got %q, %q, %q", all[0].GoodsKey, all[1].GoodsKey, all[2].GoodsKey)
	}

	oneShop, total, err := goods.ListAllPageFiltered(1, 20, ShopGoodsFilter{TargetID: first.ID})
	if err != nil {
		t.Fatalf("list first target goods: %v", err)
	}
	if total != 2 || len(oneShop) != 2 {
		t.Fatalf("first target total=%d len=%d", total, len(oneShop))
	}
	for _, row := range oneShop {
		if row.TargetID != first.ID {
			t.Fatalf("unexpected target in filtered rows: %#v", oneShop)
		}
	}

	lowStock, total, err := goods.ListAllPageFiltered(1, 20, ShopGoodsFilter{Status: "low_stock"})
	if err != nil {
		t.Fatalf("list low stock goods: %v", err)
	}
	if total != 2 || len(lowStock) != 2 {
		t.Fatalf("low stock total=%d len=%d rows=%#v", total, len(lowStock), lowStock)
	}
}

func TestAggregateBalanceTrendFillsMissingDays(t *testing.T) {
	db := openTestDB(t)
	rates := NewRates(db)

	now := time.Now().In(trendLocation)
	day0 := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, trendLocation)
	day1 := day0.AddDate(0, 0, -1)
	day2 := day0.AddDate(0, 0, -2)

	snapshots := []BalanceSnapshot{
		{ChannelID: 1, Balance: 10, SampledAt: day2.Add(9 * time.Hour)},
		{ChannelID: 1, Balance: 20, SampledAt: day0.Add(12 * time.Hour)},
	}
	for _, snapshot := range snapshots {
		snapshot := snapshot
		if err := rates.AppendBalance(&snapshot); err != nil {
			t.Fatalf("append balance: %v", err)
		}
	}

	got, err := rates.AggregateBalanceTrend(3)
	if err != nil {
		t.Fatalf("aggregate balance trend: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 days, got %d", len(got))
	}

	want := []DailyAggregate{
		{Day: day2, Balance: 10},
		{Day: day1, Balance: 0},
		{Day: day0, Balance: 20},
	}
	for i := range want {
		if !got[i].Day.Equal(want[i].Day) {
			t.Fatalf("day %d mismatch: got %s want %s", i, got[i].Day, want[i].Day)
		}
		if got[i].Balance != want[i].Balance {
			t.Fatalf("balance %d mismatch: got %v want %v", i, got[i].Balance, want[i].Balance)
		}
	}
}

func TestAggregateCostTrend(t *testing.T) {
	db := openTestDB(t)
	rates := NewRates(db)

	now := time.Now().In(trendLocation)
	day0 := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, trendLocation)
	day1 := day0.AddDate(0, 0, -1)
	day2 := day0.AddDate(0, 0, -2)

	snapshots := []CostSnapshot{
		{ChannelID: 1, TodayCost: 1.1, SampledAt: day2.Add(9 * time.Hour)},
		{ChannelID: 1, TodayCost: 2.2, SampledAt: day2.Add(18 * time.Hour)},
		{ChannelID: 2, TodayCost: 0.8, SampledAt: day2.Add(10 * time.Hour)},
		{ChannelID: 1, TodayCost: 3.5, SampledAt: day1.Add(11 * time.Hour)},
		{ChannelID: 2, TodayCost: 1.2, SampledAt: day1.Add(13 * time.Hour)},
		{ChannelID: 2, TodayCost: 1.8, SampledAt: day1.Add(22 * time.Hour)},
		{ChannelID: 1, TodayCost: 4.0, SampledAt: day0.Add(8 * time.Hour)},
		{ChannelID: 2, TodayCost: 2.5, SampledAt: day0.Add(21 * time.Hour)},
	}
	for _, snapshot := range snapshots {
		snapshot := snapshot
		if err := rates.AppendCost(&snapshot); err != nil {
			t.Fatalf("append cost: %v", err)
		}
	}

	got, err := rates.AggregateCostTrend(3)
	if err != nil {
		t.Fatalf("aggregate cost trend: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 days, got %d", len(got))
	}

	want := []DailyCostAggregate{
		{Day: day2, Cost: 3.0},
		{Day: day1, Cost: 5.3},
		{Day: day0, Cost: 6.5},
	}
	for i := range want {
		if !got[i].Day.Equal(want[i].Day) {
			t.Fatalf("day %d mismatch: got %s want %s", i, got[i].Day, want[i].Day)
		}
		if got[i].Cost != want[i].Cost {
			t.Fatalf("cost %d mismatch: got %v want %v", i, got[i].Cost, want[i].Cost)
		}
	}
}

func TestAggregateCostTrendFillsMissingDays(t *testing.T) {
	db := openTestDB(t)
	rates := NewRates(db)

	now := time.Now().In(trendLocation)
	day0 := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, trendLocation)
	day1 := day0.AddDate(0, 0, -1)
	day2 := day0.AddDate(0, 0, -2)

	snapshots := []CostSnapshot{
		{ChannelID: 1, TodayCost: 1.5, SampledAt: day2.Add(9 * time.Hour)},
		{ChannelID: 1, TodayCost: 2.5, SampledAt: day0.Add(12 * time.Hour)},
	}
	for _, snapshot := range snapshots {
		snapshot := snapshot
		if err := rates.AppendCost(&snapshot); err != nil {
			t.Fatalf("append cost: %v", err)
		}
	}

	got, err := rates.AggregateCostTrend(3)
	if err != nil {
		t.Fatalf("aggregate cost trend: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 days, got %d", len(got))
	}

	want := []DailyCostAggregate{
		{Day: day2, Cost: 1.5},
		{Day: day1, Cost: 0},
		{Day: day0, Cost: 2.5},
	}
	for i := range want {
		if !got[i].Day.Equal(want[i].Day) {
			t.Fatalf("day %d mismatch: got %s want %s", i, got[i].Day, want[i].Day)
		}
		if got[i].Cost != want[i].Cost {
			t.Fatalf("cost %d mismatch: got %v want %v", i, got[i].Cost, want[i].Cost)
		}
	}
}

func TestAggregateTrendUsesShanghaiDayBoundary(t *testing.T) {
	oldNow := trendNow
	trendNow = func() time.Time {
		return time.Date(2026, 6, 19, 16, 30, 0, 0, time.UTC)
	}
	t.Cleanup(func() { trendNow = oldNow })

	db := openTestDB(t)
	rates := NewRates(db)

	day0 := time.Date(2026, 6, 20, 0, 0, 0, 0, trendLocation)
	day1 := day0.AddDate(0, 0, -1)

	balanceSnapshots := []BalanceSnapshot{
		{ChannelID: 1, Balance: 10, SampledAt: time.Date(2026, 6, 19, 15, 59, 0, 0, time.UTC)},
		{ChannelID: 1, Balance: 20, SampledAt: time.Date(2026, 6, 19, 16, 1, 0, 0, time.UTC)},
	}
	for _, snapshot := range balanceSnapshots {
		snapshot := snapshot
		if err := rates.AppendBalance(&snapshot); err != nil {
			t.Fatalf("append balance: %v", err)
		}
	}

	costSnapshots := []CostSnapshot{
		{ChannelID: 1, TodayCost: 1.5, SampledAt: time.Date(2026, 6, 19, 15, 59, 0, 0, time.UTC)},
		{ChannelID: 1, TodayCost: 2.5, SampledAt: time.Date(2026, 6, 19, 16, 1, 0, 0, time.UTC)},
	}
	for _, snapshot := range costSnapshots {
		snapshot := snapshot
		if err := rates.AppendCost(&snapshot); err != nil {
			t.Fatalf("append cost: %v", err)
		}
	}

	balances, err := rates.AggregateBalanceTrend(2)
	if err != nil {
		t.Fatalf("aggregate balance trend: %v", err)
	}
	if len(balances) != 2 {
		t.Fatalf("balance days = %d, want 2", len(balances))
	}
	if !balances[0].Day.Equal(day1) || balances[0].Balance != 10 {
		t.Fatalf("previous shanghai day = %#v, want day %s balance 10", balances[0], day1)
	}
	if !balances[1].Day.Equal(day0) || balances[1].Balance != 20 {
		t.Fatalf("current shanghai day = %#v, want day %s balance 20", balances[1], day0)
	}

	costs, err := rates.AggregateCostTrend(2)
	if err != nil {
		t.Fatalf("aggregate cost trend: %v", err)
	}
	if len(costs) != 2 {
		t.Fatalf("cost days = %d, want 2", len(costs))
	}
	if !costs[0].Day.Equal(day1) || costs[0].Cost != 1.5 {
		t.Fatalf("previous shanghai day cost = %#v, want day %s cost 1.5", costs[0], day1)
	}
	if !costs[1].Day.Equal(day0) || costs[1].Cost != 2.5 {
		t.Fatalf("current shanghai day cost = %#v, want day %s cost 2.5", costs[1], day0)
	}
}

func TestDeleteCostSnapshotsBefore(t *testing.T) {
	db := openTestDB(t)
	rates := NewRates(db)

	now := time.Now()
	oldSnapshot := CostSnapshot{ChannelID: 1, TodayCost: 1.2, SampledAt: now.AddDate(0, 0, -10)}
	newSnapshot := CostSnapshot{ChannelID: 1, TodayCost: 2.3, SampledAt: now.AddDate(0, 0, -2)}
	if err := rates.AppendCost(&oldSnapshot); err != nil {
		t.Fatalf("append old cost: %v", err)
	}
	if err := rates.AppendCost(&newSnapshot); err != nil {
		t.Fatalf("append new cost: %v", err)
	}

	deleted, err := rates.DeleteCostSnapshotsBefore(now.AddDate(0, 0, -5))
	if err != nil {
		t.Fatalf("delete cost snapshots: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}

	var count int64
	if err := db.Model(&CostSnapshot{}).Count(&count).Error; err != nil {
		t.Fatalf("count cost snapshots: %v", err)
	}
	if count != 1 {
		t.Fatalf("remaining count = %d, want 1", count)
	}
}

func TestTryClaimCooldown(t *testing.T) {
	db := openTestDB(t)
	notifications := NewNotifications(db)

	ok, err := notifications.TryClaimCooldown(1, EventBalanceLow, time.Minute)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if !ok {
		t.Fatal("first claim should succeed")
	}

	ok, err = notifications.TryClaimCooldown(1, EventBalanceLow, time.Minute)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if ok {
		t.Fatal("second claim should be blocked by cooldown")
	}

	oldTime := time.Now().Add(-2 * time.Minute)
	if err := db.Model(&NotificationCooldown{}).
		Where("channel_id = ? AND event = ?", 1, EventBalanceLow).
		Updates(map[string]any{
			"last_sent_at": oldTime,
			"updated_at":   oldTime,
		}).Error; err != nil {
		t.Fatalf("age cooldown: %v", err)
	}

	ok, err = notifications.TryClaimCooldown(1, EventBalanceLow, time.Minute)
	if err != nil {
		t.Fatalf("third claim: %v", err)
	}
	if !ok {
		t.Fatal("third claim should succeed after cooldown expires")
	}
}

func TestTryClaimCooldownConcurrent(t *testing.T) {
	db := openTestDB(t)
	notifications := NewNotifications(db)

	var claimed int32
	var wg sync.WaitGroup

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			ok, err := notifications.TryClaimCooldown(2, EventBalanceLow, time.Minute)
			if err != nil {
				t.Errorf("concurrent claim: %v", err)
				return
			}
			if ok {
				atomic.AddInt32(&claimed, 1)
			}
		}()
	}
	wg.Wait()

	if claimed != 1 {
		t.Fatalf("expected exactly one successful claim, got %d", claimed)
	}
}

func TestUpstreamAnnouncementsSyncDedupes(t *testing.T) {
	db := openTestDB(t)
	announcements := NewUpstreamAnnouncements(db)

	now := time.Now()
	items := []UpstreamAnnouncement{
		{SourceKey: "a", Title: "A", Content: "one", FirstSeenAt: now},
		{SourceKey: "a", Title: "A2", Content: "dup", FirstSeenAt: now.Add(time.Second)},
	}
	newItems, err := announcements.Sync(1, items)
	if err != nil {
		t.Fatalf("sync announcements: %v", err)
	}
	if len(newItems) != 1 {
		t.Fatalf("new items = %d, want 1", len(newItems))
	}

	exists, err := announcements.Exists(1, "a")
	if err != nil {
		t.Fatalf("exists: %v", err)
	}
	if !exists {
		t.Fatal("expected announcement to exist")
	}
}

func TestUpstreamAnnouncementsListLatest(t *testing.T) {
	db := openTestDB(t)
	announcements := NewUpstreamAnnouncements(db)

	now := time.Now()
	publishedOld := now.Add(-3 * time.Hour)
	publishedNew := now.Add(-1 * time.Hour)
	items := []UpstreamAnnouncement{
		{ChannelID: 1, SourceKey: "a", Content: "body", PublishedAt: &publishedOld, FirstSeenAt: now.Add(3 * time.Minute)},
		{ChannelID: 1, SourceKey: "b", Content: "body", PublishedAt: &publishedNew, FirstSeenAt: now.Add(1 * time.Minute)},
		{ChannelID: 1, SourceKey: "c", Content: "body", FirstSeenAt: now.Add(4 * time.Minute)},
	}
	for _, item := range items {
		item := item
		if err := db.Create(&item).Error; err != nil {
			t.Fatalf("create announcement: %v", err)
		}
	}

	list, err := announcements.ListLatest(2)
	if err != nil {
		t.Fatalf("list latest: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("list len = %d, want 2", len(list))
	}
	if list[0].SourceKey != "b" || list[1].SourceKey != "a" {
		t.Fatalf("unexpected order: %#v", list)
	}
}

func TestUpstreamAnnouncementsDeleteByChannel(t *testing.T) {
	db := openTestDB(t)
	announcements := NewUpstreamAnnouncements(db)

	now := time.Now()
	if _, err := announcements.Sync(1, []UpstreamAnnouncement{{
		SourceKey:   "a",
		Content:     "one",
		FirstSeenAt: now,
	}}); err != nil {
		t.Fatalf("sync announcements: %v", err)
	}
	if _, err := announcements.Sync(2, []UpstreamAnnouncement{{
		SourceKey:   "b",
		Content:     "two",
		FirstSeenAt: now,
	}}); err != nil {
		t.Fatalf("sync announcements: %v", err)
	}

	rows, err := announcements.DeleteByChannel(1)
	if err != nil {
		t.Fatalf("delete by channel: %v", err)
	}
	if rows != 1 {
		t.Fatalf("rows = %d, want 1", rows)
	}
	list, total, err := announcements.ListPage(1, 10)
	if err != nil {
		t.Fatalf("list page: %v", err)
	}
	if total != 1 || len(list) != 1 || list[0].ChannelID != 2 {
		t.Fatalf("unexpected remaining announcements: total=%d list=%#v", total, list)
	}
}

func TestUpstreamAnnouncementsDeleteBefore(t *testing.T) {
	db := openTestDB(t)
	announcements := NewUpstreamAnnouncements(db)

	oldTime := time.Now().AddDate(0, 0, -10)
	newTime := time.Now()
	if _, err := announcements.Sync(1, []UpstreamAnnouncement{{
		SourceKey:   "old",
		Content:     "old",
		FirstSeenAt: oldTime,
	}}); err != nil {
		t.Fatalf("sync announcements: %v", err)
	}
	if _, err := announcements.Sync(1, []UpstreamAnnouncement{{
		SourceKey:   "new",
		Content:     "new",
		FirstSeenAt: newTime,
	}}); err != nil {
		t.Fatalf("sync announcements: %v", err)
	}

	rows, err := announcements.DeleteBefore(time.Now().AddDate(0, 0, -5))
	if err != nil {
		t.Fatalf("delete before: %v", err)
	}
	if rows != 1 {
		t.Fatalf("rows = %d, want 1", rows)
	}
	list, total, err := announcements.ListPage(1, 10)
	if err != nil {
		t.Fatalf("list page: %v", err)
	}
	if total != 1 || len(list) != 1 || list[0].SourceKey != "new" {
		t.Fatalf("unexpected remaining announcements: total=%d list=%#v", total, list)
	}
}

func TestUpdateCosts(t *testing.T) {
	db := openTestDB(t)
	channels := NewChannels(db)

	c := &Channel{
		Name:           "test",
		Type:           ChannelTypeNewAPI,
		SiteURL:        "https://example.com",
		Username:       "u",
		PasswordCipher: "x",
		MonitorEnabled: true,
	}
	if err := channels.Create(c); err != nil {
		t.Fatalf("create channel: %v", err)
	}

	if err := channels.UpdateCosts(c.ID, 1.23, 9.87); err != nil {
		t.Fatalf("update costs: %v", err)
	}

	got, err := channels.FindByID(c.ID)
	if err != nil {
		t.Fatalf("find channel: %v", err)
	}
	if got.TodayCost == nil || *got.TodayCost != 1.23 {
		t.Fatalf("today cost mismatch: %#v", got.TodayCost)
	}
	if got.TotalCost == nil || *got.TotalCost != 9.87 {
		t.Fatalf("total cost mismatch: %#v", got.TotalCost)
	}
}

func TestHardDeleteAllowsReusingNames(t *testing.T) {
	db := openTestDB(t)

	channels := NewChannels(db)
	ch := &Channel{
		Name:           "demo",
		Type:           ChannelTypeNewAPI,
		SiteURL:        "https://example.com",
		Username:       "u",
		PasswordCipher: "x",
		MonitorEnabled: true,
	}
	if err := channels.Create(ch); err != nil {
		t.Fatalf("create channel: %v", err)
	}
	if err := channels.Delete(ch.ID); err != nil {
		t.Fatalf("delete channel: %v", err)
	}
	ch = &Channel{
		Name:           "demo",
		Type:           ChannelTypeNewAPI,
		SiteURL:        "https://example.com",
		Username:       "u",
		PasswordCipher: "x",
		MonitorEnabled: true,
	}
	if err := channels.Create(ch); err != nil {
		t.Fatalf("recreate channel: %v", err)
	}

	captchas := NewCaptchas(db)
	cfg := &CaptchaConfig{
		Name:         "solver",
		Type:         CaptchaCapSolver,
		APIKeyCipher: "x",
		Enabled:      true,
	}
	if err := captchas.Create(cfg); err != nil {
		t.Fatalf("create captcha: %v", err)
	}
	if err := captchas.Delete(cfg.ID); err != nil {
		t.Fatalf("delete captcha: %v", err)
	}
	cfg = &CaptchaConfig{
		Name:         "solver",
		Type:         CaptchaCapSolver,
		APIKeyCipher: "x",
		Enabled:      true,
	}
	if err := captchas.Create(cfg); err != nil {
		t.Fatalf("recreate captcha: %v", err)
	}

	notifications := NewNotifications(db)
	notify := &NotificationChannel{
		Name:         "telegram",
		Type:         NotifyTelegram,
		ConfigCipher: "x",
		Enabled:      true,
	}
	if err := notifications.CreateChannel(notify); err != nil {
		t.Fatalf("create notification channel: %v", err)
	}
	if err := notifications.DeleteChannel(notify.ID); err != nil {
		t.Fatalf("delete notification channel: %v", err)
	}
	notify = &NotificationChannel{
		Name:         "telegram",
		Type:         NotifyTelegram,
		ConfigCipher: "x",
		Enabled:      true,
	}
	if err := notifications.CreateChannel(notify); err != nil {
		t.Fatalf("recreate notification channel: %v", err)
	}
}

func TestDeleteChannelCleansScopedState(t *testing.T) {
	db := openTestDB(t)

	channels := NewChannels(db)
	ch := &Channel{
		Name:           "demo",
		Type:           ChannelTypeSub2API,
		SiteURL:        "https://example.com",
		Username:       "u",
		PasswordCipher: "x",
		MonitorEnabled: true,
	}
	if err := channels.Create(ch); err != nil {
		t.Fatalf("create channel: %v", err)
	}

	now := time.Now()
	if err := db.Create(&AuthSession{ChannelID: ch.ID}).Error; err != nil {
		t.Fatalf("create auth session: %v", err)
	}
	if err := db.Create(&RateSnapshot{ChannelID: ch.ID, ModelName: "old", Ratio: 1, LastSeenAt: now}).Error; err != nil {
		t.Fatalf("create rate snapshot: %v", err)
	}
	if err := db.Create(&RateChangeLog{ChannelID: ch.ID, ModelName: "old", NewRatio: 1, ChangedAt: now}).Error; err != nil {
		t.Fatalf("create rate change: %v", err)
	}
	if err := db.Create(&BalanceSnapshot{ChannelID: ch.ID, Balance: 1, SampledAt: now}).Error; err != nil {
		t.Fatalf("create balance snapshot: %v", err)
	}
	if err := db.Create(&CostSnapshot{ChannelID: ch.ID, TodayCost: 1, SampledAt: now}).Error; err != nil {
		t.Fatalf("create cost snapshot: %v", err)
	}
	if err := db.Create(&MonitorLog{ChannelID: ch.ID, Job: MonitorJobBalance, Success: true, StartedAt: now, FinishedAt: now}).Error; err != nil {
		t.Fatalf("create monitor log: %v", err)
	}
	if err := db.Create(&NotificationCooldown{ChannelID: ch.ID, Event: EventBalanceLow, LastSentAt: now}).Error; err != nil {
		t.Fatalf("create cooldown: %v", err)
	}
	if err := db.Create(&NotificationLog{ChannelID: 99, UpstreamChannelID: ch.ID, Event: EventBalanceLow, Subject: "alert", Success: true, SentAt: now}).Error; err != nil {
		t.Fatalf("create notification log: %v", err)
	}
	if err := db.Create(&NotificationLog{ChannelID: 99, Event: EventBalanceLow, Subject: "demo 余额低于阈值", Success: true, SentAt: now}).Error; err != nil {
		t.Fatalf("create legacy notification log: %v", err)
	}
	if err := db.Create(&UpstreamAnnouncement{ChannelID: ch.ID, SourceKey: "a", Content: "deleted", FirstSeenAt: now}).Error; err != nil {
		t.Fatalf("create announcement: %v", err)
	}

	if err := channels.Delete(ch.ID); err != nil {
		t.Fatalf("delete channel: %v", err)
	}

	for _, tt := range []struct {
		name  string
		model any
	}{
		{"auth_sessions", &AuthSession{}},
		{"rate_snapshots", &RateSnapshot{}},
		{"rate_change_logs", &RateChangeLog{}},
		{"balance_snapshots", &BalanceSnapshot{}},
		{"cost_snapshots", &CostSnapshot{}},
		{"monitor_logs", &MonitorLog{}},
		{"notification_cooldowns", &NotificationCooldown{}},
		{"upstream_announcements", &UpstreamAnnouncement{}},
		{"notification_logs", &NotificationLog{}},
	} {
		var count int64
		q := db.Model(tt.model).Where("channel_id = ?", ch.ID)
		if tt.name == "notification_logs" {
			q = db.Model(tt.model).Where("upstream_channel_id = ? OR subject LIKE ?", ch.ID, "%"+ch.Name+"%")
		}
		if err := q.Count(&count).Error; err != nil {
			t.Fatalf("count %s: %v", tt.name, err)
		}
		if count != 0 {
			t.Fatalf("%s count = %d, want 0", tt.name, count)
		}
	}
}

func TestAutoMigrateDropsDeletedAtColumns(t *testing.T) {
	db := openTestDB(t)

	for _, ddl := range []string{
		"ALTER TABLE channels ADD COLUMN deleted_at datetime",
		"ALTER TABLE captcha_configs ADD COLUMN deleted_at datetime",
		"ALTER TABLE notification_channels ADD COLUMN deleted_at datetime",
		"CREATE INDEX idx_channels_deleted_at ON channels(deleted_at)",
		"CREATE INDEX idx_captcha_configs_deleted_at ON captcha_configs(deleted_at)",
		"CREATE INDEX idx_notification_channels_deleted_at ON notification_channels(deleted_at)",
	} {
		if err := db.Exec(ddl).Error; err != nil {
			t.Fatalf("exec %q: %v", ddl, err)
		}
	}

	now := time.Now()
	activeChannel := &Channel{
		Name:           "active-channel",
		Type:           ChannelTypeNewAPI,
		SiteURL:        "https://example.com",
		Username:       "u",
		PasswordCipher: "x",
		MonitorEnabled: true,
	}
	deletedChannel := &Channel{
		Name:           "deleted-channel",
		Type:           ChannelTypeNewAPI,
		SiteURL:        "https://example.com",
		Username:       "u",
		PasswordCipher: "x",
		MonitorEnabled: true,
	}
	if err := db.Create(activeChannel).Error; err != nil {
		t.Fatalf("create active channel: %v", err)
	}
	if err := db.Create(deletedChannel).Error; err != nil {
		t.Fatalf("create deleted channel: %v", err)
	}
	if err := db.Table("channels").Where("id = ?", deletedChannel.ID).Update("deleted_at", now).Error; err != nil {
		t.Fatalf("mark deleted channel: %v", err)
	}

	activeCaptcha := &CaptchaConfig{Name: "active-captcha", Type: CaptchaCapSolver, APIKeyCipher: "x", Enabled: true}
	deletedCaptcha := &CaptchaConfig{Name: "deleted-captcha", Type: CaptchaCapSolver, APIKeyCipher: "x", Enabled: true}
	if err := db.Create(activeCaptcha).Error; err != nil {
		t.Fatalf("create active captcha: %v", err)
	}
	if err := db.Create(deletedCaptcha).Error; err != nil {
		t.Fatalf("create deleted captcha: %v", err)
	}
	if err := db.Table("captcha_configs").Where("id = ?", deletedCaptcha.ID).Update("deleted_at", now).Error; err != nil {
		t.Fatalf("mark deleted captcha: %v", err)
	}

	activeNotify := &NotificationChannel{Name: "active-notify", Type: NotifyTelegram, ConfigCipher: "x", Enabled: true}
	deletedNotify := &NotificationChannel{Name: "deleted-notify", Type: NotifyTelegram, ConfigCipher: "x", Enabled: true}
	if err := db.Create(activeNotify).Error; err != nil {
		t.Fatalf("create active notification channel: %v", err)
	}
	if err := db.Create(deletedNotify).Error; err != nil {
		t.Fatalf("create deleted notification channel: %v", err)
	}
	if err := db.Table("notification_channels").Where("id = ?", deletedNotify.ID).Update("deleted_at", now).Error; err != nil {
		t.Fatalf("mark deleted notification channel: %v", err)
	}

	if err := AutoMigrate(db); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}

	for _, table := range []string{"channels", "captcha_configs", "notification_channels"} {
		hasColumn, err := tableHasColumn(db, table, "deleted_at")
		if err != nil {
			t.Fatalf("inspect %s.deleted_at: %v", table, err)
		}
		if hasColumn {
			t.Fatalf("%s.deleted_at still exists", table)
		}
	}

	var count int64
	if err := db.Model(&Channel{}).Count(&count).Error; err != nil {
		t.Fatalf("count channels: %v", err)
	}
	if count != 1 {
		t.Fatalf("channel count = %d, want 1", count)
	}
	if err := db.Model(&CaptchaConfig{}).Count(&count).Error; err != nil {
		t.Fatalf("count captchas: %v", err)
	}
	if count != 1 {
		t.Fatalf("captcha count = %d, want 1", count)
	}
	if err := db.Model(&NotificationChannel{}).Count(&count).Error; err != nil {
		t.Fatalf("count notification channels: %v", err)
	}
	if count != 1 {
		t.Fatalf("notification channel count = %d, want 1", count)
	}
}
