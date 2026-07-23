package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ifty-r/upstream-ops/backend/shopmonitor"
	"github.com/ifty-r/upstream-ops/backend/storage"
	"gorm.io/gorm"
)

func TestFailShopUpstreamUsesFailedDependencyStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	failShopUpstream(c, errors.New("ldxp returned HTML"))

	if w.Code != http.StatusFailedDependency {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusFailedDependency)
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error != "店铺上游不可用：ldxp returned HTML" {
		t.Fatalf("error = %q", body.Error)
	}
}

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

func TestBuildShopTargetPreservesOptionalFlagsWhenOmitted(t *testing.T) {
	current := &storage.ShopTarget{
		Name:                "shop",
		Platform:            storage.ShopPlatformLDXP,
		SiteURL:             "https://pay.ldxp.cn/shop/TOKEN",
		BaseURL:             "https://pay.ldxp.cn",
		Token:               "TOKEN",
		MonitorEnabled:      false,
		PriceChangeEnabled:  false,
		StockChangeEnabled:  false,
		LowStockEnabled:     false,
		RestockEnabled:      false,
		NewGoodsEnabled:     false,
		RemovedGoodsEnabled: false,
		ProxyEnabled:        true,
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
	if next.MonitorEnabled || next.PriceChangeEnabled || next.StockChangeEnabled || next.LowStockEnabled || next.RestockEnabled || next.NewGoodsEnabled || next.RemovedGoodsEnabled {
		t.Fatalf("optional flags were re-enabled: %#v", next)
	}
	if !next.ProxyEnabled {
		t.Fatalf("proxy_enabled = false, want preserved true")
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

func TestBuildShopTargetNormalizesLDXPItemURL(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/shopApi/Shop/goodsInfo":
			_, _ = w.Write([]byte(strings.ReplaceAll(`{"code":1,"msg":"success","data":{"user":{"nickname":"测试店铺","token":"ITEMSHOP","link":"__SERVER__/shop/ITEMSHOP"}}}`, "__SERVER__", server.URL)))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	next, err := buildShopTarget(shopTargetInput{
		Name:     "shop",
		Platform: storage.ShopPlatformLDXP,
		SiteURL:  server.URL + "/item/9l814h",
	}, nil)
	if err != nil {
		t.Fatalf("build target: %v", err)
	}
	if next.SiteURL != server.URL+"/shop/ITEMSHOP" {
		t.Fatalf("site_url = %q", next.SiteURL)
	}
	if next.BaseURL != server.URL {
		t.Fatalf("base_url = %q", next.BaseURL)
	}
	if next.Token != "ITEMSHOP" {
		t.Fatalf("token = %q", next.Token)
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

func TestBuildShopTargetLeavesSortOrderUnsetWhenOmitted(t *testing.T) {
	current := &storage.ShopTarget{
		Name:      "shop",
		Platform:  storage.ShopPlatformLDXP,
		SiteURL:   "https://pay.ldxp.cn/shop/TOKEN",
		BaseURL:   "https://pay.ldxp.cn",
		Token:     "TOKEN",
		SortOrder: 20,
		GoodsSort: "category",
		ScopeMode: storage.ShopScopeAll,
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
	if next.SortOrder != 0 {
		t.Fatalf("sort_order = %d, want omitted sentinel 0", next.SortOrder)
	}
}

func TestParseShopURLAPIResolvesLDXPItemURL(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/shopApi/Shop/goodsInfo":
			_, _ = w.Write([]byte(strings.ReplaceAll(`{"code":1,"msg":"success","data":{"user":{"nickname":"商品店铺","token":"ITEMSHOP","link":"__SERVER__/shop/ITEMSHOP"}}}`, "__SERVER__", server.URL)))
		case "/shopApi/Shop/info":
			_, _ = w.Write([]byte(strings.ReplaceAll(`{"code":1,"msg":"success","data":{"nickname":"商品店铺","link":"__SERVER__/shop/ITEMSHOP","goods_count":2}}`, "__SERVER__", server.URL)))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	router := gin.New()
	router.POST("/api/shop-targets/parse-url", func(c *gin.Context) { parseShopURL(c, &Deps{}) })

	req := httptest.NewRequest(http.MethodPost, "/api/shop-targets/parse-url", bytes.NewBufferString(`{"site_url":"`+server.URL+`/item/9l814h"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data struct {
			Platform string `json:"platform"`
			SiteURL  string `json:"site_url"`
			BaseURL  string `json:"base_url"`
			Token    string `json:"token"`
			Name     string `json:"name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Data.Platform != string(storage.ShopPlatformLDXP) {
		t.Fatalf("platform = %q", body.Data.Platform)
	}
	if body.Data.SiteURL != server.URL+"/shop/ITEMSHOP" {
		t.Fatalf("site_url = %q", body.Data.SiteURL)
	}
	if body.Data.BaseURL != server.URL {
		t.Fatalf("base_url = %q", body.Data.BaseURL)
	}
	if body.Data.Token != "ITEMSHOP" {
		t.Fatalf("token = %q", body.Data.Token)
	}
	if body.Data.Name != "商品店铺" {
		t.Fatalf("name = %q", body.Data.Name)
	}
}

func TestCreateShopTargetReturnsConflictWhenShopExists(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := openTestDB(t)
	targets := storage.NewShopTargets(db)
	goods := storage.NewShopGoods(db)
	existing := &storage.ShopTarget{
		Name:           "shop-a",
		Platform:       storage.ShopPlatformLDXP,
		SiteURL:        "https://pay.ldxp.cn/shop/A",
		BaseURL:        "https://pay.ldxp.cn",
		Token:          "A",
		MonitorEnabled: true,
		ScopeMode:      storage.ShopScopeAll,
	}
	if err := targets.Create(existing); err != nil {
		t.Fatalf("create existing target: %v", err)
	}

	router := gin.New()
	registerShopTargets(router.Group("/api"), &Deps{
		ShopTargets: targets,
		ShopGoods:   goods,
	})

	body := `{
		"name":"shop-copy",
		"platform":"ldxp",
		"site_url":"https://pay.ldxp.cn/shop/A",
		"base_url":"https://pay.ldxp.cn",
		"token":"A"
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/shop-targets", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !strings.Contains(resp.Error, "店铺已存在") {
		t.Fatalf("error = %q", resp.Error)
	}
	list, err := targets.List()
	if err != nil {
		t.Fatalf("list targets: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("targets count = %d, want 1", len(list))
	}
}

func TestCreateShopTargetSortOrderUsesRequestedPosition(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := openTestDB(t)
	targets := storage.NewShopTargets(db)
	goods := storage.NewShopGoods(db)
	for _, item := range []*storage.ShopTarget{
		{Name: "shop-a", Platform: storage.ShopPlatformLDXP, SiteURL: "https://pay.ldxp.cn/shop/A", BaseURL: "https://pay.ldxp.cn", Token: "A", MonitorEnabled: true, ScopeMode: storage.ShopScopeAll},
		{Name: "shop-b", Platform: storage.ShopPlatformLDXP, SiteURL: "https://pay.ldxp.cn/shop/B", BaseURL: "https://pay.ldxp.cn", Token: "B", MonitorEnabled: true, ScopeMode: storage.ShopScopeAll},
		{Name: "shop-c", Platform: storage.ShopPlatformLDXP, SiteURL: "https://pay.ldxp.cn/shop/C", BaseURL: "https://pay.ldxp.cn", Token: "C", MonitorEnabled: true, ScopeMode: storage.ShopScopeAll},
	} {
		if err := targets.Create(item); err != nil {
			t.Fatalf("create %s: %v", item.Name, err)
		}
	}

	router := gin.New()
	registerShopTargets(router.Group("/api"), &Deps{
		ShopTargets: targets,
		ShopGoods:   goods,
	})

	body := `{
		"name":"shop-inserted",
		"platform":"ldxp",
		"site_url":"https://pay.ldxp.cn/shop/INSERTED",
		"base_url":"https://pay.ldxp.cn",
		"token":"INSERTED",
		"sort_order":2
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/shop-targets", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data storage.ShopTarget `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Data.SortOrder != 2 {
		t.Fatalf("created sort_order = %d, want 2", resp.Data.SortOrder)
	}
	list, err := targets.List()
	if err != nil {
		t.Fatalf("list targets: %v", err)
	}
	wantNames := []string{"shop-a", "shop-inserted", "shop-b", "shop-c"}
	if len(list) != len(wantNames) {
		t.Fatalf("targets count = %d, want %d", len(list), len(wantNames))
	}
	for i, wantName := range wantNames {
		if list[i].Name != wantName || list[i].SortOrder != i+1 {
			t.Fatalf("list[%d] = %#v, want name=%s sort_order=%d", i, list[i], wantName, i+1)
		}
	}
}

func TestUpdateShopTargetSortOrderSemantics(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := openTestDB(t)
	targets := storage.NewShopTargets(db)
	goods := storage.NewShopGoods(db)
	items := []*storage.ShopTarget{
		{Name: "shop-a", Platform: storage.ShopPlatformLDXP, SiteURL: "https://pay.ldxp.cn/shop/A", BaseURL: "https://pay.ldxp.cn", Token: "A", MonitorEnabled: true, ScopeMode: storage.ShopScopeAll},
		{Name: "shop-b", Platform: storage.ShopPlatformLDXP, SiteURL: "https://pay.ldxp.cn/shop/B", BaseURL: "https://pay.ldxp.cn", Token: "B", MonitorEnabled: true, ScopeMode: storage.ShopScopeAll},
		{Name: "shop-c", Platform: storage.ShopPlatformLDXP, SiteURL: "https://pay.ldxp.cn/shop/C", BaseURL: "https://pay.ldxp.cn", Token: "C", MonitorEnabled: true, ScopeMode: storage.ShopScopeAll},
	}
	for _, item := range items {
		if err := targets.Create(item); err != nil {
			t.Fatalf("create %s: %v", item.Name, err)
		}
	}

	router := gin.New()
	registerShopTargets(router.Group("/api"), &Deps{
		ShopTargets: targets,
		ShopGoods:   goods,
	})

	preserveBody := `{
		"name":"shop-b-renamed",
		"platform":"ldxp",
		"site_url":"https://pay.ldxp.cn/shop/B",
		"base_url":"https://pay.ldxp.cn",
		"token":"B"
	}`
	preserveReq := httptest.NewRequest(http.MethodPut, "/api/shop-targets/"+uintString(items[1].ID), strings.NewReader(preserveBody))
	preserveReq.Header.Set("Content-Type", "application/json")
	preserveRec := httptest.NewRecorder()
	router.ServeHTTP(preserveRec, preserveReq)
	if preserveRec.Code != http.StatusOK {
		t.Fatalf("preserve status = %d, body = %s", preserveRec.Code, preserveRec.Body.String())
	}

	moveBody := `{
		"name":"shop-a-moved",
		"platform":"ldxp",
		"site_url":"https://pay.ldxp.cn/shop/A",
		"base_url":"https://pay.ldxp.cn",
		"token":"A",
		"sort_order":99
	}`
	moveReq := httptest.NewRequest(http.MethodPut, "/api/shop-targets/"+uintString(items[0].ID), strings.NewReader(moveBody))
	moveReq.Header.Set("Content-Type", "application/json")
	moveRec := httptest.NewRecorder()
	router.ServeHTTP(moveRec, moveReq)
	if moveRec.Code != http.StatusOK {
		t.Fatalf("move status = %d, body = %s", moveRec.Code, moveRec.Body.String())
	}
	var moveResp struct {
		Data storage.ShopTarget `json:"data"`
	}
	if err := json.Unmarshal(moveRec.Body.Bytes(), &moveResp); err != nil {
		t.Fatalf("decode move response: %v", err)
	}
	if moveResp.Data.SortOrder != 3 {
		t.Fatalf("moved sort_order = %d, want clamped 3", moveResp.Data.SortOrder)
	}

	list, err := targets.List()
	if err != nil {
		t.Fatalf("list targets: %v", err)
	}
	wantNames := []string{"shop-b-renamed", "shop-c", "shop-a-moved"}
	if len(list) != len(wantNames) {
		t.Fatalf("targets count = %d, want %d", len(list), len(wantNames))
	}
	for i, wantName := range wantNames {
		if list[i].Name != wantName || list[i].SortOrder != i+1 {
			t.Fatalf("list[%d] = %#v, want name=%s sort_order=%d", i, list[i], wantName, i+1)
		}
	}
}

func TestUpdateShopTargetWithoutSortOrderPreservesSparseLegacyOrder(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := openTestDB(t)
	targets := storage.NewShopTargets(db)
	goods := storage.NewShopGoods(db)
	items := []*storage.ShopTarget{
		{Name: "shop-a", Platform: storage.ShopPlatformLDXP, SiteURL: "https://pay.ldxp.cn/shop/A", BaseURL: "https://pay.ldxp.cn", Token: "A", MonitorEnabled: true, ScopeMode: storage.ShopScopeAll},
		{Name: "shop-b", Platform: storage.ShopPlatformLDXP, SiteURL: "https://pay.ldxp.cn/shop/B", BaseURL: "https://pay.ldxp.cn", Token: "B", MonitorEnabled: true, ScopeMode: storage.ShopScopeAll},
		{Name: "shop-c", Platform: storage.ShopPlatformLDXP, SiteURL: "https://pay.ldxp.cn/shop/C", BaseURL: "https://pay.ldxp.cn", Token: "C", MonitorEnabled: true, ScopeMode: storage.ShopScopeAll},
	}
	for _, item := range items {
		if err := targets.Create(item); err != nil {
			t.Fatalf("create %s: %v", item.Name, err)
		}
	}
	if err := targets.UpdateSortOrders(map[uint]int{
		items[0].ID: 10,
		items[1].ID: 20,
		items[2].ID: 30,
	}); err != nil {
		t.Fatalf("seed sparse sort orders: %v", err)
	}

	router := gin.New()
	registerShopTargets(router.Group("/api"), &Deps{
		ShopTargets: targets,
		ShopGoods:   goods,
	})

	body := `{
		"name":"shop-b-renamed",
		"platform":"ldxp",
		"site_url":"https://pay.ldxp.cn/shop/B",
		"base_url":"https://pay.ldxp.cn",
		"token":"B"
	}`
	req := httptest.NewRequest(http.MethodPut, "/api/shop-targets/"+uintString(items[1].ID), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data storage.ShopTarget `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Data.SortOrder != 20 {
		t.Fatalf("returned sort_order = %d, want preserved 20", resp.Data.SortOrder)
	}

	list, err := targets.List()
	if err != nil {
		t.Fatalf("list targets: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("targets count = %d, want 3", len(list))
	}
	wantNames := []string{"shop-a", "shop-b-renamed", "shop-c"}
	wantOrders := []int{10, 20, 30}
	for i := range wantNames {
		if list[i].Name != wantNames[i] || list[i].SortOrder != wantOrders[i] {
			t.Fatalf("list[%d] = %#v, want name=%s sort_order=%d", i, list[i], wantNames[i], wantOrders[i])
		}
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

func intPtr(value int) *int {
	return &value
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

func TestListAllShopGoodsReturnsTargetMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := openTestDB(t)
	targets := storage.NewShopTargets(db)
	goods := storage.NewShopGoods(db)
	target := &storage.ShopTarget{
		Name:           "shop-a",
		Platform:       storage.ShopPlatformLDXP,
		SiteURL:        "https://pay.ldxp.cn/shop/A",
		BaseURL:        "https://pay.ldxp.cn",
		Token:          "A",
		MonitorEnabled: true,
		NotifyEnabled:  true,
		ScopeMode:      storage.ShopScopeAll,
		StockThreshold: 2,
	}
	if err := targets.Create(target); err != nil {
		t.Fatalf("create target: %v", err)
	}
	now := time.Now()
	if err := goods.CreateSnapshot(&storage.ShopGoodsSnapshot{
		TargetID:     target.ID,
		GoodsKey:     "sku-low",
		GoodsType:    "card",
		Name:         "低库存商品",
		CategoryID:   10,
		CategoryName: "GPT",
		Link:         "https://pay.ldxp.cn/buy/sku-low",
		Price:        1.23,
		StockCount:   1,
		FirstSeenAt:  now,
		LastSeenAt:   now,
	}); err != nil {
		t.Fatalf("create snapshot: %v", err)
	}

	router := gin.New()
	registerShopTargets(router.Group("/api"), &Deps{
		ShopTargets: targets,
		ShopGoods:   goods,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/shop-goods?target_id="+uintString(target.ID)+"&status=low_stock", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			Items []storage.ShopGoodsWithTarget `json:"items"`
			Total int64                         `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Data.Total != 1 || len(resp.Data.Items) != 1 {
		t.Fatalf("unexpected goods page: %#v", resp.Data)
	}
	item := resp.Data.Items[0]
	if item.TargetID != target.ID || item.TargetName != target.Name || item.TargetSiteURL != target.SiteURL || !item.TargetNotifyEnabled {
		t.Fatalf("target metadata mismatch: %#v", item)
	}
}

type fakeShopSyncJobRunner struct {
	job        *storage.ShopSyncJob
	batch      *storage.ShopSyncBatch
	batchItems []storage.ShopSyncBatchItem
	reused     bool
	started    []uint
}

func (r *fakeShopSyncJobRunner) Start(targetID uint) (*storage.ShopSyncJob, bool, error) {
	r.started = append(r.started, targetID)
	job := *r.job
	job.TargetID = targetID
	r.job = &job
	return &job, r.reused, nil
}

func (r *fakeShopSyncJobRunner) Get(targetID, jobID uint) (*storage.ShopSyncJob, error) {
	if r.job != nil && r.job.TargetID == targetID && r.job.ID == jobID {
		return r.job, nil
	}
	return nil, gorm.ErrRecordNotFound
}

func (r *fakeShopSyncJobRunner) Latest(targetID uint) (*storage.ShopSyncJob, error) {
	if r.job != nil && r.job.TargetID == targetID {
		return r.job, nil
	}
	return nil, gorm.ErrRecordNotFound
}

func (r *fakeShopSyncJobRunner) GetMany(jobIDs []uint) ([]storage.ShopSyncJob, error) {
	if r.job == nil {
		return nil, nil
	}
	for _, id := range jobIDs {
		if r.job.ID == id {
			return []storage.ShopSyncJob{*r.job}, nil
		}
	}
	return nil, nil
}

func (r *fakeShopSyncJobRunner) CreateBatchWithItems(total, queued, reused, startFailed int, items []storage.ShopSyncBatchItem, startedAt time.Time) (*storage.ShopSyncBatch, error) {
	r.batch = &storage.ShopSyncBatch{
		ID:               31,
		Status:           storage.ShopSyncBatchRunning,
		TotalCount:       total,
		QueuedCount:      queued,
		ReusedCount:      reused,
		StartFailedCount: startFailed,
		FailedCount:      startFailed,
		StartedAt:        startedAt,
	}
	r.batchItems = append([]storage.ShopSyncBatchItem(nil), items...)
	for i := range r.batchItems {
		r.batchItems[i].BatchID = r.batch.ID
	}
	return r.batch, nil
}

func (r *fakeShopSyncJobRunner) LatestBatch() (*storage.ShopSyncBatch, error) {
	if r.batch == nil {
		return nil, gorm.ErrRecordNotFound
	}
	return r.batch, nil
}

func (r *fakeShopSyncJobRunner) BatchDetails(batchID uint) (*shopmonitor.SyncBatchDetails, error) {
	if r.batch == nil || r.batch.ID != batchID {
		return nil, gorm.ErrRecordNotFound
	}
	details := &shopmonitor.SyncBatchDetails{Batch: r.batch, Items: make([]shopmonitor.SyncBatchItemDetail, 0, len(r.batchItems))}
	for i := range r.batchItems {
		item := shopmonitor.SyncBatchItemDetail{ShopSyncBatchItem: r.batchItems[i]}
		if r.job != nil && r.job.ID == r.batchItems[i].JobID && r.job.TargetID == r.batchItems[i].TargetID {
			item.Job = r.job
		}
		details.Items = append(details.Items, item)
	}
	return details, nil
}

func TestShopSyncJobEndpointsStartAndReadJob(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openTestDB(t)
	targets := storage.NewShopTargets(db)
	goods := storage.NewShopGoods(db)
	runner := &fakeShopSyncJobRunner{job: &storage.ShopSyncJob{ID: 12, Status: storage.ShopSyncJobQueued}}
	router := gin.New()
	registerShopTargets(router.Group("/api"), &Deps{
		ShopTargets:    targets,
		ShopGoods:      goods,
		ShopSyncRunner: runner,
	})

	startReq := httptest.NewRequest(http.MethodPost, "/api/shop-targets/9/sync", nil)
	startRec := httptest.NewRecorder()
	router.ServeHTTP(startRec, startReq)
	if startRec.Code != http.StatusAccepted {
		t.Fatalf("start status = %d, body = %s", startRec.Code, startRec.Body.String())
	}
	var startResp struct {
		Data struct {
			Job    storage.ShopSyncJob `json:"job"`
			Reused bool                `json:"reused"`
		} `json:"data"`
	}
	if err := json.Unmarshal(startRec.Body.Bytes(), &startResp); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	if startResp.Data.Job.ID != 12 || startResp.Data.Job.TargetID != 9 || startResp.Data.Reused {
		t.Fatalf("start response = %#v", startResp.Data)
	}

	readReq := httptest.NewRequest(http.MethodGet, "/api/shop-targets/9/sync-jobs/12", nil)
	readRec := httptest.NewRecorder()
	router.ServeHTTP(readRec, readReq)
	if readRec.Code != http.StatusOK {
		t.Fatalf("read status = %d, body = %s", readRec.Code, readRec.Body.String())
	}
	latestReq := httptest.NewRequest(http.MethodGet, "/api/shop-targets/9/sync-jobs/latest", nil)
	latestRec := httptest.NewRecorder()
	router.ServeHTTP(latestRec, latestReq)
	if latestRec.Code != http.StatusOK {
		t.Fatalf("latest status = %d, body = %s", latestRec.Code, latestRec.Body.String())
	}

	statusReq := httptest.NewRequest(http.MethodPost, "/api/shop-targets/sync-jobs/status", strings.NewReader(`{"job_ids":[12]}`))
	statusReq.Header.Set("Content-Type", "application/json")
	statusRec := httptest.NewRecorder()
	router.ServeHTTP(statusRec, statusReq)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("batch status = %d, body = %s", statusRec.Code, statusRec.Body.String())
	}
}

func TestLatestShopMonitorLogEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openTestDB(t)
	targets := storage.NewShopTargets(db)
	goods := storage.NewShopGoods(db)
	oldStartedAt := time.Now().Add(-10 * time.Minute)
	newStartedAt := time.Now().Add(-5 * time.Minute)
	if err := goods.AppendMonitorLog(&storage.ShopMonitorLog{
		TargetID:   1,
		Success:    true,
		StartedAt:  oldStartedAt,
		FinishedAt: oldStartedAt.Add(time.Second),
	}); err != nil {
		t.Fatalf("append old monitor log: %v", err)
	}
	if err := goods.AppendMonitorLog(&storage.ShopMonitorLog{
		TargetID:   2,
		Success:    true,
		StartedAt:  newStartedAt,
		FinishedAt: newStartedAt.Add(2500 * time.Millisecond),
	}); err != nil {
		t.Fatalf("append new monitor log: %v", err)
	}
	router := gin.New()
	registerShopTargets(router.Group("/api"), &Deps{ShopTargets: targets, ShopGoods: goods})

	req := httptest.NewRequest(http.MethodGet, "/api/shop-targets/monitor-logs/latest", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data storage.ShopMonitorLog `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Data.TargetID != 2 || resp.Data.DurationMS <= 0 {
		t.Fatalf("latest monitor log = %#v", resp.Data)
	}
}

func TestSyncAllShopTargetsQueuesBackgroundJobs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openTestDB(t)
	targets := storage.NewShopTargets(db)
	for _, name := range []string{"shop-a", "shop-b"} {
		if err := targets.Create(&storage.ShopTarget{
			Name:           name,
			Platform:       storage.ShopPlatformLDXP,
			SiteURL:        "https://pay.ldxp.cn/shop/" + name,
			BaseURL:        "https://pay.ldxp.cn",
			Token:          name,
			MonitorEnabled: true,
		}); err != nil {
			t.Fatalf("create target %s: %v", name, err)
		}
	}
	runner := &fakeShopSyncJobRunner{job: &storage.ShopSyncJob{ID: 20, Status: storage.ShopSyncJobQueued}}
	router := gin.New()
	registerShopTargets(router.Group("/api"), &Deps{ShopTargets: targets, ShopSyncRunner: runner})

	req := httptest.NewRequest(http.MethodPost, "/api/shop-targets/sync-all", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(runner.started) != 2 {
		t.Fatalf("started jobs = %v, want two", runner.started)
	}
	var resp struct {
		Data struct {
			Total  int                   `json:"total"`
			Queued int                   `json:"queued"`
			Batch  storage.ShopSyncBatch `json:"batch"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Data.Total != 2 || resp.Data.Queued != 2 || resp.Data.Batch.ID != 31 || resp.Data.Batch.TotalCount != 2 {
		t.Fatalf("response = %#v", resp.Data)
	}

	latestReq := httptest.NewRequest(http.MethodGet, "/api/shop-targets/sync-batches/latest", nil)
	latestRec := httptest.NewRecorder()
	router.ServeHTTP(latestRec, latestReq)
	if latestRec.Code != http.StatusOK {
		t.Fatalf("latest batch status = %d, body = %s", latestRec.Code, latestRec.Body.String())
	}

	detailsReq := httptest.NewRequest(http.MethodGet, "/api/shop-targets/sync-batches/31", nil)
	detailsRec := httptest.NewRecorder()
	router.ServeHTTP(detailsRec, detailsReq)
	if detailsRec.Code != http.StatusOK {
		t.Fatalf("batch details status = %d, body = %s", detailsRec.Code, detailsRec.Body.String())
	}
	var detailsResp struct {
		Data shopmonitor.SyncBatchDetails `json:"data"`
	}
	if err := json.Unmarshal(detailsRec.Body.Bytes(), &detailsResp); err != nil {
		t.Fatalf("decode batch details: %v", err)
	}
	if len(detailsResp.Data.Items) != 2 || detailsResp.Data.Items[0].TargetName != "shop-a" || detailsResp.Data.Items[1].TargetName != "shop-b" {
		t.Fatalf("batch details = %#v", detailsResp.Data)
	}
}

func TestGlobalShopWatchRulesApplyExcludeKeywordsAcrossShops(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openTestDB(t)
	targets := storage.NewShopTargets(db)
	goods := storage.NewShopGoods(db)
	rules := storage.NewShopWatchRules(db)
	target := &storage.ShopTarget{
		Name:           "global-rule-shop",
		Platform:       storage.ShopPlatformLDXP,
		SiteURL:        "https://pay.ldxp.cn/shop/GLOBAL",
		BaseURL:        "https://pay.ldxp.cn",
		Token:          "GLOBAL",
		MonitorEnabled: true,
		ScopeMode:      storage.ShopScopeAll,
	}
	if err := targets.Create(target); err != nil {
		t.Fatalf("create target: %v", err)
	}
	secondTarget := &storage.ShopTarget{
		Name:           "global-rule-shop-2",
		Platform:       storage.ShopPlatformLDXP,
		SiteURL:        "https://pay.ldxp.cn/shop/GLOBAL-2",
		BaseURL:        "https://pay.ldxp.cn",
		Token:          "GLOBAL-2",
		MonitorEnabled: true,
		ScopeMode:      storage.ShopScopeAll,
	}
	if err := targets.Create(secondTarget); err != nil {
		t.Fatalf("create second target: %v", err)
	}
	now := time.Now()
	for _, snapshot := range []storage.ShopGoodsSnapshot{
		{TargetID: target.ID, GoodsKey: "keep", GoodsType: "card", Name: "Claude Pro", Price: 1, FirstSeenAt: now, LastSeenAt: now},
		{TargetID: target.ID, GoodsKey: "skip", GoodsType: "card", Name: "Claude 测试套餐", Price: 1, FirstSeenAt: now, LastSeenAt: now},
		{TargetID: secondTarget.ID, GoodsKey: "keep-second", GoodsType: "card", Name: "Claude Team", Price: 1, FirstSeenAt: now, LastSeenAt: now},
	} {
		if err := goods.CreateSnapshot(&snapshot); err != nil {
			t.Fatalf("create snapshot: %v", err)
		}
	}

	router := gin.New()
	registerShopTargets(router.Group("/api"), &Deps{ShopTargets: targets, ShopGoods: goods, ShopWatchRules: rules})
	body := `{"name":"全局 Claude","enabled":true,"keywords":["Claude"],"exclude_keywords":["测试"],"events":["stock_changed"],"stock_threshold":1}`
	createReq := httptest.NewRequest(http.MethodPost, "/api/shop-watch-rules", strings.NewReader(body))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	router.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusOK {
		t.Fatalf("create status = %d, body = %s", createRec.Code, createRec.Body.String())
	}
	var created struct {
		Data storage.ShopWatchRule `json:"data"`
	}
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.Data.TargetID != 0 || created.Data.ExcludeKeywordsJSON != `["测试"]` {
		t.Fatalf("created global rule = %#v", created.Data)
	}

	previewReq := httptest.NewRequest(http.MethodPost, "/api/shop-watch-rules/preview", strings.NewReader(body))
	previewReq.Header.Set("Content-Type", "application/json")
	previewRec := httptest.NewRecorder()
	router.ServeHTTP(previewRec, previewReq)
	if previewRec.Code != http.StatusOK {
		t.Fatalf("preview status = %d, body = %s", previewRec.Code, previewRec.Body.String())
	}
	var preview struct {
		Data shopWatchRulePreview `json:"data"`
	}
	if err := json.Unmarshal(previewRec.Body.Bytes(), &preview); err != nil {
		t.Fatalf("decode preview response: %v", err)
	}
	if preview.Data.Total != 2 || len(preview.Data.Items) != 2 {
		t.Fatalf("preview = %#v", preview.Data)
	}
	matched := map[string]bool{}
	for _, item := range preview.Data.Items {
		matched[item.GoodsKey] = true
	}
	if !matched["keep"] || !matched["keep-second"] || matched["skip"] {
		t.Fatalf("preview items = %#v", preview.Data.Items)
	}
}
