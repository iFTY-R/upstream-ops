package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
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
	job    *storage.ShopSyncJob
	reused bool
}

func (r *fakeShopSyncJobRunner) Start(targetID uint) (*storage.ShopSyncJob, bool, error) {
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
}
