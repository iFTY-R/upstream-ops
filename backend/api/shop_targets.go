package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ifty-r/upstream-ops/backend/shopprovider"
	"github.com/ifty-r/upstream-ops/backend/storage"
	"gorm.io/gorm"
)

func registerShopTargets(g *gin.RouterGroup, d *Deps) {
	g.GET("/shop-goods", func(c *gin.Context) { listAllShopGoods(c, d) })
	gp := g.Group("/shop-targets")
	gp.GET("", func(c *gin.Context) { listShopTargets(c, d) })
	gp.POST("", func(c *gin.Context) { createShopTarget(c, d) })
	gp.POST("/parse-url", func(c *gin.Context) { parseShopURL(c, d) })
	gp.POST("/sync-all", func(c *gin.Context) { syncAllShopTargets(c, d) })
	gp.POST("/sync-jobs/status", func(c *gin.Context) { getShopSyncJobsStatus(c, d) })
	gp.POST("/reorder", func(c *gin.Context) { reorderShopTargets(c, d) })
	gp.POST("/bulk-notification", func(c *gin.Context) { bulkConfigureShopNotifications(c, d) })
	gp.GET("/:id", func(c *gin.Context) { getShopTarget(c, d) })
	gp.PUT("/:id", func(c *gin.Context) { updateShopTarget(c, d) })
	gp.DELETE("/:id", func(c *gin.Context) { deleteShopTarget(c, d) })
	gp.GET("/:id/watch-rules", func(c *gin.Context) { listShopWatchRules(c, d) })
	gp.POST("/:id/watch-rules", func(c *gin.Context) { createShopWatchRule(c, d) })
	gp.POST("/:id/watch-rules/preview", func(c *gin.Context) { previewShopWatchRule(c, d) })
	gp.PUT("/:id/watch-rules/:rule_id", func(c *gin.Context) { updateShopWatchRule(c, d) })
	gp.DELETE("/:id/watch-rules/:rule_id", func(c *gin.Context) { deleteShopWatchRule(c, d) })
	gp.POST("/:id/test", func(c *gin.Context) { testShopTarget(c, d) })
	gp.POST("/:id/sync", func(c *gin.Context) { syncShopTarget(c, d) })
	gp.GET("/:id/sync-jobs/latest", func(c *gin.Context) { getLatestShopSyncJob(c, d) })
	gp.GET("/:id/sync-jobs/:job_id", func(c *gin.Context) { getShopSyncJob(c, d) })
	gp.GET("/:id/categories", func(c *gin.Context) { shopTargetCategories(c, d) })
	gp.GET("/:id/snapshot-categories", func(c *gin.Context) { shopTargetSnapshotCategories(c, d) })
	gp.GET("/:id/goods", func(c *gin.Context) { shopTargetGoods(c, d) })
	gp.POST("/:id/goods/:goods_key/refresh", func(c *gin.Context) { refreshShopTargetGoods(c, d) })
	gp.GET("/:id/change-logs", func(c *gin.Context) { shopTargetChangeLogs(c, d) })
	gp.GET("/:id/monitor-logs", func(c *gin.Context) { shopTargetMonitorLogs(c, d) })
}

type shopTargetInput struct {
	Name                string                `json:"name"`
	Platform            storage.ShopPlatform  `json:"platform"`
	SiteURL             string                `json:"site_url"`
	BaseURL             string                `json:"base_url"`
	Token               string                `json:"token"`
	MonitorEnabled      *bool                 `json:"monitor_enabled"`
	NotifyEnabled       *bool                 `json:"notify_enabled"`
	ScopeMode           storage.ShopScopeMode `json:"scope_mode"`
	GoodsTypes          []string              `json:"goods_types"`
	CategoryIDs         []int64               `json:"category_ids"`
	CategoryNames       []string              `json:"category_names"`
	Keywords            []string              `json:"keywords"`
	GoodsKeys           []string              `json:"goods_keys"`
	StockThreshold      int                   `json:"stock_threshold"`
	PriceChangeEnabled  *bool                 `json:"price_change_enabled"`
	StockChangeEnabled  *bool                 `json:"stock_change_enabled"`
	LowStockEnabled     *bool                 `json:"low_stock_enabled"`
	RestockEnabled      *bool                 `json:"restock_enabled"`
	NewGoodsEnabled     *bool                 `json:"new_goods_enabled"`
	RemovedGoodsEnabled *bool                 `json:"removed_goods_enabled"`
	ProxyEnabled        *bool                 `json:"proxy_enabled"`
	SortOrder           int                   `json:"sort_order"`
	GoodsSort           string                `json:"goods_sort"`
}

type parseShopURLInput struct {
	SiteURL string `json:"site_url" binding:"required"`
}

type shopTargetReorderInput struct {
	Items []struct {
		ID        uint `json:"id"`
		SortOrder int  `json:"sort_order"`
	} `json:"items"`
}

type shopBulkNotificationInput struct {
	TargetIDs       []uint             `json:"target_ids"`
	NotifyEnabled   *bool              `json:"notify_enabled"`
	UpsertRule      bool               `json:"upsert_rule"`
	ReplaceSameName bool               `json:"replace_same_name"`
	Rule            shopWatchRuleInput `json:"rule"`
}

type shopBulkNotificationResult struct {
	UpdatedTargets int                  `json:"updated_targets"`
	CreatedRules   int                  `json:"created_rules"`
	UpdatedRules   int                  `json:"updated_rules"`
	Targets        []storage.ShopTarget `json:"targets"`
}

type shopSyncAllJobResult struct {
	TargetID uint                 `json:"target_id"`
	Name     string               `json:"name"`
	Job      *storage.ShopSyncJob `json:"job,omitempty"`
	Reused   bool                 `json:"reused,omitempty"`
	Error    string               `json:"error,omitempty"`
}

type shopSyncAllJobsResult struct {
	Total   int                    `json:"total"`
	Queued  int                    `json:"queued"`
	Reused  int                    `json:"reused"`
	Failed  int                    `json:"failed"`
	Targets []shopSyncAllJobResult `json:"targets"`
}

type shopSyncJobsStatusInput struct {
	JobIDs []uint `json:"job_ids" binding:"required"`
}

func listShopTargets(c *gin.Context, d *Deps) {
	if d.ShopTargets == nil {
		fail(c, http.StatusInternalServerError, fmt.Errorf("shop targets repository is not configured"))
		return
	}
	list, err := d.ShopTargets.List()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if !attachShopWatchRuleCounts(c, d, list) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": list})
}

func createShopTarget(c *gin.Context, d *Deps) {
	if !shopReposReady(c, d) {
		return
	}
	var in shopTargetInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	target, err := buildShopTarget(in, nil)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if err := d.ShopTargets.Create(target); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": target})
}

func reorderShopTargets(c *gin.Context, d *Deps) {
	if d.ShopTargets == nil {
		fail(c, http.StatusInternalServerError, fmt.Errorf("shop targets repository is not configured"))
		return
	}
	var in shopTargetReorderInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	orders := make(map[uint]int, len(in.Items))
	for _, item := range in.Items {
		if item.ID == 0 {
			fail(c, http.StatusBadRequest, fmt.Errorf("invalid shop target id"))
			return
		}
		orders[item.ID] = item.SortOrder
	}
	if err := d.ShopTargets.UpdateSortOrders(orders); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	list, err := d.ShopTargets.List()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if !attachShopWatchRuleCounts(c, d, list) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": list})
}

func bulkConfigureShopNotifications(c *gin.Context, d *Deps) {
	if !shopWatchReposReady(c, d) {
		return
	}
	var in shopBulkNotificationInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	targetIDs := cleanUintIDs(in.TargetIDs)
	if len(targetIDs) == 0 {
		fail(c, http.StatusBadRequest, fmt.Errorf("请选择至少一个店铺"))
		return
	}
	if in.NotifyEnabled == nil && !in.UpsertRule {
		fail(c, http.StatusBadRequest, fmt.Errorf("请选择要批量修改的通知配置"))
		return
	}

	if in.UpsertRule {
		if _, err := buildShopWatchRule(0, in.Rule, nil); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
	}
	result := shopBulkNotificationResult{}
	if err := d.ShopTargets.Transaction(func(targets *storage.ShopTargets, rules *storage.ShopWatchRules) error {
		for _, targetID := range targetIDs {
			target, err := targets.FindByID(targetID)
			if err != nil {
				return fmt.Errorf("店铺不存在：%d: %w", targetID, err)
			}
			if in.NotifyEnabled != nil && target.NotifyEnabled != *in.NotifyEnabled {
				target.NotifyEnabled = *in.NotifyEnabled
				if err := targets.Update(target); err != nil {
					return err
				}
				result.UpdatedTargets++
			}
			if !in.UpsertRule {
				continue
			}
			rule, err := buildShopWatchRule(target.ID, in.Rule, nil)
			if err != nil {
				return err
			}
			current, err := rules.FindByTargetAndName(target.ID, rule.Name)
			if err != nil {
				return err
			}
			if current != nil && in.ReplaceSameName {
				rule.ID = current.ID
				rule.CreatedAt = current.CreatedAt
				if err := rules.Update(rule); err != nil {
					return err
				}
				result.UpdatedRules++
			} else if current == nil {
				if err := rules.Create(rule); err != nil {
					return err
				}
				result.CreatedRules++
			}
		}
		return nil
	}); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			fail(c, http.StatusNotFound, err)
			return
		}
		fail(c, http.StatusInternalServerError, err)
		return
	}
	list, err := d.ShopTargets.List()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if !attachShopWatchRuleCounts(c, d, list) {
		return
	}
	result.Targets = list
	c.JSON(http.StatusOK, gin.H{"data": result})
}

func attachShopWatchRuleCounts(c *gin.Context, d *Deps, list []storage.ShopTarget) bool {
	if d.ShopWatchRules == nil {
		return true
	}
	ids := make([]uint, 0, len(list))
	for _, target := range list {
		ids = append(ids, target.ID)
	}
	counts, err := d.ShopWatchRules.CountByTargets(ids)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return false
	}
	for i := range list {
		list[i].WatchRuleCount = counts[list[i].ID]
	}
	return true
}

func getShopTarget(c *gin.Context, d *Deps) {
	if !shopReposReady(c, d) {
		return
	}
	id, ok := parseUintParam(c, "id")
	if !ok {
		return
	}
	target, err := d.ShopTargets.FindByID(id)
	if err != nil {
		fail(c, http.StatusNotFound, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": target})
}

func updateShopTarget(c *gin.Context, d *Deps) {
	if !shopReposReady(c, d) {
		return
	}
	id, ok := parseUintParam(c, "id")
	if !ok {
		return
	}
	current, err := d.ShopTargets.FindByID(id)
	if err != nil {
		fail(c, http.StatusNotFound, err)
		return
	}
	var in shopTargetInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	next, err := buildShopTarget(in, current)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	next.ID = current.ID
	next.CreatedAt = current.CreatedAt
	next.LastSyncAt = current.LastSyncAt
	next.LastError = current.LastError
	next.LastShopName = current.LastShopName
	next.LastGoodsCount = current.LastGoodsCount
	next.LastLowStockGoods = current.LastLowStockGoods
	next.LastChangedCount = current.LastChangedCount
	if err := d.ShopTargets.Update(next); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": next})
}

func deleteShopTarget(c *gin.Context, d *Deps) {
	if !shopReposReady(c, d) {
		return
	}
	id, ok := parseUintParam(c, "id")
	if !ok {
		return
	}
	if d.ShopMonitor != nil {
		if err := d.ShopMonitor.DeleteTarget(id); err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
	} else if err := d.ShopTargets.Delete(id); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"ok": true}})
}

func parseShopURL(c *gin.Context, d *Deps) {
	var in parseShopURLInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	parsed, err := shopprovider.ParseShopURL(in.SiteURL)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if provider, err := shopprovider.For(parsed.Platform); err == nil {
		if setter, ok := provider.(shopprovider.HTTPConfigSetter); ok {
			setter.SetHTTPConfig(shopprovider.HTTPConfig{Timeout: 10 * time.Second})
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
		defer cancel()
		info, err := provider.Info(ctx, shopprovider.Target{
			Platform: parsed.Platform,
			SiteURL:  strings.TrimSpace(in.SiteURL),
			BaseURL:  parsed.BaseURL,
			Token:    parsed.Token,
		})
		if err != nil {
			parsed.NameError = err.Error()
		} else if info != nil {
			parsed.Name = strings.TrimSpace(info.Name)
			if parsed.Name == "" {
				parsed.NameError = "shop info response does not contain nickname"
			}
		}
	} else {
		parsed.NameError = err.Error()
	}
	c.JSON(http.StatusOK, gin.H{"data": parsed})
}

func testShopTarget(c *gin.Context, d *Deps) {
	if !shopMonitorReady(c, d) {
		return
	}
	id, ok := parseUintParam(c, "id")
	if !ok {
		return
	}
	target, err := d.ShopTargets.FindByID(id)
	if err != nil {
		fail(c, http.StatusNotFound, err)
		return
	}
	result, err := d.ShopMonitor.Test(c.Request.Context(), *target)
	if err != nil {
		failShopUpstream(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": result})
}

func syncShopTarget(c *gin.Context, d *Deps) {
	if !shopSyncRunnerReady(c, d) {
		return
	}
	id, ok := parseUintParam(c, "id")
	if !ok {
		return
	}
	job, reused, err := d.ShopSyncRunner.Start(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			fail(c, http.StatusNotFound, err)
			return
		}
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"data": gin.H{"job": job, "reused": reused}})
}

func getShopSyncJob(c *gin.Context, d *Deps) {
	if !shopSyncRunnerReady(c, d) {
		return
	}
	targetID, ok := parseUintParam(c, "id")
	if !ok {
		return
	}
	jobID, ok := parseUintParam(c, "job_id")
	if !ok {
		return
	}
	job, err := d.ShopSyncRunner.Get(targetID, jobID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			fail(c, http.StatusNotFound, err)
			return
		}
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": job})
}

func getLatestShopSyncJob(c *gin.Context, d *Deps) {
	if !shopSyncRunnerReady(c, d) {
		return
	}
	targetID, ok := parseUintParam(c, "id")
	if !ok {
		return
	}
	job, err := d.ShopSyncRunner.Latest(targetID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			fail(c, http.StatusNotFound, err)
			return
		}
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": job})
}

func getShopSyncJobsStatus(c *gin.Context, d *Deps) {
	if !shopSyncRunnerReady(c, d) {
		return
	}
	var in shopSyncJobsStatusInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	jobIDs := cleanUintIDs(in.JobIDs)
	if len(jobIDs) > 500 {
		fail(c, http.StatusBadRequest, fmt.Errorf("too many shop sync jobs"))
		return
	}
	jobs, err := d.ShopSyncRunner.GetMany(jobIDs)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": jobs})
}

func syncAllShopTargets(c *gin.Context, d *Deps) {
	if !shopSyncRunnerReady(c, d) {
		return
	}
	list, err := d.ShopTargets.ListMonitorEnabled()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	result := shopSyncAllJobsResult{Total: len(list), Targets: make([]shopSyncAllJobResult, 0, len(list))}
	for i := range list {
		target := list[i]
		job, reused, startErr := d.ShopSyncRunner.Start(target.ID)
		item := shopSyncAllJobResult{TargetID: target.ID, Name: target.Name, Job: job, Reused: reused}
		if startErr != nil {
			item.Error = startErr.Error()
			result.Failed++
		} else if reused {
			result.Reused++
		} else {
			result.Queued++
		}
		result.Targets = append(result.Targets, item)
	}
	c.JSON(http.StatusAccepted, gin.H{"data": result})
}

func shopTargetCategories(c *gin.Context, d *Deps) {
	if !shopMonitorReady(c, d) {
		return
	}
	id, ok := parseUintParam(c, "id")
	if !ok {
		return
	}
	target, err := d.ShopTargets.FindByID(id)
	if err != nil {
		fail(c, http.StatusNotFound, err)
		return
	}
	result, err := d.ShopMonitor.Test(c.Request.Context(), *target)
	if err != nil {
		failShopUpstream(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": result.Categories})
}

func shopTargetGoods(c *gin.Context, d *Deps) {
	if !shopReposReady(c, d) {
		return
	}
	id, ok := parseUintParam(c, "id")
	if !ok {
		return
	}
	target, err := d.ShopTargets.FindByID(id)
	if err != nil {
		fail(c, http.StatusNotFound, err)
		return
	}
	page, pageSize := parsePageDefaults(c)
	filter, ok := parseShopGoodsFilter(c, target.StockThreshold)
	if !ok {
		return
	}
	list, total, err := d.ShopGoods.ListPageFiltered(id, page, pageSize, filter)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": pageData(list, total, page, pageSize)})
}

func listAllShopGoods(c *gin.Context, d *Deps) {
	if !shopReposReady(c, d) {
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
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": pageData(list, total, page, pageSize)})
}

func shopTargetSnapshotCategories(c *gin.Context, d *Deps) {
	if !shopReposReady(c, d) {
		return
	}
	id, ok := parseUintParam(c, "id")
	if !ok {
		return
	}
	target, err := d.ShopTargets.FindByID(id)
	if err != nil {
		fail(c, http.StatusNotFound, err)
		return
	}
	list, err := d.ShopGoods.SnapshotCategories(id, target.StockThreshold)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": list})
}

func refreshShopTargetGoods(c *gin.Context, d *Deps) {
	if !shopMonitorReady(c, d) {
		return
	}
	id, ok := parseUintParam(c, "id")
	if !ok {
		return
	}
	goodsKey := strings.TrimSpace(c.Param("goods_key"))
	if goodsKey == "" {
		fail(c, http.StatusBadRequest, fmt.Errorf("goods_key is required"))
		return
	}
	result, err := d.ShopMonitor.RefreshGoodsByKey(c.Request.Context(), id, goodsKey)
	if err != nil {
		failShopUpstream(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": result})
}

func shopTargetChangeLogs(c *gin.Context, d *Deps) {
	if !shopReposReady(c, d) {
		return
	}
	id, ok := parseUintParam(c, "id")
	if !ok {
		return
	}
	page, pageSize := parsePageDefaults(c)
	list, total, err := d.ShopGoods.ListChangesPage(id, page, pageSize)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": pageData(list, total, page, pageSize)})
}

func shopTargetMonitorLogs(c *gin.Context, d *Deps) {
	if !shopReposReady(c, d) {
		return
	}
	id, ok := parseUintParam(c, "id")
	if !ok {
		return
	}
	page, pageSize := parsePageDefaults(c)
	list, total, err := d.ShopGoods.ListMonitorLogsPage(id, page, pageSize)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": pageData(list, total, page, pageSize)})
}

func buildShopTarget(in shopTargetInput, current *storage.ShopTarget) (*storage.ShopTarget, error) {
	target := &storage.ShopTarget{}
	if current != nil {
		*target = *current
	}
	target.Name = strings.TrimSpace(in.Name)
	target.Platform = in.Platform
	target.SiteURL = strings.TrimSpace(in.SiteURL)
	target.BaseURL = strings.TrimRight(strings.TrimSpace(in.BaseURL), "/")
	target.Token = strings.TrimSpace(in.Token)
	if target.SiteURL == "" {
		return nil, fmt.Errorf("site_url is required")
	}
	if parsed, err := shopprovider.ParseShopURL(target.SiteURL); err == nil {
		if target.Platform == "" {
			target.Platform = parsed.Platform
		}
		if target.BaseURL == "" {
			target.BaseURL = parsed.BaseURL
		}
		if target.Token == "" {
			target.Token = parsed.Token
		}
	}
	if target.Platform == "" {
		target.Platform = storage.ShopPlatformLDXP
	}
	if target.Name == "" {
		target.Name = target.Token
	}
	if target.BaseURL == "" || target.Token == "" {
		return nil, fmt.Errorf("base_url and token are required")
	}
	monitorEnabled := true
	notifyEnabled := false
	priceChangeEnabled := true
	stockChangeEnabled := true
	lowStockEnabled := true
	restockEnabled := true
	newGoodsEnabled := true
	removedGoodsEnabled := true
	proxyEnabled := false
	if current != nil {
		monitorEnabled = current.MonitorEnabled
		notifyEnabled = current.NotifyEnabled
		priceChangeEnabled = current.PriceChangeEnabled
		stockChangeEnabled = current.StockChangeEnabled
		lowStockEnabled = current.LowStockEnabled
		restockEnabled = current.RestockEnabled
		newGoodsEnabled = current.NewGoodsEnabled
		removedGoodsEnabled = current.RemovedGoodsEnabled
		proxyEnabled = current.ProxyEnabled
	}
	target.MonitorEnabled = boolDefault(in.MonitorEnabled, monitorEnabled)
	target.NotifyEnabled = boolDefault(in.NotifyEnabled, notifyEnabled)
	target.ScopeMode = in.ScopeMode
	if target.ScopeMode == "" {
		target.ScopeMode = storage.ShopScopeAll
	}
	if target.ScopeMode != storage.ShopScopeAll && target.ScopeMode != storage.ShopScopeFilters && target.ScopeMode != storage.ShopScopeGoodsKeys {
		return nil, fmt.Errorf("unsupported scope_mode: %s", target.ScopeMode)
	}
	target.GoodsTypesJSON = mustJSON(defaultStrings(in.GoodsTypes, []string{"card"}))
	target.CategoryIDsJSON = mustJSON(in.CategoryIDs)
	target.CategoryNamesJSON = mustJSON(cleanStrings(in.CategoryNames))
	target.KeywordsJSON = mustJSON(cleanStrings(in.Keywords))
	target.GoodsKeysJSON = mustJSON(cleanStrings(in.GoodsKeys))
	target.StockThreshold = in.StockThreshold
	target.PriceChangeEnabled = boolDefault(in.PriceChangeEnabled, priceChangeEnabled)
	target.StockChangeEnabled = boolDefault(in.StockChangeEnabled, stockChangeEnabled)
	target.LowStockEnabled = boolDefault(in.LowStockEnabled, lowStockEnabled)
	target.RestockEnabled = boolDefault(in.RestockEnabled, restockEnabled)
	target.NewGoodsEnabled = boolDefault(in.NewGoodsEnabled, newGoodsEnabled)
	target.RemovedGoodsEnabled = boolDefault(in.RemovedGoodsEnabled, removedGoodsEnabled)
	target.ProxyEnabled = boolDefault(in.ProxyEnabled, proxyEnabled)
	target.SortOrder = in.SortOrder
	target.GoodsSort = normalizeShopGoodsSort(in.GoodsSort)
	if current != nil && strings.TrimSpace(in.GoodsSort) == "" {
		target.GoodsSort = current.GoodsSort
	}
	if current != nil && target.SortOrder == 0 {
		target.SortOrder = current.SortOrder
	}
	if current != nil && target.SortOrder == 0 {
		target.SortOrder = 1
	}
	return target, nil
}

func normalizeShopGoodsSort(sort string) string {
	switch strings.TrimSpace(sort) {
	case "", "category":
		return "category"
	case "stock_asc", "stock_desc", "price_asc", "price_desc", "last_seen_desc":
		return strings.TrimSpace(sort)
	default:
		return "category"
	}
}

func cleanUintIDs(ids []uint) []uint {
	out := make([]uint, 0, len(ids))
	seen := map[uint]struct{}{}
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func parseUintParam(c *gin.Context, name string) (uint, bool) {
	raw := c.Param(name)
	id64, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || id64 == 0 {
		fail(c, http.StatusBadRequest, fmt.Errorf("invalid %s", name))
		return 0, false
	}
	return uint(id64), true
}

func parsePageDefaults(c *gin.Context) (int, int) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	return page, pageSize
}

func parseShopGoodsFilter(c *gin.Context, stockThreshold int) (storage.ShopGoodsFilter, bool) {
	filter := storage.ShopGoodsFilter{
		CategoryName:   strings.TrimSpace(c.Query("category_name")),
		Keyword:        strings.TrimSpace(c.Query("keyword")),
		StockThreshold: stockThreshold,
	}
	if raw, exists := c.GetQuery("category_id"); exists {
		categoryID, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			fail(c, http.StatusBadRequest, fmt.Errorf("invalid category_id"))
			return filter, false
		}
		filter.CategoryID = &categoryID
	}
	status := strings.TrimSpace(c.Query("status"))
	switch status {
	case "", "all":
	case "active", "in_stock", "removed", "low_stock", "out_of_stock":
		filter.Status = status
	default:
		fail(c, http.StatusBadRequest, fmt.Errorf("unsupported goods status: %s", status))
		return filter, false
	}
	sort := strings.TrimSpace(c.DefaultQuery("sort", "category"))
	switch sort {
	case "", "category":
	case "stock_asc", "stock_desc", "price_asc", "price_desc", "last_seen_desc":
		filter.Sort = sort
	default:
		fail(c, http.StatusBadRequest, fmt.Errorf("unsupported goods sort: %s", sort))
		return filter, false
	}
	return filter, true
}

func pageData[T any](items []T, total int64, page, pageSize int) gin.H {
	pages := 1
	if total > 0 {
		pages = int((total + int64(pageSize) - 1) / int64(pageSize))
	}
	return gin.H{
		"items":     items,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
		"pages":     pages,
	}
}

func boolDefault(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func cleanStrings(list []string) []string {
	out := make([]string, 0, len(list))
	for _, item := range list {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

func defaultStrings(list, fallback []string) []string {
	list = cleanStrings(list)
	if len(list) == 0 {
		return fallback
	}
	return list
}

func mustJSON(value any) string {
	body, err := json.Marshal(value)
	if err != nil {
		return "[]"
	}
	return string(body)
}

func shopReposReady(c *gin.Context, d *Deps) bool {
	if d.ShopTargets == nil || d.ShopGoods == nil {
		fail(c, http.StatusInternalServerError, fmt.Errorf("shop repositories are not configured"))
		return false
	}
	return true
}

func shopMonitorReady(c *gin.Context, d *Deps) bool {
	if !shopReposReady(c, d) {
		return false
	}
	if d.ShopMonitor == nil {
		fail(c, http.StatusInternalServerError, fmt.Errorf("shop monitor service is not configured"))
		return false
	}
	return true
}

func shopSyncRunnerReady(c *gin.Context, d *Deps) bool {
	if d.ShopTargets == nil || d.ShopSyncRunner == nil {
		fail(c, http.StatusInternalServerError, fmt.Errorf("shop sync job runner is not configured"))
		return false
	}
	return true
}
