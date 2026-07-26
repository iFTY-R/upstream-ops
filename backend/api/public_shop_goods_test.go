package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ifty-r/upstream-ops/backend/auth"
	"github.com/ifty-r/upstream-ops/backend/storage"
)

func TestPublicShopGoodsEndpointsExposeOnlyAllowedFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openTestDB(t)
	targets := storage.NewShopTargets(db)
	goods := storage.NewShopGoods(db)
	target := &storage.ShopTarget{
		Name:           "Public Shop",
		Platform:       storage.ShopPlatformLDXP,
		SiteURL:        "https://pay.ldxp.cn/shop/public-shop",
		BaseURL:        "https://pay.ldxp.cn",
		Token:          "secret-shop-token",
		ProxyEnabled:   true,
		MonitorEnabled: true,
		StockThreshold: 2,
		LastError:      "internal sync error",
		LastShopName:   "Upstream Shop",
	}
	if err := targets.Create(target); err != nil {
		t.Fatalf("create target: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	snapshot := &storage.ShopGoodsSnapshot{
		TargetID:     target.ID,
		GoodsKey:     "goods-1",
		GoodsType:    "card",
		Name:         "Public Goods",
		CategoryID:   7,
		CategoryName: "AI",
		Link:         "https://pay.ldxp.cn/buy/goods-1",
		Price:        12.5,
		MarketPrice:  20,
		StockCount:   3,
		LimitCount:   5,
		RawJSON:      `{"credential":"must-not-leak"}`,
		FirstSeenAt:  now,
		LastSeenAt:   now,
	}
	if err := db.Create(snapshot).Error; err != nil {
		t.Fatalf("create snapshot: %v", err)
	}

	router := newPublicShopGoodsTestRouter(t, targets, goods)

	targetResponse := performRequest(router, http.MethodGet, "/api/public/shop-targets")
	if targetResponse.Code != http.StatusOK {
		t.Fatalf("target status = %d, body = %s", targetResponse.Code, targetResponse.Body.String())
	}
	var targetBody struct {
		Data []map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(targetResponse.Body.Bytes(), &targetBody); err != nil {
		t.Fatalf("decode targets: %v", err)
	}
	if len(targetBody.Data) != 1 {
		t.Fatalf("target count = %d, want 1", len(targetBody.Data))
	}
	assertOnlyJSONKeys(t, targetBody.Data[0], "id", "name", "last_shop_name", "site_url")

	goodsResponse := performRequest(router, http.MethodGet, "/api/public/shop-goods?status=in_stock")
	if goodsResponse.Code != http.StatusOK {
		t.Fatalf("goods status = %d, body = %s", goodsResponse.Code, goodsResponse.Body.String())
	}
	var goodsBody struct {
		Data struct {
			Items []map[string]json.RawMessage `json:"items"`
			Total int                          `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(goodsResponse.Body.Bytes(), &goodsBody); err != nil {
		t.Fatalf("decode goods: %v", err)
	}
	if goodsBody.Data.Total != 1 || len(goodsBody.Data.Items) != 1 {
		t.Fatalf("unexpected goods page: total=%d items=%d", goodsBody.Data.Total, len(goodsBody.Data.Items))
	}
	assertOnlyJSONKeys(t, goodsBody.Data.Items[0],
		"id", "target_id", "goods_key", "name", "category_name", "link", "price",
		"stock_count", "limit_count", "last_seen_at", "removed_at", "target_name",
		"target_last_shop_name", "target_site_url", "target_stock_threshold",
	)
	var limitCount int
	if err := json.Unmarshal(goodsBody.Data.Items[0]["limit_count"], &limitCount); err != nil {
		t.Fatalf("decode limit_count: %v", err)
	}
	if limitCount != 5 {
		t.Fatalf("limit_count = %d, want 5", limitCount)
	}

	excludedResponse := performRequest(router, http.MethodGet, "/api/public/shop-goods?keyword=Public%20Goods&exclude_keyword=goods-1")
	if excludedResponse.Code != http.StatusOK {
		t.Fatalf("excluded goods status = %d, body = %s", excludedResponse.Code, excludedResponse.Body.String())
	}
	var excludedBody struct {
		Data struct {
			Total int `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(excludedResponse.Body.Bytes(), &excludedBody); err != nil {
		t.Fatalf("decode excluded goods: %v", err)
	}
	if excludedBody.Data.Total != 0 {
		t.Fatalf("excluded goods total = %d, want 0", excludedBody.Data.Total)
	}
}

func TestPublicShopGoodsNameGroupingSanitizesEveryQuote(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openTestDB(t)
	targets := storage.NewShopTargets(db)
	goods := storage.NewShopGoods(db)
	safeTarget := &storage.ShopTarget{
		Name:           "Safe Shop",
		Platform:       storage.ShopPlatformLDXP,
		SiteURL:        "https://pay.ldxp.cn/shop/safe",
		BaseURL:        "https://pay.ldxp.cn",
		Token:          "safe-secret",
		MonitorEnabled: true,
	}
	unsafeTarget := &storage.ShopTarget{
		Name:           "Unsafe Shop",
		Platform:       storage.ShopPlatformLDXP,
		SiteURL:        "javascript:alert(1)",
		BaseURL:        "https://pay.ldxp.cn",
		Token:          "unsafe-secret",
		MonitorEnabled: true,
	}
	for _, target := range []*storage.ShopTarget{safeTarget, unsafeTarget} {
		if err := targets.Create(target); err != nil {
			t.Fatalf("create target %q: %v", target.Name, err)
		}
	}
	now := time.Now().UTC().Truncate(time.Second)
	for _, snapshot := range []storage.ShopGoodsSnapshot{
		{TargetID: safeTarget.ID, GoodsKey: "plus-safe", GoodsType: "card", Name: "ChatGPT Plus", Link: "https://pay.ldxp.cn/buy/plus-safe", Price: 12, StockCount: 2, RawJSON: `{"secret":"safe"}`, FirstSeenAt: now, LastSeenAt: now},
		{TargetID: unsafeTarget.ID, GoodsKey: "plus-unsafe", GoodsType: "card", Name: " chatgpt plus ", Link: "data:text/html,unsafe", Price: 10, StockCount: 3, RawJSON: `{"secret":"unsafe"}`, FirstSeenAt: now, LastSeenAt: now},
	} {
		snapshot := snapshot
		if err := goods.CreateSnapshot(&snapshot); err != nil {
			t.Fatalf("create snapshot %q: %v", snapshot.GoodsKey, err)
		}
	}

	router := newPublicShopGoodsTestRouter(t, targets, goods)
	grouped := performRequest(router, http.MethodGet, "/api/public/shop-goods?group_by=name&page_size=100000")
	if grouped.Code != http.StatusOK {
		t.Fatalf("grouped status = %d, body = %s", grouped.Code, grouped.Body.String())
	}
	var body struct {
		Data struct {
			Items    []map[string]json.RawMessage `json:"items"`
			Total    int                          `json:"total"`
			PageSize int                          `json:"page_size"`
		} `json:"data"`
	}
	if err := json.Unmarshal(grouped.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode grouped response: %v", err)
	}
	if body.Data.Total != 1 || body.Data.PageSize != 200 || len(body.Data.Items) != 1 {
		t.Fatalf("unexpected grouped response: %#v", body.Data)
	}
	assertOnlyJSONKeys(t, body.Data.Items[0],
		"group_key", "name", "shop_count", "quote_count", "total_stock",
		"min_price", "max_price", "latest_seen_at", "quotes",
	)
	var quotes []map[string]json.RawMessage
	if err := json.Unmarshal(body.Data.Items[0]["quotes"], &quotes); err != nil {
		t.Fatalf("decode grouped quotes: %v", err)
	}
	if len(quotes) != 2 {
		t.Fatalf("grouped quote count = %d, want 2", len(quotes))
	}
	for _, quote := range quotes {
		assertOnlyJSONKeys(t, quote,
			"id", "target_id", "goods_key", "name", "category_name", "link", "price",
			"stock_count", "limit_count", "last_seen_at", "removed_at", "target_name",
			"target_last_shop_name", "target_site_url", "target_stock_threshold",
		)
		var goodsKey, link, siteURL string
		if err := json.Unmarshal(quote["goods_key"], &goodsKey); err != nil {
			t.Fatalf("decode goods key: %v", err)
		}
		if err := json.Unmarshal(quote["link"], &link); err != nil {
			t.Fatalf("decode link: %v", err)
		}
		if err := json.Unmarshal(quote["target_site_url"], &siteURL); err != nil {
			t.Fatalf("decode site URL: %v", err)
		}
		if goodsKey == "plus-unsafe" && (link != "" || siteURL != "") {
			t.Fatalf("unsafe quote URLs leaked: %#v", quote)
		}
	}

	invalid := performRequest(router, http.MethodGet, "/api/public/shop-goods?group_by=shop")
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid group_by status = %d, body = %s", invalid.Code, invalid.Body.String())
	}
}

func TestPublicShopGoodsRoutesDoNotOpenManagementEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openTestDB(t)
	router := newPublicShopGoodsTestRouter(t, storage.NewShopTargets(db), storage.NewShopGoods(db))

	protected := performRequest(router, http.MethodGet, "/api/shop-targets")
	if protected.Code != http.StatusUnauthorized {
		t.Fatalf("protected status = %d, want %d", protected.Code, http.StatusUnauthorized)
	}
	mutation := performRequest(router, http.MethodPost, "/api/public/shop-goods")
	if mutation.Code != http.StatusNotFound {
		t.Fatalf("public mutation status = %d, want %d", mutation.Code, http.StatusNotFound)
	}
}

func TestSafePublicShopURLAllowsOnlyHTTP(t *testing.T) {
	if got := safePublicShopURL("https://pay.ldxp.cn/shop/demo"); got != "https://pay.ldxp.cn/shop/demo" {
		t.Fatalf("safe URL = %q", got)
	}
	for _, value := range []string{"javascript:alert(1)", "data:text/html,unsafe", "//missing-scheme.example"} {
		if got := safePublicShopURL(value); got != "" {
			t.Fatalf("unsafe URL %q returned %q", value, got)
		}
	}
}

func newPublicShopGoodsTestRouter(t *testing.T, targets *storage.ShopTargets, goods *storage.ShopGoods) *gin.Engine {
	t.Helper()
	authService, err := auth.New("admin", "password", "test-secret", time.Hour)
	if err != nil {
		t.Fatalf("create auth service: %v", err)
	}
	deps := &Deps{ShopTargets: targets, ShopGoods: goods}
	router := gin.New()
	registerPublicShopGoods(router.Group("/api/public"), deps)
	protected := router.Group("/api")
	protected.Use(authService.Middleware())
	registerShopTargets(protected, deps)
	return router
}

func performRequest(router http.Handler, method, path string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(method, path, nil))
	return recorder
}

func assertOnlyJSONKeys(t *testing.T, value map[string]json.RawMessage, expected ...string) {
	t.Helper()
	if len(value) != len(expected) {
		t.Fatalf("response keys = %v, want exactly %v", mapKeys(value), expected)
	}
	for _, key := range expected {
		if _, ok := value[key]; !ok {
			t.Fatalf("response is missing %q; keys=%v", key, mapKeys(value))
		}
	}
}

func mapKeys(value map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	return keys
}
