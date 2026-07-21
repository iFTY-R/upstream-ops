package api

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Public shop responses are intentionally separate from storage models. Keep
// this allowlist explicit so credentials and monitoring configuration cannot
// become public when the internal models gain fields.
type publicShopTarget struct {
	ID           uint   `json:"id"`
	Name         string `json:"name"`
	LastShopName string `json:"last_shop_name"`
	SiteURL      string `json:"site_url"`
}

type publicShopGoodsItem struct {
	ID                   uint       `json:"id"`
	TargetID             uint       `json:"target_id"`
	GoodsKey             string     `json:"goods_key"`
	Name                 string     `json:"name"`
	CategoryName         string     `json:"category_name"`
	Link                 string     `json:"link"`
	Price                float64    `json:"price"`
	StockCount           int        `json:"stock_count"`
	LimitCount           int        `json:"limit_count"`
	LastSeenAt           time.Time  `json:"last_seen_at"`
	RemovedAt            *time.Time `json:"removed_at"`
	TargetName           string     `json:"target_name"`
	TargetLastShopName   string     `json:"target_last_shop_name"`
	TargetSiteURL        string     `json:"target_site_url"`
	TargetStockThreshold int        `json:"target_stock_threshold"`
}

func registerPublicShopGoods(g *gin.RouterGroup, d *Deps) {
	g.GET("/shop-targets", func(c *gin.Context) { listPublicShopTargets(c, d) })
	g.GET("/shop-goods", func(c *gin.Context) { listPublicShopGoods(c, d) })
}

func listPublicShopTargets(c *gin.Context, d *Deps) {
	if d.ShopTargets == nil {
		failPublicShopRead(c, d, "shop targets", nil)
		return
	}
	list, err := d.ShopTargets.List()
	if err != nil {
		failPublicShopRead(c, d, "shop targets", err)
		return
	}
	out := make([]publicShopTarget, 0, len(list))
	for _, target := range list {
		out = append(out, publicShopTarget{
			ID:           target.ID,
			Name:         target.Name,
			LastShopName: target.LastShopName,
			SiteURL:      safePublicShopURL(target.SiteURL),
		})
	}
	c.JSON(http.StatusOK, gin.H{"data": out})
}

func listPublicShopGoods(c *gin.Context, d *Deps) {
	if d.ShopGoods == nil {
		failPublicShopRead(c, d, "shop goods", nil)
		return
	}
	page, pageSize := parsePageDefaults(c)
	filter, ok := parseShopGoodsFilter(c, 0)
	if !ok {
		return
	}
	if raw := strings.TrimSpace(c.Query("target_id")); raw != "" {
		targetID, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			fail(c, http.StatusBadRequest, fmt.Errorf("invalid target_id"))
			return
		}
		filter.TargetID = uint(targetID)
	}
	list, total, err := d.ShopGoods.ListAllPageFiltered(page, pageSize, filter)
	if err != nil {
		failPublicShopRead(c, d, "shop goods", err)
		return
	}
	out := make([]publicShopGoodsItem, 0, len(list))
	for _, item := range list {
		out = append(out, publicShopGoodsItem{
			ID:                   item.ID,
			TargetID:             item.TargetID,
			GoodsKey:             item.GoodsKey,
			Name:                 item.Name,
			CategoryName:         item.CategoryName,
			Link:                 safePublicShopURL(item.Link),
			Price:                item.Price,
			StockCount:           item.StockCount,
			LimitCount:           item.LimitCount,
			LastSeenAt:           item.LastSeenAt,
			RemovedAt:            item.RemovedAt,
			TargetName:           item.TargetName,
			TargetLastShopName:   item.TargetLastShopName,
			TargetSiteURL:        safePublicShopURL(item.TargetSiteURL),
			TargetStockThreshold: item.TargetStockThreshold,
		})
	}
	c.JSON(http.StatusOK, gin.H{"data": pageData(out, total, page, pageSize)})
}

func safePublicShopURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return ""
	}
	return parsed.String()
}

func failPublicShopRead(c *gin.Context, d *Deps, resource string, err error) {
	if d.Log != nil {
		d.Log.Error("public shop read failed", "resource", resource, "err", err)
	}
	c.JSON(http.StatusInternalServerError, gin.H{"error": "public shop data unavailable"})
}
