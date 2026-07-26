package storage

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestPriceAIAutoMigrateCreatesTablesAndIndexes(t *testing.T) {
	db := openTestDB(t)

	for _, model := range []any{
		&PriceAIFeedState{},
		&PriceAIProduct{},
		&PriceAIWatchTarget{},
		&PriceAIPreset{},
		&PriceAIOffer{},
		&PriceAIOfferRanking{},
		&PriceAISyncLog{},
		&PriceAIRiskFeedback{},
		&PriceAILDXPTargetBinding{},
	} {
		if !db.Migrator().HasTable(model) {
			t.Fatalf("missing migrated table for %T", model)
		}
	}

	for _, item := range []struct {
		model any
		name  string
	}{
		{&PriceAIProduct{}, "uq_priceai_products_remote_id"},
		{&PriceAIProduct{}, "uq_priceai_products_slug"},
		{&PriceAIWatchTarget{}, "uq_priceai_watch_targets_product_id"},
		{&PriceAIPreset{}, "uq_priceai_presets_product_remote_id"},
		{&PriceAIOffer{}, "uq_priceai_offers_product_dedupe"},
		{&PriceAIOfferRanking{}, "uq_priceai_offer_rankings_membership"},
		{&PriceAIRiskFeedback{}, "uq_priceai_risk_feedback_subject"},
		{&PriceAILDXPTargetBinding{}, "uq_priceai_ldxp_target_identity"},
		{&PriceAILDXPTargetBinding{}, "uq_priceai_ldxp_target_shop_target"},
	} {
		if !db.Migrator().HasIndex(item.model, item.name) {
			t.Fatalf("missing index %s for %T", item.name, item.model)
		}
	}
}

func TestPriceAIUpsertsPreserveLogicalRecordAndRollbackTransaction(t *testing.T) {
	db := openTestDB(t)
	repo := NewPriceAI(db)
	now := time.Now().UTC().Truncate(time.Millisecond)
	firstPrice := 19.99
	product := newPriceAIProduct("product-1", "chatgpt-plus", now, "snapshot-1", &firstPrice)
	stored, err := repo.UpsertProduct(product)
	if err != nil {
		t.Fatalf("upsert first product: %v", err)
	}
	firstSeen := stored.FirstSeenAt

	updatedPrice := 18.99
	updated := newPriceAIProduct("product-1", "chatgpt-plus", now.Add(time.Hour), "snapshot-2", &updatedPrice)
	updated.Name = "ChatGPT Plus Updated"
	updated.FirstSeenAt = now.Add(time.Hour)
	stored, err = repo.UpsertProduct(updated)
	if err != nil {
		t.Fatalf("upsert updated product: %v", err)
	}
	if stored.ID == 0 || stored.Name != "ChatGPT Plus Updated" {
		t.Fatalf("unexpected stored product: %#v", stored)
	}
	if !stored.FirstSeenAt.Equal(firstSeen) {
		t.Fatalf("first_seen_at changed: got %s want %s", stored.FirstSeenAt, firstSeen)
	}
	var productCount int64
	if err := db.Model(&PriceAIProduct{}).Where("remote_id = ?", "product-1").Count(&productCount).Error; err != nil {
		t.Fatalf("count upserted product: %v", err)
	}
	if productCount != 1 {
		t.Fatalf("product count = %d, want 1", productCount)
	}

	err = repo.Transaction(func(tx *PriceAI) error {
		rollbackProduct := newPriceAIProduct("rollback-product", "rollback-product", now, "snapshot-rollback", &firstPrice)
		if _, err := tx.UpsertProduct(rollbackProduct); err != nil {
			return err
		}
		return errors.New("force rollback")
	})
	if err == nil {
		t.Fatal("transaction rollback returned nil error")
	}
	if err := db.Model(&PriceAIProduct{}).Where("remote_id = ?", "rollback-product").Count(&productCount).Error; err != nil {
		t.Fatalf("count rolled back product: %v", err)
	}
	if productCount != 0 {
		t.Fatalf("rolled back product count = %d, want 0", productCount)
	}
}

func TestPriceAIPruneCurrentBoardsKeepsCurrentRows(t *testing.T) {
	db := openTestDB(t)
	repo := NewPriceAI(db)
	now := time.Now().UTC().Truncate(time.Millisecond)
	price := 20.0
	product, err := repo.UpsertProduct(newPriceAIProduct("product-1", "chatgpt-plus", now, "snapshot-new", &price))
	if err != nil {
		t.Fatalf("upsert product: %v", err)
	}
	oldPreset := PriceAIPreset{ProductID: product.ID, RemoteID: "preset-old", Label: "Old", GeneratedAt: now, LastSnapshotID: "snapshot-old"}
	newPreset := PriceAIPreset{ProductID: product.ID, RemoteID: "preset-new", Label: "New", GeneratedAt: now, LastSnapshotID: "snapshot-new"}
	if err := db.Create(&oldPreset).Error; err != nil {
		t.Fatalf("create old preset: %v", err)
	}
	if err := db.Create(&newPreset).Error; err != nil {
		t.Fatalf("create new preset: %v", err)
	}
	oldOffer := PriceAIOffer{ProductID: product.ID, DedupeKey: "old", MerchantKey: "merchant-old", Title: "Old offer", NormalizedTitle: "old offer", Price: 20, URL: "https://merchant.example/old", LastSnapshotID: "snapshot-old", FirstSeenAt: now, LastSeenAt: now}
	newOffer := PriceAIOffer{ProductID: product.ID, DedupeKey: "new", MerchantKey: "merchant-new", Title: "New offer", NormalizedTitle: "new offer", Price: 18, URL: "https://merchant.example/new", LastSnapshotID: "snapshot-new", FirstSeenAt: now, LastSeenAt: now}
	if err := db.Create(&oldOffer).Error; err != nil {
		t.Fatalf("create old offer: %v", err)
	}
	if err := db.Create(&newOffer).Error; err != nil {
		t.Fatalf("create new offer: %v", err)
	}
	for _, ranking := range []PriceAIOfferRanking{
		{ProductID: product.ID, OfferID: oldOffer.ID, BoardKind: PriceAIBoardDefault, Rank: 1, BoardGeneratedAt: now, LastSnapshotID: "snapshot-old"},
		{ProductID: product.ID, OfferID: newOffer.ID, BoardKind: PriceAIBoardDefault, Rank: 1, BoardGeneratedAt: now, LastSnapshotID: "snapshot-new"},
	} {
		if err := db.Create(&ranking).Error; err != nil {
			t.Fatalf("create ranking: %v", err)
		}
	}
	pruned, err := repo.PruneCurrentBoards("snapshot-new")
	if err != nil {
		t.Fatalf("prune current boards: %v", err)
	}
	if pruned.RankingsDeleted != 1 || pruned.OffersDeleted != 1 || pruned.PresetsDeleted != 1 {
		t.Fatalf("unexpected prune result: %#v", pruned)
	}
	for _, item := range []struct {
		name  string
		model any
		want  int64
	}{
		{"rankings", &PriceAIOfferRanking{}, 1},
		{"offers", &PriceAIOffer{}, 1},
		{"presets", &PriceAIPreset{}, 1},
	} {
		var count int64
		if err := db.Model(item.model).Count(&count).Error; err != nil {
			t.Fatalf("count %s: %v", item.name, err)
		}
		if count != item.want {
			t.Fatalf("%s count = %d, want %d", item.name, count, item.want)
		}
	}
}

func TestPriceAIAutoMigrateDropsLegacyHistoryTables(t *testing.T) {
	db := openTestDB(t)
	repo := NewPriceAI(db)
	base := time.Date(2026, time.July, 26, 10, 30, 0, 0, time.UTC)

	for _, table := range []string{"priceai_product_history", "priceai_change_logs"} {
		if db.Migrator().HasTable(table) {
			t.Fatalf("legacy table %s unexpectedly exists before setup", table)
		}
		if err := db.Exec("CREATE TABLE " + table + " (id INTEGER PRIMARY KEY)").Error; err != nil {
			t.Fatalf("create legacy table %s: %v", table, err)
		}
		if !db.Migrator().HasTable(table) {
			t.Fatalf("legacy table %s was not created", table)
		}
	}

	seedSyncLog := func(jobKind PriceAISyncJobKind, seq int) {
		t.Helper()
		startedAt := base.Add(time.Duration(seq) * time.Minute)
		if err := db.Create(&PriceAISyncLog{
			JobKind:    jobKind,
			SnapshotID: fmt.Sprintf("%s-%d", jobKind, seq),
			Success:    seq%2 == 0,
			StartedAt:  startedAt,
			FinishedAt: startedAt.Add(3 * time.Second),
		}).Error; err != nil {
			t.Fatalf("seed %s sync log %d: %v", jobKind, seq, err)
		}
	}
	for i := 0; i < 7; i++ {
		seedSyncLog(PriceAISyncJobFeed, i)
	}
	for i := 0; i < 6; i++ {
		seedSyncLog(PriceAISyncJobRisk, i)
	}

	if err := AutoMigrate(db); err != nil {
		t.Fatalf("auto migrate with legacy tables: %v", err)
	}

	for _, table := range []string{"priceai_product_history", "priceai_change_logs"} {
		if db.Migrator().HasTable(table) {
			t.Fatalf("legacy table %s still exists after auto migrate", table)
		}
	}

	assertJobLogs := func(jobKind PriceAISyncJobKind, want []string) {
		t.Helper()
		logs, total, err := repo.ListSyncLogs(jobKind, 1, 10)
		if err != nil {
			t.Fatalf("list %s sync logs after migrate: %v", jobKind, err)
		}
		if total != int64(len(want)) || len(logs) != len(want) {
			t.Fatalf("%s sync log total=%d len=%d after migrate, want %d", jobKind, total, len(logs), len(want))
		}
		for i, snapshotID := range want {
			if logs[i].SnapshotID != snapshotID {
				t.Fatalf("%s sync log %d snapshot_id=%q after migrate, want %q", jobKind, i, logs[i].SnapshotID, snapshotID)
			}
		}
	}

	assertJobLogs(PriceAISyncJobFeed, []string{"feed-6", "feed-5", "feed-4", "feed-3", "feed-2"})
	assertJobLogs(PriceAISyncJobRisk, []string{"risk-5", "risk-4", "risk-3", "risk-2", "risk-1"})
}

func TestPriceAIAppendSyncLogKeepsFivePerJobKind(t *testing.T) {
	db := openTestDB(t)
	repo := NewPriceAI(db)
	base := time.Date(2026, time.July, 26, 11, 0, 0, 0, time.UTC)

	appendLog := func(jobKind PriceAISyncJobKind, seq int) {
		t.Helper()
		startedAt := base.Add(time.Duration(seq) * time.Minute)
		if err := repo.AppendSyncLog(&PriceAISyncLog{
			JobKind:    jobKind,
			SnapshotID: fmt.Sprintf("%s-%d", jobKind, seq),
			Success:    seq%2 == 0,
			StartedAt:  startedAt,
			FinishedAt: startedAt.Add(3 * time.Second),
		}); err != nil {
			t.Fatalf("append %s sync log %d: %v", jobKind, seq, err)
		}
	}

	for i := 0; i < 7; i++ {
		appendLog(PriceAISyncJobFeed, i)
	}
	for i := 0; i < 6; i++ {
		appendLog(PriceAISyncJobRisk, i)
	}

	assertJobLogs := func(jobKind PriceAISyncJobKind, want []string) {
		t.Helper()
		logs, total, err := repo.ListSyncLogs(jobKind, 1, 10)
		if err != nil {
			t.Fatalf("list %s sync logs: %v", jobKind, err)
		}
		if total != int64(len(want)) || len(logs) != len(want) {
			t.Fatalf("%s sync log total=%d len=%d, want %d", jobKind, total, len(logs), len(want))
		}
		for i, snapshotID := range want {
			if logs[i].SnapshotID != snapshotID {
				t.Fatalf("%s sync log %d snapshot_id=%q, want %q", jobKind, i, logs[i].SnapshotID, snapshotID)
			}
		}
	}

	assertJobLogs(PriceAISyncJobFeed, []string{"feed-6", "feed-5", "feed-4", "feed-3", "feed-2"})
	assertJobLogs(PriceAISyncJobRisk, []string{"risk-5", "risk-4", "risk-3", "risk-2", "risk-1"})

	logs, total, err := repo.ListSyncLogs("", 1, 20)
	if err != nil {
		t.Fatalf("list all sync logs: %v", err)
	}
	if total != 10 || len(logs) != 10 {
		t.Fatalf("all sync log total=%d len=%d, want 10", total, len(logs))
	}
}

func TestPriceAILDXPTargetBindingIsUniqueAndDeletedWithShopTarget(t *testing.T) {
	db := openTestDB(t)
	repo := NewPriceAI(db)
	targets := NewShopTargets(db)
	firstTarget := newPriceAITestShopTarget("priceai-managed-one", "token-one")
	secondTarget := newPriceAITestShopTarget("priceai-managed-two", "token-two")
	if err := targets.Create(firstTarget); err != nil {
		t.Fatalf("create first target: %v", err)
	}
	if err := targets.Create(secondTarget); err != nil {
		t.Fatalf("create second target: %v", err)
	}
	binding := &PriceAILDXPTargetBinding{
		Platform:     ShopPlatformLDXP,
		BaseURL:      "HTTPS://PAY.LDXP.CN/",
		Token:        "shared-token",
		ShopTargetID: firstTarget.ID,
	}
	if err := repo.CreateLDXPTargetBinding(binding); err != nil {
		t.Fatalf("create binding: %v", err)
	}
	if binding.BaseURL != "https://pay.ldxp.cn" {
		t.Fatalf("binding base URL was not normalized: %q", binding.BaseURL)
	}
	found, err := repo.FindLDXPTargetBinding(ShopPlatformLDXP, "https://pay.ldxp.cn/", "shared-token")
	if err != nil {
		t.Fatalf("find binding: %v", err)
	}
	if found == nil || found.ShopTargetID != firstTarget.ID {
		t.Fatalf("unexpected binding: %#v", found)
	}
	if err := repo.CreateLDXPTargetBinding(&PriceAILDXPTargetBinding{
		Platform:     ShopPlatformLDXP,
		BaseURL:      "https://pay.ldxp.cn",
		Token:        "shared-token",
		ShopTargetID: secondTarget.ID,
	}); err == nil {
		t.Fatal("duplicate shop identity binding unexpectedly succeeded")
	}
	if err := repo.CreateLDXPTargetBinding(&PriceAILDXPTargetBinding{
		Platform:     ShopPlatformLDXP,
		BaseURL:      "https://www.ldxp.cn",
		Token:        "different-token",
		ShopTargetID: firstTarget.ID,
	}); err == nil {
		t.Fatal("duplicate shop target binding unexpectedly succeeded")
	}

	if err := targets.Delete(firstTarget.ID); err != nil {
		t.Fatalf("delete shop target: %v", err)
	}
	found, err = repo.FindLDXPTargetBinding(ShopPlatformLDXP, "https://pay.ldxp.cn", "shared-token")
	if err != nil {
		t.Fatalf("find deleted binding: %v", err)
	}
	if found != nil {
		t.Fatalf("binding remained after target deletion: %#v", found)
	}
}

func TestShopTargetsTransactionWithPriceAIRollsBackBothRepositories(t *testing.T) {
	db := openTestDB(t)
	targets := NewShopTargets(db)
	priceAI := NewPriceAI(db)
	rollback := errors.New("force rollback")

	err := targets.TransactionWithPriceAI(priceAI, func(txTargets *ShopTargets, txPriceAI *PriceAI) error {
		target := newPriceAITestShopTarget("rolled-back-priceai-target", "rollback-token")
		if err := txTargets.Create(target); err != nil {
			return err
		}
		if err := txPriceAI.CreateLDXPTargetBinding(&PriceAILDXPTargetBinding{
			Platform:     ShopPlatformLDXP,
			BaseURL:      "https://pay.ldxp.cn",
			Token:        "rollback-token",
			ShopTargetID: target.ID,
		}); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("transaction error = %v, want rollback", err)
	}

	var targetCount, bindingCount int64
	if err := db.Model(&ShopTarget{}).Count(&targetCount).Error; err != nil {
		t.Fatalf("count shop targets: %v", err)
	}
	if err := db.Model(&PriceAILDXPTargetBinding{}).Count(&bindingCount).Error; err != nil {
		t.Fatalf("count bindings: %v", err)
	}
	if targetCount != 0 || bindingCount != 0 {
		t.Fatalf("rollback left target_count=%d binding_count=%d", targetCount, bindingCount)
	}
}

func newPriceAIProduct(remoteID, slug string, now time.Time, snapshotID string, price *float64) *PriceAIProduct {
	currency := "USD"
	return &PriceAIProduct{
		RemoteID:                   remoteID,
		Slug:                       slug,
		Name:                       "ChatGPT Plus",
		Platform:                   "chatgpt",
		ProductType:                "subscription",
		OfferCount:                 5,
		InStockCount:               4,
		LowestPrice:                price,
		LowestPriceCurrency:        &currency,
		LatestSeenAt:               now,
		ProductSnapshotGeneratedAt: now,
		LastSnapshotID:             snapshotID,
		FirstSeenAt:                now,
		LastSeenAt:                 now,
	}
}

func newPriceAITestShopTarget(name, token string) *ShopTarget {
	return &ShopTarget{
		Name:           name,
		Platform:       ShopPlatformLDXP,
		SiteURL:        "https://pay.ldxp.cn/shop/" + token,
		BaseURL:        "https://pay.ldxp.cn",
		Token:          token,
		MonitorEnabled: true,
		ScopeMode:      ShopScopeGoodsKeys,
	}
}
