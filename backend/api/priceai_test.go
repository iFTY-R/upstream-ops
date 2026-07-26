package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ifty-r/upstream-ops/backend/shopprovider"
	"github.com/ifty-r/upstream-ops/backend/storage"
)

func newPriceAITestRouter(t *testing.T, repo *storage.PriceAI, targets *storage.ShopTargets) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	registerPriceAI(router.Group("/api"), &Deps{PriceAI: repo, ShopTargets: targets})
	return router
}

func performPriceAIRequest(router http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func createPriceAITestProduct(t *testing.T, repo *storage.PriceAI, slug string) *storage.PriceAIProduct {
	t.Helper()
	now := time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)
	price := 9.99
	currency := "USD"
	product := &storage.PriceAIProduct{
		RemoteID:                   "product-" + slug,
		Slug:                       slug,
		Name:                       "PriceAI " + slug,
		Platform:                   "chatgpt",
		ProductType:                "subscription",
		OfferCount:                 2,
		InStockCount:               2,
		LowestPrice:                &price,
		LowestPriceCurrency:        &currency,
		LatestSeenAt:               now,
		ProductSnapshotGeneratedAt: now,
		LastSnapshotID:             "snapshot-1",
		FirstSeenAt:                now,
		LastSeenAt:                 now,
	}
	if _, err := repo.UpsertProduct(product); err != nil {
		t.Fatalf("upsert product: %v", err)
	}
	return product
}

func createPriceAITestOffer(t *testing.T, repo *storage.PriceAI, productID uint, remoteID, sourceID, title, rawURL string, price float64) *storage.PriceAIOffer {
	t.Helper()
	now := time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)
	offer := &storage.PriceAIOffer{
		ProductID:       productID,
		RemoteID:        remoteID,
		DedupeKey:       remoteID,
		SourceID:        sourceID,
		SourceName:      "merchant-" + sourceID,
		SourceStoreName: "store-" + sourceID,
		MerchantKey:     sourceID,
		Title:           title,
		NormalizedTitle: strings.ToLower(title),
		Price:           price,
		Currency:        "USD",
		Status:          "in_stock",
		URL:             rawURL,
		LastSnapshotID:  "snapshot-1",
		FirstSeenAt:     now,
		LastSeenAt:      now,
	}
	if _, err := repo.UpsertOffer(offer); err != nil {
		t.Fatalf("upsert offer: %v", err)
	}
	return offer
}

func addPriceAITestDefaultRanking(t *testing.T, repo *storage.PriceAI, productID, offerID uint, rank int) {
	t.Helper()
	if _, err := repo.UpsertOfferRanking(&storage.PriceAIOfferRanking{
		ProductID:        productID,
		OfferID:          offerID,
		BoardKind:        storage.PriceAIBoardDefault,
		Rank:             rank,
		BoardGeneratedAt: time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC),
		LastSnapshotID:   "snapshot-1",
	}); err != nil {
		t.Fatalf("upsert ranking: %v", err)
	}
}

func stubPriceAIItemResolver(t *testing.T, token string) {
	t.Helper()
	previous := parsePriceAIShopURLContext
	parsePriceAIShopURLContext = func(_ context.Context, raw string) (*shopprovider.ParsedURL, error) {
		goodsKey, err := shopprovider.LDXPItemGoodsKey(raw)
		if err != nil {
			return nil, err
		}
		return &shopprovider.ParsedURL{
			Platform: storage.ShopPlatformLDXP,
			SiteURL:  "https://pay.ldxp.cn/shop/" + token,
			BaseURL:  "https://pay.ldxp.cn",
			Token:    token,
			GoodsKey: goodsKey,
		}, nil
	}
	t.Cleanup(func() { parsePriceAIShopURLContext = previous })
}

func TestPriceAIProductsAndOffersUseDataEnvelopeAndExactRiskMatches(t *testing.T) {
	db := openTestDB(t)
	repo := storage.NewPriceAI(db)
	product := createPriceAITestProduct(t, repo, "chatgpt-plus")
	risky := createPriceAITestOffer(t, repo, product.ID, "offer-risk", "source-risk", "ChatGPT Plus", "https://pay.ldxp.cn/item/risk-item", 8.99)
	plain := createPriceAITestOffer(t, repo, product.ID, "offer-plain", "source-plain", "ChatGPT Plus", "https://pay.ldxp.cn/item/plain-item", 9.99)
	addPriceAITestDefaultRanking(t, repo, product.ID, risky.ID, 1)
	addPriceAITestDefaultRanking(t, repo, product.ID, plain.ID, 2)
	if _, err := repo.UpsertRiskFeedback(&storage.PriceAIRiskFeedback{
		ProductID:       product.ID,
		Scope:           storage.PriceAIRiskScopeSource,
		SubjectRemoteID: "source-risk",
		Status:          "user_report_pending_verification",
		FeedbackCount:   2,
		FetchedAt:       ptrTime(time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)),
	}); err != nil {
		t.Fatalf("upsert matching risk feedback: %v", err)
	}
	if _, err := repo.UpsertRiskFeedback(&storage.PriceAIRiskFeedback{
		ProductID:       product.ID,
		Scope:           storage.PriceAIRiskScopeSource,
		SubjectRemoteID: "unmatched-source",
		Status:          "user_report_pending_verification",
		FeedbackCount:   9,
	}); err != nil {
		t.Fatalf("upsert unmatched risk feedback: %v", err)
	}

	router := newPriceAITestRouter(t, repo, storage.NewShopTargets(db))
	productsRec := performPriceAIRequest(router, http.MethodGet, "/api/priceai/products?query=plus", "")
	if productsRec.Code != http.StatusOK {
		t.Fatalf("products status = %d, body = %s", productsRec.Code, productsRec.Body.String())
	}
	if strings.Contains(productsRec.Body.String(), "raw_json") {
		t.Fatalf("catalog response leaked persisted raw feed payload: %s", productsRec.Body.String())
	}
	var productsResp struct {
		Data struct {
			Items []storage.PriceAIProduct `json:"items"`
			Total int64                    `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(productsRec.Body.Bytes(), &productsResp); err != nil {
		t.Fatalf("decode products response: %v", err)
	}
	if productsResp.Data.Total != 1 || len(productsResp.Data.Items) != 1 || productsResp.Data.Items[0].ID != product.ID {
		t.Fatalf("products response = %#v", productsResp.Data)
	}

	offersRec := performPriceAIRequest(router, http.MethodGet, "/api/priceai/products/chatgpt-plus/offers?board=default", "")
	if offersRec.Code != http.StatusOK {
		t.Fatalf("offers status = %d, body = %s", offersRec.Code, offersRec.Body.String())
	}
	var offersResp struct {
		Data struct {
			Items []struct {
				RiskBadgeCount int `json:"risk_badge_count"`
				Quotes         []struct {
					ID           uint                     `json:"id"`
					RiskFeedback []priceAIRiskFeedbackDTO `json:"risk_feedback"`
				} `json:"quotes"`
			} `json:"items"`
			Coverage string `json:"coverage"`
		} `json:"data"`
	}
	if err := json.Unmarshal(offersRec.Body.Bytes(), &offersResp); err != nil {
		t.Fatalf("decode offers response: %v", err)
	}
	if len(offersResp.Data.Items) != 1 || len(offersResp.Data.Items[0].Quotes) != 2 || offersResp.Data.Items[0].RiskBadgeCount != 1 {
		t.Fatalf("offer groups = %#v", offersResp.Data.Items)
	}
	if offersResp.Data.Coverage == "" {
		t.Fatal("offer coverage disclosure is missing")
	}
	for _, quote := range offersResp.Data.Items[0].Quotes {
		switch quote.ID {
		case risky.ID:
			if len(quote.RiskFeedback) != 1 || quote.RiskFeedback[0].SubjectRemoteID != "source-risk" {
				t.Fatalf("risky quote feedback = %#v", quote.RiskFeedback)
			}
		case plain.ID:
			if len(quote.RiskFeedback) != 0 {
				t.Fatalf("plain quote incorrectly received feedback = %#v", quote.RiskFeedback)
			}
		default:
			t.Fatalf("unexpected quote ID %d", quote.ID)
		}
	}
}

func TestPriceAIWatchTargetLifecycleAndValidation(t *testing.T) {
	db := openTestDB(t)
	repo := storage.NewPriceAI(db)
	product := createPriceAITestProduct(t, repo, "chatgpt-team-business")
	router := newPriceAITestRouter(t, repo, storage.NewShopTargets(db))

	invalidRec := performPriceAIRequest(router, http.MethodPost, "/api/priceai/watch-targets", `{"product_id":`+priceAIUintString(product.ID)+`,"target_price":10}`)
	if invalidRec.Code != http.StatusBadRequest {
		t.Fatalf("invalid target status = %d, body = %s", invalidRec.Code, invalidRec.Body.String())
	}

	createRec := performPriceAIRequest(router, http.MethodPost, "/api/priceai/watch-targets", `{"product_id":`+priceAIUintString(product.ID)+`,"notify_enabled":true,"target_price":10,"target_price_currency":"USD","price_drop_percent":5}`)
	if createRec.Code != http.StatusOK {
		t.Fatalf("create watch status = %d, body = %s", createRec.Code, createRec.Body.String())
	}
	var createResp struct {
		Data storage.PriceAIWatchTarget `json:"data"`
	}
	if err := json.Unmarshal(createRec.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("decode watch create: %v", err)
	}
	if !createResp.Data.MonitorEnabled || !createResp.Data.NotifyEnabled || createResp.Data.TargetPrice == nil || *createResp.Data.TargetPrice != 10 {
		t.Fatalf("created target = %#v", createResp.Data)
	}

	updateRec := performPriceAIRequest(router, http.MethodPut, "/api/priceai/watch-targets/"+priceAIUintString(createResp.Data.ID), `{"monitor_enabled":false}`)
	if updateRec.Code != http.StatusOK {
		t.Fatalf("update watch status = %d, body = %s", updateRec.Code, updateRec.Body.String())
	}
	deleteRec := performPriceAIRequest(router, http.MethodDelete, "/api/priceai/watch-targets/"+priceAIUintString(createResp.Data.ID), "")
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("delete watch status = %d, body = %s", deleteRec.Code, deleteRec.Body.String())
	}
	listRec := performPriceAIRequest(router, http.MethodGet, "/api/priceai/watch-targets", "")
	if listRec.Code != http.StatusOK || !strings.Contains(listRec.Body.String(), `"data":[]`) {
		t.Fatalf("watch list status = %d, body = %s", listRec.Code, listRec.Body.String())
	}
}

func TestPriceAIOfferShopTargetCreatesAndReusesExactTarget(t *testing.T) {
	db := openTestDB(t)
	repo := storage.NewPriceAI(db)
	targets := storage.NewShopTargets(db)
	product := createPriceAITestProduct(t, repo, "chatgpt-plus-exact")
	first := createPriceAITestOffer(t, repo, product.ID, "offer-a", "source-a", "Exact A", "https://pay.ldxp.cn/item/item-a", 8)
	second := createPriceAITestOffer(t, repo, product.ID, "offer-b", "source-a", "Exact B", "https://pay.ldxp.cn/item/item-b", 8.5)
	stubPriceAIItemResolver(t, "store-token")
	router := newPriceAITestRouter(t, repo, targets)

	firstRec := performPriceAIRequest(router, http.MethodPost, "/api/priceai/offers/"+priceAIUintString(first.ID)+"/shop-target", "")
	if firstRec.Code != http.StatusOK {
		t.Fatalf("first exact target status = %d, body = %s", firstRec.Code, firstRec.Body.String())
	}
	var firstResp struct {
		Data priceAIOfferShopTargetResult `json:"data"`
	}
	if err := json.Unmarshal(firstRec.Body.Bytes(), &firstResp); err != nil {
		t.Fatalf("decode first exact target: %v", err)
	}
	if !firstResp.Data.Created || firstResp.Data.Reused || firstResp.Data.Target == nil || firstResp.Data.Target.ScopeMode != storage.ShopScopeGoodsKeys || firstResp.Data.GoodsKey != "item-a" {
		t.Fatalf("first target response = %#v", firstResp.Data)
	}
	binding, err := repo.FindLDXPTargetBinding(storage.ShopPlatformLDXP, "https://pay.ldxp.cn", "store-token")
	if err != nil || binding == nil || binding.ShopTargetID != firstResp.Data.Target.ID {
		t.Fatalf("binding = %#v, err = %v", binding, err)
	}

	retryRec := performPriceAIRequest(router, http.MethodPost, "/api/priceai/offers/"+priceAIUintString(first.ID)+"/shop-target", "")
	if retryRec.Code != http.StatusOK {
		t.Fatalf("retry exact target status = %d, body = %s", retryRec.Code, retryRec.Body.String())
	}
	var retryResp struct {
		Data priceAIOfferShopTargetResult `json:"data"`
	}
	if err := json.Unmarshal(retryRec.Body.Bytes(), &retryResp); err != nil {
		t.Fatalf("decode retry exact target: %v", err)
	}
	if !retryResp.Data.Reused || !retryResp.Data.AlreadyIncluded || retryResp.Data.Target == nil || retryResp.Data.Target.ID != firstResp.Data.Target.ID {
		t.Fatalf("retry target response = %#v", retryResp.Data)
	}

	secondRec := performPriceAIRequest(router, http.MethodPost, "/api/priceai/offers/"+priceAIUintString(second.ID)+"/shop-target", "")
	if secondRec.Code != http.StatusOK {
		t.Fatalf("second exact target status = %d, body = %s", secondRec.Code, secondRec.Body.String())
	}
	stored, err := targets.FindByID(firstResp.Data.Target.ID)
	if err != nil {
		t.Fatalf("load reused target: %v", err)
	}
	if stored.GoodsKeysJSON != `["item-a","item-b"]` {
		t.Fatalf("exact keys = %s", stored.GoodsKeysJSON)
	}
	list, err := targets.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("targets = %#v, err = %v", list, err)
	}
}

func TestPriceAIOfferShopTargetAppendsToExplicitFilterTargetWithoutBinding(t *testing.T) {
	db := openTestDB(t)
	repo := storage.NewPriceAI(db)
	targets := storage.NewShopTargets(db)
	product := createPriceAITestProduct(t, repo, "chatgpt-filter-exact")
	offer := createPriceAITestOffer(t, repo, product.ID, "offer-filter", "source-filter", "Exact filter", "https://pay.ldxp.cn/item/item-filter", 8)
	existing := &storage.ShopTarget{
		Name:           "existing filtered shop",
		Platform:       storage.ShopPlatformLDXP,
		SiteURL:        "https://pay.ldxp.cn/shop/store-token",
		BaseURL:        "https://pay.ldxp.cn",
		Token:          "store-token",
		MonitorEnabled: true,
		ScopeMode:      storage.ShopScopeFilters,
		GoodsTypesJSON: `["card"]`,
		KeywordsJSON:   `["existing filter"]`,
		GoodsKeysJSON:  `["existing-item"]`,
		GoodsSort:      "category",
	}
	if err := targets.Create(existing); err != nil {
		t.Fatalf("create explicit target: %v", err)
	}
	stubPriceAIItemResolver(t, "store-token")
	router := newPriceAITestRouter(t, repo, targets)

	rec := performPriceAIRequest(router, http.MethodPost, "/api/priceai/offers/"+priceAIUintString(offer.ID)+"/shop-target", `{"shop_target_id":`+priceAIUintString(existing.ID)+`}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("explicit exact target status = %d, body = %s", rec.Code, rec.Body.String())
	}
	stored, err := targets.FindByID(existing.ID)
	if err != nil {
		t.Fatalf("load explicit target: %v", err)
	}
	if stored.ScopeMode != storage.ShopScopeFilters || stored.KeywordsJSON != `["existing filter"]` || stored.GoodsKeysJSON != `["existing-item","item-filter"]` {
		t.Fatalf("explicit target unexpectedly widened = %#v", stored)
	}
	binding, err := repo.FindLDXPTargetBinding(storage.ShopPlatformLDXP, "https://pay.ldxp.cn", "store-token")
	if err != nil || binding != nil {
		t.Fatalf("manual target binding = %#v, err = %v", binding, err)
	}
}

func TestPriceAIOfferShopTargetRejectsUntrustedOrMismatchedTarget(t *testing.T) {
	db := openTestDB(t)
	repo := storage.NewPriceAI(db)
	targets := storage.NewShopTargets(db)
	product := createPriceAITestProduct(t, repo, "chatgpt-reject-exact")
	unsafe := createPriceAITestOffer(t, repo, product.ID, "offer-unsafe", "source-unsafe", "Unsafe", "https://example.invalid/item/item-unsafe", 8)
	safe := createPriceAITestOffer(t, repo, product.ID, "offer-safe", "source-safe", "Safe", "https://pay.ldxp.cn/item/item-safe", 8)
	mismatch := &storage.ShopTarget{
		Name:           "other shop",
		Platform:       storage.ShopPlatformLDXP,
		SiteURL:        "https://pay.ldxp.cn/shop/other-token",
		BaseURL:        "https://pay.ldxp.cn",
		Token:          "other-token",
		MonitorEnabled: true,
		ScopeMode:      storage.ShopScopeFilters,
		GoodsKeysJSON:  `["keep-me"]`,
		GoodsSort:      "category",
	}
	if err := targets.Create(mismatch); err != nil {
		t.Fatalf("create mismatch target: %v", err)
	}
	stubPriceAIItemResolver(t, "store-token")
	router := newPriceAITestRouter(t, repo, targets)

	unsafeRec := performPriceAIRequest(router, http.MethodPost, "/api/priceai/offers/"+priceAIUintString(unsafe.ID)+"/shop-target", "")
	if unsafeRec.Code != http.StatusBadRequest {
		t.Fatalf("unsafe offer status = %d, body = %s", unsafeRec.Code, unsafeRec.Body.String())
	}
	mismatchRec := performPriceAIRequest(router, http.MethodPost, "/api/priceai/offers/"+priceAIUintString(safe.ID)+"/shop-target", `{"shop_target_id":`+priceAIUintString(mismatch.ID)+`}`)
	if mismatchRec.Code != http.StatusBadRequest {
		t.Fatalf("mismatched target status = %d, body = %s", mismatchRec.Code, mismatchRec.Body.String())
	}
	stored, err := targets.FindByID(mismatch.ID)
	if err != nil {
		t.Fatalf("load mismatch target: %v", err)
	}
	if stored.GoodsKeysJSON != `["keep-me"]` {
		t.Fatalf("mismatch target was changed: %s", stored.GoodsKeysJSON)
	}
}

func TestPriceAIRoutesRequireRepository(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	registerPriceAI(router.Group("/api"), &Deps{})
	rec := performPriceAIRequest(router, http.MethodGet, "/api/priceai/status", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func ptrTime(value time.Time) *time.Time { return &value }

func priceAIUintString(value uint) string {
	return strconv.FormatUint(uint64(value), 10)
}
