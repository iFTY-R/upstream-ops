package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/ifty-r/upstream-ops/backend/storage"
)

type shopWatchRuleInput struct {
	Name           string                         `json:"name"`
	Enabled        *bool                          `json:"enabled"`
	GoodsKeys      []string                       `json:"goods_keys"`
	CategoryIDs    []int64                        `json:"category_ids"`
	CategoryNames  []string                       `json:"category_names"`
	Keywords       []string                       `json:"keywords"`
	Events         []storage.ShopGoodsChangeEvent `json:"events"`
	StockThreshold int                            `json:"stock_threshold"`
}

type shopWatchRulePreview struct {
	Total int64                       `json:"total"`
	Items []storage.ShopGoodsSnapshot `json:"items"`
}

func listShopWatchRules(c *gin.Context, d *Deps) {
	if !shopWatchReposReady(c, d) {
		return
	}
	targetID, ok := parseUintParam(c, "id")
	if !ok {
		return
	}
	if _, err := d.ShopTargets.FindByID(targetID); err != nil {
		fail(c, http.StatusNotFound, err)
		return
	}
	list, err := d.ShopWatchRules.ListByTarget(targetID)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": list})
}

func createShopWatchRule(c *gin.Context, d *Deps) {
	if !shopWatchReposReady(c, d) {
		return
	}
	targetID, ok := parseUintParam(c, "id")
	if !ok {
		return
	}
	if _, err := d.ShopTargets.FindByID(targetID); err != nil {
		fail(c, http.StatusNotFound, err)
		return
	}
	var in shopWatchRuleInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	rule, err := buildShopWatchRule(targetID, in, nil)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if err := d.ShopWatchRules.Create(rule); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": rule})
}

func updateShopWatchRule(c *gin.Context, d *Deps) {
	if !shopWatchReposReady(c, d) {
		return
	}
	targetID, ok := parseUintParam(c, "id")
	if !ok {
		return
	}
	ruleID, ok := parseUintParam(c, "rule_id")
	if !ok {
		return
	}
	current, err := d.ShopWatchRules.FindByID(targetID, ruleID)
	if err != nil {
		fail(c, http.StatusNotFound, err)
		return
	}
	var in shopWatchRuleInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	next, err := buildShopWatchRule(targetID, in, current)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	next.ID = current.ID
	next.CreatedAt = current.CreatedAt
	if err := d.ShopWatchRules.Update(next); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": next})
}

func deleteShopWatchRule(c *gin.Context, d *Deps) {
	if !shopWatchReposReady(c, d) {
		return
	}
	targetID, ok := parseUintParam(c, "id")
	if !ok {
		return
	}
	ruleID, ok := parseUintParam(c, "rule_id")
	if !ok {
		return
	}
	if err := d.ShopWatchRules.Delete(targetID, ruleID); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"ok": true}})
}

func previewShopWatchRule(c *gin.Context, d *Deps) {
	if !shopWatchReposReady(c, d) {
		return
	}
	targetID, ok := parseUintParam(c, "id")
	if !ok {
		return
	}
	if _, err := d.ShopTargets.FindByID(targetID); err != nil {
		fail(c, http.StatusNotFound, err)
		return
	}
	var in shopWatchRuleInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	rule, err := buildShopWatchRule(targetID, in, nil)
	if err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	items, total, err := d.ShopGoods.ListFirstMatchingWatchRule(*rule, 10)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": shopWatchRulePreview{Total: total, Items: items}})
}

func buildShopWatchRule(targetID uint, in shopWatchRuleInput, current *storage.ShopWatchRule) (*storage.ShopWatchRule, error) {
	rule := &storage.ShopWatchRule{}
	if current != nil {
		*rule = *current
	}
	rule.TargetID = targetID
	rule.Name = strings.TrimSpace(in.Name)
	if rule.Name == "" {
		return nil, fmt.Errorf("name is required")
	}
	rule.Enabled = boolDefault(in.Enabled, true)
	rule.GoodsKeysJSON = mustJSON(cleanStrings(in.GoodsKeys))
	rule.CategoryIDsJSON = mustJSON(cleanInt64s(in.CategoryIDs))
	rule.CategoryNamesJSON = mustJSON(cleanStrings(in.CategoryNames))
	rule.KeywordsJSON = mustJSON(cleanStrings(in.Keywords))
	events, err := cleanShopWatchEvents(in.Events)
	if err != nil {
		return nil, err
	}
	rule.EventsJSON = mustJSON(events)
	rule.StockThreshold = in.StockThreshold
	if rule.StockThreshold < 0 {
		rule.StockThreshold = 0
	}
	return rule, nil
}

func cleanShopWatchEvents(events []storage.ShopGoodsChangeEvent) ([]storage.ShopGoodsChangeEvent, error) {
	if len(events) == 0 {
		return nil, nil
	}
	allowed := map[storage.ShopGoodsChangeEvent]struct{}{
		storage.ShopChangeGoodsAdded:     {},
		storage.ShopChangeGoodsRemoved:   {},
		storage.ShopChangePriceChanged:   {},
		storage.ShopChangeStockChanged:   {},
		storage.ShopChangeStockLow:       {},
		storage.ShopChangeGoodsRestocked: {},
		storage.ShopChangeMonitorFailed:  {},
	}
	out := make([]storage.ShopGoodsChangeEvent, 0, len(events))
	seen := map[storage.ShopGoodsChangeEvent]struct{}{}
	for _, event := range events {
		if _, ok := allowed[event]; !ok {
			return nil, fmt.Errorf("unsupported watch event: %s", event)
		}
		if _, ok := seen[event]; ok {
			continue
		}
		seen[event] = struct{}{}
		out = append(out, event)
	}
	return out, nil
}

func cleanInt64s(list []int64) []int64 {
	out := make([]int64, 0, len(list))
	seen := map[int64]struct{}{}
	for _, item := range list {
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

func shopWatchReposReady(c *gin.Context, d *Deps) bool {
	if !shopReposReady(c, d) {
		return false
	}
	if d.ShopWatchRules == nil {
		fail(c, http.StatusInternalServerError, fmt.Errorf("shop watch rules repository is not configured"))
		return false
	}
	return true
}
