package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ifty-r/upstream-ops/backend/shopprovider"
	"github.com/ifty-r/upstream-ops/backend/storage"
	"gorm.io/gorm"
)

const priceAIPublicBoardCoverage = "报价仅来自 PriceAI 当前公开 Top 5 / 预设榜单，不代表完整商家报价库。"

func registerPriceAI(g *gin.RouterGroup, d *Deps) {
	group := g.Group("/priceai")
	group.GET("/status", func(c *gin.Context) { priceAIStatus(c, d) })
	group.POST("/sync", func(c *gin.Context) { syncPriceAI(c, d) })
	group.POST("/risk-refresh", func(c *gin.Context) { refreshPriceAIRisk(c, d) })
	group.GET("/products", func(c *gin.Context) { listPriceAIProducts(c, d) })
	group.GET("/products/:slug", func(c *gin.Context) { getPriceAIProduct(c, d) })
	group.GET("/products/:slug/offers", func(c *gin.Context) { listPriceAIOffers(c, d) })
	group.GET("/products/:slug/history", func(c *gin.Context) { listPriceAIHistory(c, d) })
	group.GET("/products/:slug/change-logs", func(c *gin.Context) { listPriceAIChangeLogs(c, d) })
	group.POST("/offers/:offer_id/shop-target", func(c *gin.Context) { createPriceAIOfferShopTarget(c, d) })
	group.GET("/watch-targets", func(c *gin.Context) { listPriceAIWatchTargets(c, d) })
	group.POST("/watch-targets", func(c *gin.Context) { createPriceAIWatchTarget(c, d) })
	group.PUT("/watch-targets/:id", func(c *gin.Context) { updatePriceAIWatchTarget(c, d) })
	group.DELETE("/watch-targets/:id", func(c *gin.Context) { deletePriceAIWatchTarget(c, d) })
}

func priceAIReady(c *gin.Context, d *Deps) bool {
	if d == nil || d.PriceAI == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "PriceAI service is not configured"})
		return false
	}
	return true
}

func priceAIServiceReady(c *gin.Context, d *Deps) bool {
	if !priceAIReady(c, d) {
		return false
	}
	if d.PriceAISvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "PriceAI sync service is not configured"})
		return false
	}
	return true
}

func priceAIShopTargetReady(c *gin.Context, d *Deps) bool {
	if !priceAIReady(c, d) {
		return false
	}
	if d.ShopTargets == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "shop targets repository is not configured"})
		return false
	}
	return true
}

func priceAIStatus(c *gin.Context, d *Deps) {
	if !priceAIReady(c, d) {
		return
	}
	state, err := d.PriceAI.FindFeedState("price-radar")
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	feedLogs, _, err := d.PriceAI.ListSyncLogs(storage.PriceAISyncJobFeed, 1, 1)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	riskLogs, _, err := d.PriceAI.ListSyncLogs(storage.PriceAISyncJobRisk, 1, 1)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	data := gin.H{"state": state, "feed_log": nil, "risk_log": nil}
	if len(feedLogs) > 0 {
		data["feed_log"] = feedLogs[0]
	}
	if len(riskLogs) > 0 {
		data["risk_log"] = riskLogs[0]
	}
	c.JSON(http.StatusOK, gin.H{"data": data})
}

func syncPriceAI(c *gin.Context, d *Deps) {
	if !priceAIServiceReady(c, d) {
		return
	}
	result, err := d.PriceAISvc.Sync(c.Request.Context())
	if err != nil {
		fail(c, http.StatusBadGateway, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": result})
}

func refreshPriceAIRisk(c *gin.Context, d *Deps) {
	if !priceAIServiceReady(c, d) {
		return
	}
	result, err := d.PriceAISvc.RefreshRisk(c.Request.Context())
	if err != nil {
		fail(c, http.StatusBadGateway, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": result})
}

var errPriceAIShopTargetIdentity = errors.New("shop target does not belong to the resolved LDXP shop")

// parsePriceAIShopURLContext is replaceable in API tests so route coverage does
// not need a live LDXP item resolver. Production always uses the provider.
var parsePriceAIShopURLContext = shopprovider.ParseShopURLContext

type priceAIOfferShopTargetInput struct {
	ShopTargetID *uint `json:"shop_target_id"`
}

type priceAIOfferShopTargetResult struct {
	Target          *storage.ShopTarget `json:"target"`
	GoodsKey        string              `json:"goods_key"`
	Created         bool                `json:"created"`
	Reused          bool                `json:"reused"`
	AlreadyIncluded bool                `json:"already_included"`
}

// createPriceAIOfferShopTarget accepts only a persisted offer ID. It never
// accepts a remote URL from the client, which keeps the LDXP resolver behind
// the imported PriceAI offer trust boundary.
func createPriceAIOfferShopTarget(c *gin.Context, d *Deps) {
	if !priceAIShopTargetReady(c, d) {
		return
	}
	offerID, ok := parseUintParam(c, "offer_id")
	if !ok {
		return
	}
	var input priceAIOfferShopTargetInput
	if err := c.ShouldBindJSON(&input); err != nil && !errors.Is(err, io.EOF) {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if input.ShopTargetID != nil && *input.ShopTargetID == 0 {
		fail(c, http.StatusBadRequest, fmt.Errorf("shop_target_id must be a positive integer"))
		return
	}
	offer, err := d.PriceAI.FindOfferByID(offerID)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if offer == nil {
		fail(c, http.StatusNotFound, fmt.Errorf("PriceAI offer not found"))
		return
	}
	goodsKey, err := shopprovider.LDXPItemGoodsKey(offer.URL)
	if err != nil {
		fail(c, http.StatusBadRequest, fmt.Errorf("offer is not an eligible LDXP item: %w", err))
		return
	}
	parsed, err := parsePriceAIShopURLContext(c.Request.Context(), offer.URL)
	if err != nil {
		failShopUpstream(c, err)
		return
	}
	if parsed.Platform != storage.ShopPlatformLDXP || parsed.GoodsKey != goodsKey || strings.TrimSpace(parsed.BaseURL) == "" || strings.TrimSpace(parsed.Token) == "" {
		fail(c, http.StatusBadRequest, fmt.Errorf("unable to establish an exact LDXP item monitor"))
		return
	}
	result, err := bindPriceAIOfferShopTarget(d, parsed, goodsKey, input.ShopTargetID)
	if err != nil {
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			fail(c, http.StatusNotFound, fmt.Errorf("shop target not found"))
		case errors.Is(err, errPriceAIShopTargetIdentity):
			fail(c, http.StatusBadRequest, err)
		default:
			fail(c, http.StatusInternalServerError, err)
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": result})
}

func bindPriceAIOfferShopTarget(d *Deps, parsed *shopprovider.ParsedURL, goodsKey string, requestedTargetID *uint) (*priceAIOfferShopTargetResult, error) {
	if d == nil || d.PriceAI == nil || d.ShopTargets == nil || parsed == nil {
		return nil, fmt.Errorf("priceai shop target dependencies are not configured")
	}
	result := &priceAIOfferShopTargetResult{}
	err := d.ShopTargets.TransactionWithPriceAI(d.PriceAI, func(targets *storage.ShopTargets, priceAI *storage.PriceAI) error {
		if requestedTargetID != nil {
			target, err := targets.FindByID(*requestedTargetID)
			if err != nil {
				return err
			}
			if !priceAIShopTargetMatchesParsedURL(target, parsed) {
				return errPriceAIShopTargetIdentity
			}
			alreadyIncluded, err := appendPriceAIExactGoodsKey(target, goodsKey)
			if err != nil {
				return err
			}
			if !alreadyIncluded {
				if err := targets.Update(target); err != nil {
					return err
				}
			}
			*result = priceAIOfferShopTargetResult{Target: target, GoodsKey: goodsKey, Reused: true, AlreadyIncluded: alreadyIncluded}
			return nil
		}

		binding, err := priceAI.FindLDXPTargetBinding(parsed.Platform, parsed.BaseURL, parsed.Token)
		if err != nil {
			return err
		}
		if binding != nil {
			target, err := targets.FindByID(binding.ShopTargetID)
			if err != nil {
				return err
			}
			if !priceAIShopTargetMatchesParsedURL(target, parsed) {
				return errPriceAIShopTargetIdentity
			}
			alreadyIncluded, err := appendPriceAIExactGoodsKey(target, goodsKey)
			if err != nil {
				return err
			}
			if !alreadyIncluded {
				if err := targets.Update(target); err != nil {
					return err
				}
			}
			*result = priceAIOfferShopTargetResult{Target: target, GoodsKey: goodsKey, Reused: true, AlreadyIncluded: alreadyIncluded}
			return nil
		}

		target := newPriceAIExactShopTarget(parsed, goodsKey)
		if err := targets.Create(target); err != nil {
			return err
		}
		if err := priceAI.CreateLDXPTargetBinding(&storage.PriceAILDXPTargetBinding{
			Platform:     parsed.Platform,
			BaseURL:      parsed.BaseURL,
			Token:        parsed.Token,
			ShopTargetID: target.ID,
		}); err != nil {
			return err
		}
		*result = priceAIOfferShopTargetResult{Target: target, GoodsKey: goodsKey, Created: true}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func newPriceAIExactShopTarget(parsed *shopprovider.ParsedURL, goodsKey string) *storage.ShopTarget {
	name := fmt.Sprintf("PriceAI LDXP %s %s", strings.TrimSpace(parsed.Token), strings.TrimSpace(goodsKey))
	nameRunes := []rune(name)
	if len(nameRunes) > 128 {
		name = string(nameRunes[:128])
	}
	return &storage.ShopTarget{
		Name:                name,
		Platform:            parsed.Platform,
		SiteURL:             parsed.SiteURL,
		BaseURL:             parsed.BaseURL,
		Token:               parsed.Token,
		MonitorEnabled:      true,
		NotifyEnabled:       false,
		ScopeMode:           storage.ShopScopeGoodsKeys,
		GoodsTypesJSON:      mustJSON([]string{"card"}),
		GoodsKeysJSON:       mustJSON([]string{goodsKey}),
		PriceChangeEnabled:  true,
		StockChangeEnabled:  true,
		LowStockEnabled:     true,
		RestockEnabled:      true,
		NewGoodsEnabled:     true,
		RemovedGoodsEnabled: true,
		GoodsSort:           "category",
	}
}

func priceAIShopTargetMatchesParsedURL(target *storage.ShopTarget, parsed *shopprovider.ParsedURL) bool {
	if target == nil || parsed == nil {
		return false
	}
	baseURL := strings.TrimRight(strings.ToLower(strings.TrimSpace(target.BaseURL)), "/")
	parsedBaseURL := strings.TrimRight(strings.ToLower(strings.TrimSpace(parsed.BaseURL)), "/")
	return target.Platform == parsed.Platform && baseURL == parsedBaseURL && strings.TrimSpace(target.Token) == strings.TrimSpace(parsed.Token)
}

func appendPriceAIExactGoodsKey(target *storage.ShopTarget, goodsKey string) (bool, error) {
	if target == nil {
		return false, fmt.Errorf("shop target is required")
	}
	goodsKey = strings.TrimSpace(goodsKey)
	if goodsKey == "" {
		return false, fmt.Errorf("goods key is required")
	}
	var stored []string
	if raw := strings.TrimSpace(target.GoodsKeysJSON); raw != "" {
		if err := json.Unmarshal([]byte(raw), &stored); err != nil {
			return false, fmt.Errorf("decode shop target goods keys: %w", err)
		}
	}
	keys := make([]string, 0, len(stored)+1)
	seen := make(map[string]struct{}, len(stored)+1)
	alreadyIncluded := false
	for _, key := range stored {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if key == goodsKey {
			alreadyIncluded = true
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	if !alreadyIncluded {
		keys = append(keys, goodsKey)
	}
	target.GoodsKeysJSON = mustJSON(keys)
	return alreadyIncluded, nil
}

type priceAIProductListItem struct {
	storage.PriceAIProduct
	WatchTargetID *uint      `json:"watch_target_id,omitempty"`
	Watched       bool       `json:"watched"`
	RiskFetchedAt *time.Time `json:"risk_fetched_at,omitempty"`
}

func listPriceAIProducts(c *gin.Context, d *Deps) {
	if !priceAIReady(c, d) {
		return
	}
	page, pageSize, err := parsePageQuery(c)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	watchState := strings.TrimSpace(c.DefaultQuery("watch_state", "all"))
	if watchState != "all" && watchState != "watched" && watchState != "unwatched" {
		fail(c, http.StatusBadRequest, fmt.Errorf("invalid watch_state"))
		return
	}
	availability := strings.TrimSpace(c.DefaultQuery("availability", "all"))
	if availability != "all" && availability != "in_stock" && availability != "out_of_stock" {
		fail(c, http.StatusBadRequest, fmt.Errorf("invalid availability"))
		return
	}
	sortOrder := strings.TrimSpace(c.DefaultQuery("sort", "latest_seen_desc"))
	if !isPriceAIProductSort(sortOrder) {
		fail(c, http.StatusBadRequest, fmt.Errorf("invalid product sort"))
		return
	}
	products, total, err := d.PriceAI.ListProductsPage(storage.PriceAIProductPageOptions{
		Page:           page,
		PageSize:       pageSize,
		Query:          c.Query("query"),
		Platform:       c.Query("platform"),
		ProductType:    c.Query("product_type"),
		WatchState:     watchState,
		Availability:   availability,
		IncludeMissing: c.Query("include_missing") == "true",
		Sort:           sortOrder,
	})
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	items := make([]priceAIProductListItem, 0, len(products))
	for _, product := range products {
		item := priceAIProductListItem{PriceAIProduct: product}
		target, err := d.PriceAI.FindWatchTargetByProductID(product.ID)
		if err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		if target != nil {
			id := target.ID
			item.WatchTargetID = &id
			item.Watched = true
		}
		feedback, err := d.PriceAI.ListRiskFeedback(product.ID)
		if err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		for _, record := range feedback {
			if record.FetchedAt != nil && (item.RiskFetchedAt == nil || record.FetchedAt.After(*item.RiskFetchedAt)) {
				item.RiskFetchedAt = record.FetchedAt
			}
		}
		items = append(items, item)
	}
	c.JSON(http.StatusOK, gin.H{"data": priceAIPage(items, total, page, pageSize)})
}

func isPriceAIProductSort(value string) bool {
	switch value {
	case "latest_seen_desc", "lowest_price_asc", "lowest_price_desc", "name_asc", "in_stock_desc":
		return true
	default:
		return false
	}
}

func getPriceAIProduct(c *gin.Context, d *Deps) {
	if !priceAIReady(c, d) {
		return
	}
	product, err := findPriceAIProduct(c, d)
	if err != nil {
		return
	}
	target, err := d.PriceAI.FindWatchTargetByProductID(product.ID)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	presets, err := d.PriceAI.ListPresets(product.ID)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	feedback, err := d.PriceAI.ListRiskFeedback(product.ID)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"product":            product,
		"watch_target":       target,
		"presets":            presets,
		"risk_feedback":      priceAIRiskDTOs(feedback),
		"source_product_url": "https://priceai.cc/products/" + url.PathEscape(product.Slug),
		"coverage":           priceAIPublicBoardCoverage,
	}})
}

func listPriceAIHistory(c *gin.Context, d *Deps) {
	if !priceAIReady(c, d) {
		return
	}
	product, err := findPriceAIProduct(c, d)
	if err != nil {
		return
	}
	page, pageSize, err := parsePageQuery(c)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	items, total, err := d.PriceAI.ListProductHistory(product.ID, page, pageSize)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": priceAIPage(items, total, page, pageSize)})
}

func listPriceAIChangeLogs(c *gin.Context, d *Deps) {
	if !priceAIReady(c, d) {
		return
	}
	product, err := findPriceAIProduct(c, d)
	if err != nil {
		return
	}
	page, pageSize, err := parsePageQuery(c)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	items, total, err := d.PriceAI.ListChangeLogs(product.ID, page, pageSize)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": priceAIPage(items, total, page, pageSize)})
}

func findPriceAIProduct(c *gin.Context, d *Deps) (*storage.PriceAIProduct, error) {
	slug := strings.TrimSpace(c.Param("slug"))
	if slug == "" {
		fail(c, http.StatusBadRequest, fmt.Errorf("product slug is required"))
		return nil, fmt.Errorf("product slug is required")
	}
	product, err := d.PriceAI.FindProductBySlug(slug)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return nil, err
	}
	if product == nil {
		fail(c, http.StatusNotFound, fmt.Errorf("PriceAI product not found"))
		return nil, fmt.Errorf("PriceAI product not found")
	}
	return product, nil
}

func priceAIPage(items any, total int64, page, pageSize int) gin.H {
	pages := 1
	if total > 0 {
		pages = int((total + int64(pageSize) - 1) / int64(pageSize))
	}
	return gin.H{"items": items, "total": total, "page": page, "page_size": pageSize, "pages": pages}
}

type priceAIWatchTargetInput struct {
	ProductID                   uint     `json:"product_id"`
	MonitorEnabled              *bool    `json:"monitor_enabled"`
	NotifyEnabled               *bool    `json:"notify_enabled"`
	TargetPrice                 *float64 `json:"target_price"`
	TargetPriceCurrency         *string  `json:"target_price_currency"`
	ClearTargetPrice            bool     `json:"clear_target_price"`
	PriceDropPercent            *float64 `json:"price_drop_percent"`
	NotificationCooldownMinutes *int     `json:"notification_cooldown_minutes"`
}

func createPriceAIWatchTarget(c *gin.Context, d *Deps) {
	if !priceAIReady(c, d) {
		return
	}
	var input priceAIWatchTargetInput
	if err := c.ShouldBindJSON(&input); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if input.ProductID == 0 {
		fail(c, http.StatusBadRequest, fmt.Errorf("product_id is required"))
		return
	}
	product, err := d.PriceAI.FindProductByID(input.ProductID)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if product == nil {
		fail(c, http.StatusNotFound, fmt.Errorf("PriceAI product not found"))
		return
	}
	existing, err := d.PriceAI.FindWatchTargetByProductID(product.ID)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if existing != nil {
		fail(c, http.StatusConflict, fmt.Errorf("PriceAI watch target already exists for this product"))
		return
	}
	target := &storage.PriceAIWatchTarget{ProductID: product.ID, MonitorEnabled: true, BaselineSnapshotID: product.LastSnapshotID}
	if err := applyPriceAIWatchInput(target, input, true); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if err := d.PriceAI.CreateWatchTarget(target); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": target})
}

func updatePriceAIWatchTarget(c *gin.Context, d *Deps) {
	if !priceAIReady(c, d) {
		return
	}
	id, err := uintParam(c, "id")
	if err != nil || id == 0 {
		fail(c, http.StatusBadRequest, fmt.Errorf("invalid watch target id"))
		return
	}
	target, err := d.PriceAI.FindWatchTargetByID(id)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if target == nil {
		fail(c, http.StatusNotFound, fmt.Errorf("PriceAI watch target not found"))
		return
	}
	var input priceAIWatchTargetInput
	if err := c.ShouldBindJSON(&input); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if input.ProductID != 0 && input.ProductID != target.ProductID {
		fail(c, http.StatusBadRequest, fmt.Errorf("watch target product_id cannot change"))
		return
	}
	if err := applyPriceAIWatchInput(target, input, false); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if err := d.PriceAI.UpdateWatchTarget(target); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": target})
}

func applyPriceAIWatchInput(target *storage.PriceAIWatchTarget, input priceAIWatchTargetInput, creating bool) error {
	if target == nil {
		return fmt.Errorf("watch target is required")
	}
	if input.ClearTargetPrice {
		if input.TargetPrice != nil || input.TargetPriceCurrency != nil {
			return fmt.Errorf("clear_target_price cannot be combined with target price fields")
		}
		target.TargetPrice = nil
		target.TargetPriceCurrency = nil
	} else if input.TargetPrice != nil || input.TargetPriceCurrency != nil {
		if input.TargetPrice == nil || input.TargetPriceCurrency == nil || strings.TrimSpace(*input.TargetPriceCurrency) == "" {
			return fmt.Errorf("target_price and target_price_currency must be provided together")
		}
		if !finiteNonNegative(*input.TargetPrice) {
			return fmt.Errorf("target_price must be a finite non-negative number")
		}
		currency := strings.TrimSpace(*input.TargetPriceCurrency)
		target.TargetPrice = input.TargetPrice
		target.TargetPriceCurrency = &currency
	} else if creating && (target.TargetPrice != nil || target.TargetPriceCurrency != nil) {
		return fmt.Errorf("target_price and target_price_currency must be provided together")
	}
	if input.PriceDropPercent != nil {
		if math.IsNaN(*input.PriceDropPercent) || math.IsInf(*input.PriceDropPercent, 0) || *input.PriceDropPercent <= 0 || *input.PriceDropPercent > 100 {
			return fmt.Errorf("price_drop_percent must be greater than 0 and at most 100")
		}
		target.PriceDropPercent = input.PriceDropPercent
	}
	if input.MonitorEnabled != nil {
		target.MonitorEnabled = *input.MonitorEnabled
	}
	if input.NotifyEnabled != nil {
		target.NotifyEnabled = *input.NotifyEnabled
	}
	if input.NotificationCooldownMinutes != nil {
		if *input.NotificationCooldownMinutes < 0 || *input.NotificationCooldownMinutes > 7*24*60 {
			return fmt.Errorf("notification_cooldown_minutes must be between 0 and 10080")
		}
		target.NotificationCooldownMinutes = *input.NotificationCooldownMinutes
	}
	return nil
}

func finiteNonNegative(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

func deletePriceAIWatchTarget(c *gin.Context, d *Deps) {
	if !priceAIReady(c, d) {
		return
	}
	id, err := uintParam(c, "id")
	if err != nil || id == 0 {
		fail(c, http.StatusBadRequest, fmt.Errorf("invalid watch target id"))
		return
	}
	target, err := d.PriceAI.FindWatchTargetByID(id)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if target == nil {
		fail(c, http.StatusNotFound, fmt.Errorf("PriceAI watch target not found"))
		return
	}
	if err := d.PriceAI.DeleteWatchTarget(id); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"ok": true}})
}

func listPriceAIWatchTargets(c *gin.Context, d *Deps) {
	if !priceAIReady(c, d) {
		return
	}
	targets, err := d.PriceAI.ListWatchTargets()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	items := make([]gin.H, 0, len(targets))
	for _, target := range targets {
		product, err := d.PriceAI.FindProductByID(target.ProductID)
		if err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
		items = append(items, gin.H{"target": target, "product": product})
	}
	c.JSON(http.StatusOK, gin.H{"data": items})
}

type priceAIRiskFeedbackDTO struct {
	Scope           storage.PriceAIRiskScope `json:"scope"`
	SubjectRemoteID string                   `json:"subject_remote_id"`
	Status          string                   `json:"status,omitempty"`
	FeedbackCount   int                      `json:"feedback_count"`
	ReasonsJSON     string                   `json:"reasons_json,omitempty"`
	SummariesJSON   string                   `json:"summaries_json,omitempty"`
	LatestAt        *time.Time               `json:"latest_at,omitempty"`
	PageURL         string                   `json:"page_url,omitempty"`
	FetchedAt       *time.Time               `json:"fetched_at,omitempty"`
	LastError       string                   `json:"last_error,omitempty"`
}

func priceAIRiskDTOs(records []storage.PriceAIRiskFeedback) []priceAIRiskFeedbackDTO {
	items := make([]priceAIRiskFeedbackDTO, 0, len(records))
	for _, record := range records {
		items = append(items, priceAIRiskFeedbackDTO{
			Scope:           record.Scope,
			SubjectRemoteID: record.SubjectRemoteID,
			Status:          record.Status,
			FeedbackCount:   record.FeedbackCount,
			ReasonsJSON:     record.ReasonsJSON,
			SummariesJSON:   record.SummariesJSON,
			LatestAt:        record.LatestAt,
			PageURL:         record.PageURL,
			FetchedAt:       record.FetchedAt,
			LastError:       record.LastError,
		})
	}
	return items
}

type priceAIBoardMembership struct {
	BoardKind   storage.PriceAIBoardKind `json:"board_kind"`
	PresetID    string                   `json:"preset_id,omitempty"`
	Rank        int                      `json:"rank"`
	GeneratedAt time.Time                `json:"generated_at"`
}

type priceAIQuote struct {
	ID              uint                     `json:"id"`
	RemoteID        string                   `json:"remote_id,omitempty"`
	SourceID        string                   `json:"source_id,omitempty"`
	SourceName      string                   `json:"source_name,omitempty"`
	SourceStoreName string                   `json:"source_store_name,omitempty"`
	MerchantKey     string                   `json:"merchant_key"`
	Title           string                   `json:"title"`
	NormalizedTitle string                   `json:"normalized_title"`
	Price           float64                  `json:"price"`
	Currency        string                   `json:"currency,omitempty"`
	Status          string                   `json:"status,omitempty"`
	URL             string                   `json:"url"`
	Memberships     []priceAIBoardMembership `json:"memberships"`
	RiskFeedback    []priceAIRiskFeedbackDTO `json:"risk_feedback,omitempty"`
	LDXPEligible    bool                     `json:"ldxp_eligible"`
}

type priceAIQuoteGroup struct {
	Title             string         `json:"title"`
	NormalizedTitle   string         `json:"normalized_title"`
	MerchantCount     int            `json:"merchant_count"`
	VisibleQuoteCount int            `json:"visible_quote_count"`
	MinPrice          *float64       `json:"min_price,omitempty"`
	MaxPrice          *float64       `json:"max_price,omitempty"`
	PriceSpread       *float64       `json:"price_spread,omitempty"`
	Currency          string         `json:"currency,omitempty"`
	RiskBadgeCount    int            `json:"risk_badge_count"`
	Quotes            []priceAIQuote `json:"quotes"`
}

type priceAIQuoteGroupAccumulator struct {
	group      priceAIQuoteGroup
	merchants  map[string]struct{}
	mixedMoney bool
}

func listPriceAIOffers(c *gin.Context, d *Deps) {
	if !priceAIReady(c, d) {
		return
	}
	product, err := findPriceAIProduct(c, d)
	if err != nil {
		return
	}
	page, pageSize, err := parsePageQuery(c)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	board, presetID, err := parsePriceAIBoard(c.DefaultQuery("board", "default"))
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if groupBy := c.DefaultQuery("group_by", "title"); groupBy != "title" {
		fail(c, http.StatusBadRequest, fmt.Errorf("group_by must be title"))
		return
	}
	sortOrder := c.DefaultQuery("sort", "price_asc")
	if !isPriceAIQuoteSort(sortOrder) {
		fail(c, http.StatusBadRequest, fmt.Errorf("invalid quote sort"))
		return
	}
	groups, err := buildPriceAIQuoteGroups(d.PriceAI, product.ID, board, presetID, c.Query("query"), sortOrder)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	total := int64(len(groups))
	start := (page - 1) * pageSize
	if start > len(groups) {
		start = len(groups)
	}
	end := start + pageSize
	if end > len(groups) {
		end = len(groups)
	}
	data := priceAIPage(groups[start:end], total, page, pageSize)
	data["board"] = board
	data["preset_id"] = presetID
	data["coverage"] = priceAIPublicBoardCoverage
	c.JSON(http.StatusOK, gin.H{"data": data})
}

func parsePriceAIBoard(raw string) (string, string, error) {
	board := strings.TrimSpace(raw)
	switch board {
	case "default", "all":
		return board, "", nil
	}
	if strings.HasPrefix(board, "preset:") {
		presetID := strings.TrimSpace(strings.TrimPrefix(board, "preset:"))
		if presetID == "" {
			return "", "", fmt.Errorf("preset board id is required")
		}
		return "preset", presetID, nil
	}
	return "", "", fmt.Errorf("invalid board")
}

func isPriceAIQuoteSort(value string) bool {
	switch value {
	case "price_asc", "price_desc", "rank_asc", "risk_first":
		return true
	default:
		return false
	}
}

func buildPriceAIQuoteGroups(repo *storage.PriceAI, productID uint, board, presetID, query, sortOrder string) ([]priceAIQuoteGroup, error) {
	rows, err := repo.ListOfferBoardRows(productID)
	if err != nil {
		return nil, err
	}
	feedback, err := repo.ListRiskFeedback(productID)
	if err != nil {
		return nil, err
	}
	bySource := make(map[string][]priceAIRiskFeedbackDTO)
	byOffer := make(map[string][]priceAIRiskFeedbackDTO)
	for _, item := range priceAIRiskDTOs(feedback) {
		switch item.Scope {
		case storage.PriceAIRiskScopeSource:
			bySource[item.SubjectRemoteID] = append(bySource[item.SubjectRemoteID], item)
		case storage.PriceAIRiskScopeOffer:
			byOffer[item.SubjectRemoteID] = append(byOffer[item.SubjectRemoteID], item)
		}
	}
	needle := strings.ToLower(strings.TrimSpace(query))
	quotes := make(map[uint]*priceAIQuote)
	for _, row := range rows {
		if !matchesPriceAIBoard(row, board, presetID) || !matchesPriceAIQuoteQuery(row, needle) {
			continue
		}
		quote := quotes[row.OfferID]
		if quote == nil {
			quote = &priceAIQuote{
				ID:              row.OfferID,
				RemoteID:        row.RemoteID,
				SourceID:        row.SourceID,
				SourceName:      row.SourceName,
				SourceStoreName: row.SourceStoreName,
				MerchantKey:     row.MerchantKey,
				Title:           row.Title,
				NormalizedTitle: row.NormalizedTitle,
				Price:           row.Price,
				Currency:        row.Currency,
				Status:          row.Status,
				URL:             row.URL,
				LDXPEligible:    isLDXPEligibleOffer(row.URL),
			}
			quote.RiskFeedback = append(quote.RiskFeedback, bySource[row.SourceID]...)
			quote.RiskFeedback = append(quote.RiskFeedback, byOffer[row.RemoteID]...)
			quotes[row.OfferID] = quote
		}
		quote.Memberships = append(quote.Memberships, priceAIBoardMembership{BoardKind: row.BoardKind, PresetID: row.PresetID, Rank: row.Rank, GeneratedAt: row.BoardGeneratedAt})
	}
	accumulators := make(map[string]*priceAIQuoteGroupAccumulator)
	for _, quote := range quotes {
		key := quote.NormalizedTitle
		if key == "" {
			key = strings.ToLower(strings.TrimSpace(quote.Title))
		}
		if key == "" {
			key = fmt.Sprintf("offer-%d", quote.ID)
		}
		item := accumulators[key]
		if item == nil {
			item = &priceAIQuoteGroupAccumulator{group: priceAIQuoteGroup{Title: quote.Title, NormalizedTitle: quote.NormalizedTitle, Currency: quote.Currency}, merchants: make(map[string]struct{})}
			accumulators[key] = item
		}
		item.group.Quotes = append(item.group.Quotes, *quote)
		item.group.VisibleQuoteCount++
		if quote.MerchantKey != "" {
			item.merchants[quote.MerchantKey] = struct{}{}
		}
		if len(quote.RiskFeedback) > 0 {
			item.group.RiskBadgeCount++
		}
		if item.group.Currency != quote.Currency {
			item.mixedMoney = true
		}
		if item.group.MinPrice == nil || quote.Price < *item.group.MinPrice {
			value := quote.Price
			item.group.MinPrice = &value
		}
		if item.group.MaxPrice == nil || quote.Price > *item.group.MaxPrice {
			value := quote.Price
			item.group.MaxPrice = &value
		}
	}
	groups := make([]priceAIQuoteGroup, 0, len(accumulators))
	for _, item := range accumulators {
		item.group.MerchantCount = len(item.merchants)
		if item.mixedMoney {
			item.group.Currency = ""
			item.group.PriceSpread = nil
		} else if item.group.MinPrice != nil && item.group.MaxPrice != nil {
			spread := *item.group.MaxPrice - *item.group.MinPrice
			item.group.PriceSpread = &spread
		}
		sortPriceAIQuotes(item.group.Quotes, sortOrder)
		groups = append(groups, item.group)
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].MinPrice == nil {
			return false
		}
		if groups[j].MinPrice == nil {
			return true
		}
		if *groups[i].MinPrice == *groups[j].MinPrice {
			return groups[i].Title < groups[j].Title
		}
		if sortOrder == "price_desc" {
			return *groups[i].MinPrice > *groups[j].MinPrice
		}
		return *groups[i].MinPrice < *groups[j].MinPrice
	})
	return groups, nil
}

func matchesPriceAIBoard(row storage.PriceAIOfferBoardRow, board, presetID string) bool {
	switch board {
	case "all":
		return true
	case "default":
		return row.BoardKind == storage.PriceAIBoardDefault
	case "preset":
		return row.BoardKind == storage.PriceAIBoardPreset && row.PresetID == presetID
	default:
		return false
	}
}

func matchesPriceAIQuoteQuery(row storage.PriceAIOfferBoardRow, query string) bool {
	if query == "" {
		return true
	}
	return strings.Contains(strings.ToLower(row.Title), query) ||
		strings.Contains(strings.ToLower(row.SourceName), query) ||
		strings.Contains(strings.ToLower(row.SourceStoreName), query)
}

func isLDXPEligibleOffer(rawURL string) bool {
	_, err := shopprovider.LDXPItemGoodsKey(rawURL)
	return err == nil
}

func sortPriceAIQuotes(quotes []priceAIQuote, sortOrder string) {
	sort.Slice(quotes, func(i, j int) bool {
		left, right := quotes[i], quotes[j]
		switch sortOrder {
		case "risk_first":
			if len(left.RiskFeedback) != len(right.RiskFeedback) {
				return len(left.RiskFeedback) > len(right.RiskFeedback)
			}
		case "rank_asc":
			leftRank, rightRank := priceAIQuoteRank(left), priceAIQuoteRank(right)
			if leftRank != rightRank {
				return leftRank < rightRank
			}
		case "price_desc":
			if left.Price != right.Price {
				return left.Price > right.Price
			}
		default:
			if left.Price != right.Price {
				return left.Price < right.Price
			}
		}
		if left.Price != right.Price {
			return left.Price < right.Price
		}
		leftRank, rightRank := priceAIQuoteRank(left), priceAIQuoteRank(right)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		return left.SourceName < right.SourceName
	})
}

func priceAIQuoteRank(quote priceAIQuote) int {
	rank := math.MaxInt
	for _, membership := range quote.Memberships {
		if membership.Rank < rank {
			rank = membership.Rank
		}
	}
	return rank
}
