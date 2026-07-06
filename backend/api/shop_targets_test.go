package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/ifty-r/upstream-ops/backend/storage"
)

func TestBuildShopTargetPreservesNotifyEnabledWhenOmitted(t *testing.T) {
	current := &storage.ShopTarget{
		Name:          "shop",
		Platform:      storage.ShopPlatformLDXP,
		SiteURL:       "https://pay.ldxp.cn/shop/TOKEN",
		BaseURL:       "https://pay.ldxp.cn",
		Token:         "TOKEN",
		NotifyEnabled: true,
		GoodsSort:     "stock_asc",
	}
	next, err := buildShopTarget(shopTargetInput{
		Name:     current.Name,
		Platform: current.Platform,
		SiteURL:  current.SiteURL,
		BaseURL:  current.BaseURL,
		Token:    current.Token,
	}, current)
	if err != nil {
		t.Fatalf("build target: %v", err)
	}
	if !next.NotifyEnabled {
		t.Fatalf("notify_enabled = false, want preserved true")
	}
	if next.GoodsSort != "stock_asc" {
		t.Fatalf("goods_sort = %q, want preserved stock_asc", next.GoodsSort)
	}
}

func TestBuildShopTargetDefaultsNotifyEnabledForCreate(t *testing.T) {
	next, err := buildShopTarget(shopTargetInput{
		Name:     "shop",
		Platform: storage.ShopPlatformLDXP,
		SiteURL:  "https://pay.ldxp.cn/shop/TOKEN",
		BaseURL:  "https://pay.ldxp.cn",
		Token:    "TOKEN",
	}, nil)
	if err != nil {
		t.Fatalf("build target: %v", err)
	}
	if next.NotifyEnabled {
		t.Fatalf("notify_enabled = true, want default false")
	}
	if next.GoodsSort != "category" {
		t.Fatalf("goods_sort = %q, want category", next.GoodsSort)
	}
}

func TestBuildShopTargetAcceptsGoodsSort(t *testing.T) {
	next, err := buildShopTarget(shopTargetInput{
		Name:      "shop",
		Platform:  storage.ShopPlatformLDXP,
		SiteURL:   "https://pay.ldxp.cn/shop/TOKEN",
		BaseURL:   "https://pay.ldxp.cn",
		Token:     "TOKEN",
		GoodsSort: "stock_desc",
	}, nil)
	if err != nil {
		t.Fatalf("build target: %v", err)
	}
	if next.GoodsSort != "stock_desc" {
		t.Fatalf("goods_sort = %q, want stock_desc", next.GoodsSort)
	}
}

func TestBulkConfigureShopNotificationsUpsertsRules(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := openTestDB(t)
	targets := storage.NewShopTargets(db)
	rules := storage.NewShopWatchRules(db)
	goods := storage.NewShopGoods(db)

	first := &storage.ShopTarget{
		Name:           "shop-a",
		Platform:       storage.ShopPlatformLDXP,
		SiteURL:        "https://pay.ldxp.cn/shop/A",
		BaseURL:        "https://pay.ldxp.cn",
		Token:          "A",
		MonitorEnabled: true,
		NotifyEnabled:  false,
		ScopeMode:      storage.ShopScopeAll,
	}
	second := &storage.ShopTarget{
		Name:           "shop-b",
		Platform:       storage.ShopPlatformLDXP,
		SiteURL:        "https://pay.ldxp.cn/shop/B",
		BaseURL:        "https://pay.ldxp.cn",
		Token:          "B",
		MonitorEnabled: true,
		NotifyEnabled:  false,
		ScopeMode:      storage.ShopScopeAll,
	}
	if err := targets.Create(first); err != nil {
		t.Fatalf("create first target: %v", err)
	}
	if err := targets.Create(second); err != nil {
		t.Fatalf("create second target: %v", err)
	}
	if err := rules.Create(&storage.ShopWatchRule{
		TargetID:       first.ID,
		Name:           "重点商品",
		Enabled:        true,
		KeywordsJSON:   `["旧关键词"]`,
		EventsJSON:     `["stock_changed"]`,
		StockThreshold: 9,
	}); err != nil {
		t.Fatalf("create existing rule: %v", err)
	}

	router := gin.New()
	registerShopTargets(router.Group("/api"), &Deps{
		ShopTargets:    targets,
		ShopWatchRules: rules,
		ShopGoods:      goods,
	})

	body := `{
		"target_ids":[` + uintString(first.ID) + `,` + uintString(second.ID) + `],
		"notify_enabled":true,
		"upsert_rule":true,
		"replace_same_name":true,
		"rule":{
			"name":"重点商品",
			"enabled":true,
			"keywords":["新关键词"],
			"category_names":["会员"],
			"goods_keys":["sku-1"],
			"events":["stock_low","goods_restocked"],
			"stock_threshold":2
		}
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/shop-targets/bulk-notification", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data shopBulkNotificationResult `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Data.UpdatedTargets != 2 || resp.Data.UpdatedRules != 1 || resp.Data.CreatedRules != 1 {
		t.Fatalf("unexpected result: %#v", resp.Data)
	}

	gotFirst, err := targets.FindByID(first.ID)
	if err != nil {
		t.Fatalf("find first target: %v", err)
	}
	gotSecond, err := targets.FindByID(second.ID)
	if err != nil {
		t.Fatalf("find second target: %v", err)
	}
	if !gotFirst.NotifyEnabled || !gotSecond.NotifyEnabled {
		t.Fatalf("notify_enabled not updated: first=%v second=%v", gotFirst.NotifyEnabled, gotSecond.NotifyEnabled)
	}

	firstRules, err := rules.ListByTarget(first.ID)
	if err != nil {
		t.Fatalf("list first rules: %v", err)
	}
	secondRules, err := rules.ListByTarget(second.ID)
	if err != nil {
		t.Fatalf("list second rules: %v", err)
	}
	if len(firstRules) != 1 || len(secondRules) != 1 {
		t.Fatalf("unexpected rule counts: first=%d second=%d", len(firstRules), len(secondRules))
	}
	if firstRules[0].KeywordsJSON != `["新关键词"]` || firstRules[0].StockThreshold != 2 {
		t.Fatalf("existing rule was not replaced: %#v", firstRules[0])
	}
	if secondRules[0].Name != "重点商品" || secondRules[0].GoodsKeysJSON != `["sku-1"]` {
		t.Fatalf("new rule mismatch: %#v", secondRules[0])
	}
}

func uintString(id uint) string {
	return strconv.FormatUint(uint64(id), 10)
}

func TestBulkConfigureShopNotificationsValidatesTargetsBeforeUpdate(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := openTestDB(t)
	targets := storage.NewShopTargets(db)
	rules := storage.NewShopWatchRules(db)
	goods := storage.NewShopGoods(db)

	target := &storage.ShopTarget{
		Name:           "shop-a",
		Platform:       storage.ShopPlatformLDXP,
		SiteURL:        "https://pay.ldxp.cn/shop/A",
		BaseURL:        "https://pay.ldxp.cn",
		Token:          "A",
		MonitorEnabled: true,
		NotifyEnabled:  false,
		ScopeMode:      storage.ShopScopeAll,
	}
	if err := targets.Create(target); err != nil {
		t.Fatalf("create target: %v", err)
	}

	router := gin.New()
	registerShopTargets(router.Group("/api"), &Deps{
		ShopTargets:    targets,
		ShopWatchRules: rules,
		ShopGoods:      goods,
	})

	body := `{
		"target_ids":[` + uintString(target.ID) + `,999999],
		"notify_enabled":true
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/shop-targets/bulk-notification", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	got, err := targets.FindByID(target.ID)
	if err != nil {
		t.Fatalf("find target: %v", err)
	}
	if got.NotifyEnabled {
		t.Fatalf("notify_enabled changed before all targets were validated")
	}
}
