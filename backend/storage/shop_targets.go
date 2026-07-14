package storage

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
)

type ShopTargets struct{ db *gorm.DB }

func NewShopTargets(db *gorm.DB) *ShopTargets { return &ShopTargets{db: db} }

func (r *ShopTargets) Create(target *ShopTarget) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if target.SortOrder <= 0 {
			var maxSortOrder int
			if err := tx.Model(&ShopTarget{}).Select("COALESCE(MAX(sort_order), 0)").Scan(&maxSortOrder).Error; err != nil {
				return err
			}
			target.SortOrder = maxSortOrder + 1
		}
		return tx.Create(target).Error
	})
}

func (r *ShopTargets) Update(target *ShopTarget) error {
	return r.db.Save(target).Error
}

// Transaction runs a multi-repository shop operation atomically.
// Both repositories share the same transaction so target and rule updates cannot partially commit.
func (r *ShopTargets) Transaction(fn func(targets *ShopTargets, rules *ShopWatchRules) error) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		return fn(NewShopTargets(tx), NewShopWatchRules(tx))
	})
}

func (r *ShopTargets) UpdateSortOrders(orders map[uint]int) error {
	if len(orders) == 0 {
		return nil
	}
	return r.db.Transaction(func(tx *gorm.DB) error {
		for id, sortOrder := range orders {
			if id == 0 {
				continue
			}
			if err := tx.Model(&ShopTarget{}).Where("id = ?", id).Update("sort_order", sortOrder).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *ShopTargets) Delete(id uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		for _, model := range []any{
			&ShopWatchRule{},
			&ShopGoodsSnapshot{},
			&ShopGoodsChangeLog{},
			&ShopMonitorLog{},
			&ShopSyncJob{},
		} {
			if err := tx.Where("target_id = ?", id).Delete(model).Error; err != nil {
				return err
			}
		}
		return tx.Delete(&ShopTarget{}, id).Error
	})
}

func (r *ShopTargets) FindByID(id uint) (*ShopTarget, error) {
	var target ShopTarget
	if err := r.db.First(&target, id).Error; err != nil {
		return nil, err
	}
	return &target, nil
}

func (r *ShopTargets) List() ([]ShopTarget, error) {
	var list []ShopTarget
	if err := r.db.Order("sort_order ASC").Order("id ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func (r *ShopTargets) ListMonitorEnabled() ([]ShopTarget, error) {
	var list []ShopTarget
	if err := r.db.Where("monitor_enabled = ?", true).Order("sort_order ASC").Order("id ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func (r *ShopTargets) SetSyncResult(id uint, at *time.Time, lastErr string, shopName string, goodsCount, lowStockGoods, changedCount int) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		updates := map[string]any{
			"last_sync_at":         at,
			"last_error":           lastErr,
			"last_shop_name":       shopName,
			"last_goods_count":     goodsCount,
			"last_low_stock_goods": lowStockGoods,
			"last_changed_count":   changedCount,
		}
		if err := tx.Model(&ShopTarget{}).Where("id = ?", id).Updates(updates).Error; err != nil {
			return err
		}

		normalizedName := normalizeShopTargetName(shopName)
		if normalizedName == "" {
			return nil
		}
		var conflict int64
		if err := tx.Model(&ShopTarget{}).Where("id <> ? AND name = ?", id, normalizedName).Count(&conflict).Error; err != nil {
			return err
		}
		if conflict > 0 {
			return nil
		}
		return tx.Model(&ShopTarget{}).Where("id = ?", id).Update("name", normalizedName).Error
	})
}

func normalizeShopTargetName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	runes := []rune(name)
	if len(runes) > 128 {
		return string(runes[:128])
	}
	return name
}

type ShopWatchRules struct{ db *gorm.DB }

func NewShopWatchRules(db *gorm.DB) *ShopWatchRules { return &ShopWatchRules{db: db} }

func (r *ShopWatchRules) ListByTarget(targetID uint) ([]ShopWatchRule, error) {
	var list []ShopWatchRule
	if err := r.db.Where("target_id = ?", targetID).Order("enabled DESC").Order("id ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func (r *ShopWatchRules) ListEnabledByTarget(targetID uint) ([]ShopWatchRule, error) {
	var list []ShopWatchRule
	if err := r.db.Where("target_id = ? AND enabled = ?", targetID, true).Order("id ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func (r *ShopWatchRules) CountByTargets(targetIDs []uint) (map[uint]int, error) {
	out := make(map[uint]int, len(targetIDs))
	if len(targetIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		TargetID uint `gorm:"column:target_id"`
		Count    int  `gorm:"column:count"`
	}
	if err := r.db.Model(&ShopWatchRule{}).
		Select("target_id, COUNT(*) AS count").
		Where("target_id IN ?", targetIDs).
		Group("target_id").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.TargetID] = row.Count
	}
	return out, nil
}

func (r *ShopWatchRules) FindByID(targetID, ruleID uint) (*ShopWatchRule, error) {
	var rule ShopWatchRule
	if err := r.db.Where("target_id = ? AND id = ?", targetID, ruleID).First(&rule).Error; err != nil {
		return nil, err
	}
	return &rule, nil
}

func (r *ShopWatchRules) FindByTargetAndName(targetID uint, name string) (*ShopWatchRule, error) {
	var rule ShopWatchRule
	err := r.db.Where("target_id = ? AND name = ?", targetID, strings.TrimSpace(name)).First(&rule).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &rule, nil
}

func (r *ShopWatchRules) Create(rule *ShopWatchRule) error {
	return r.db.Create(rule).Error
}

func (r *ShopWatchRules) Update(rule *ShopWatchRule) error {
	return r.db.Save(rule).Error
}

func (r *ShopWatchRules) Delete(targetID, ruleID uint) error {
	return r.db.Where("target_id = ? AND id = ?", targetID, ruleID).Delete(&ShopWatchRule{}).Error
}

func ShopWatchRuleMatchesChange(rule ShopWatchRule, event ShopGoodsChangeEvent, snapshot *ShopGoodsSnapshot, goodsKey, goodsName string) bool {
	if !rule.Enabled {
		return false
	}
	if !shopWatchRuleMatchesEvent(rule, event) {
		return false
	}
	if event == ShopChangeMonitorFailed {
		return true
	}
	if snapshot == nil {
		if strings.TrimSpace(goodsKey) == "" && strings.TrimSpace(goodsName) == "" {
			return false
		}
		snapshot = &ShopGoodsSnapshot{GoodsKey: goodsKey, Name: goodsName}
	}
	if event == ShopChangeStockLow && rule.StockThreshold > 0 && snapshot.StockCount > rule.StockThreshold {
		return false
	}
	return shopWatchRuleMatchesSnapshot(rule, *snapshot)
}

func ShopWatchRuleMatchesSnapshot(rule ShopWatchRule, snapshot ShopGoodsSnapshot) bool {
	return shopWatchRuleMatchesSnapshot(rule, snapshot)
}

func shopWatchRuleMatchesEvent(rule ShopWatchRule, event ShopGoodsChangeEvent) bool {
	events := parseJSONStrings(rule.EventsJSON)
	if len(events) == 0 {
		return true
	}
	wanted := string(event)
	for _, item := range events {
		if item == wanted {
			return true
		}
	}
	return false
}

func shopWatchRuleMatchesSnapshot(rule ShopWatchRule, snapshot ShopGoodsSnapshot) bool {
	hasCriteria := false
	goodsKey := strings.TrimSpace(snapshot.GoodsKey)
	goodsKeys := parseJSONStrings(rule.GoodsKeysJSON)
	if len(goodsKeys) > 0 {
		hasCriteria = true
		for _, key := range goodsKeys {
			if strings.EqualFold(key, goodsKey) {
				return true
			}
		}
	}

	categoryIDs := parseJSONInt64s(rule.CategoryIDsJSON)
	if len(categoryIDs) > 0 {
		hasCriteria = true
		for _, id := range categoryIDs {
			if id == snapshot.CategoryID {
				return true
			}
		}
	}

	categoryName := strings.ToLower(strings.TrimSpace(snapshot.CategoryName))
	categoryNames := parseJSONStrings(rule.CategoryNamesJSON)
	if len(categoryNames) > 0 {
		hasCriteria = true
		for _, name := range categoryNames {
			if strings.EqualFold(name, categoryName) || strings.EqualFold(name, snapshot.CategoryName) {
				return true
			}
		}
	}

	haystack := strings.ToLower(strings.Join([]string{
		snapshot.Name,
		snapshot.GoodsKey,
		snapshot.CategoryName,
	}, " "))
	keywords := parseJSONStrings(rule.KeywordsJSON)
	if len(keywords) > 0 {
		hasCriteria = true
		for _, keyword := range keywords {
			if strings.Contains(haystack, strings.ToLower(keyword)) {
				return true
			}
		}
	}

	return !hasCriteria
}

func applyShopWatchRuleFilter(q *gorm.DB, rule ShopWatchRule) *gorm.DB {
	clauses := make([]string, 0, 4)
	args := make([]any, 0)
	if keys := parseJSONStrings(rule.GoodsKeysJSON); len(keys) > 0 {
		clauses = append(clauses, "goods_key IN ?")
		args = append(args, keys)
	}
	if ids := parseJSONInt64s(rule.CategoryIDsJSON); len(ids) > 0 {
		clauses = append(clauses, "category_id IN ?")
		args = append(args, ids)
	}
	if names := parseJSONStrings(rule.CategoryNamesJSON); len(names) > 0 {
		clauses = append(clauses, "category_name IN ?")
		args = append(args, names)
	}
	for _, keyword := range parseJSONStrings(rule.KeywordsJSON) {
		like := "%" + keyword + "%"
		clauses = append(clauses, "(name LIKE ? OR goods_key LIKE ? OR category_name LIKE ?)")
		args = append(args, like, like, like)
	}
	if len(clauses) == 0 {
		return q
	}
	return q.Where(strings.Join(clauses, " OR "), args...)
}

func parseJSONStrings(raw string) []string {
	var list []string
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return nil
	}
	out := make([]string, 0, len(list))
	seen := map[string]struct{}{}
	for _, item := range list {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		key := strings.ToLower(item)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func parseJSONInt64s(raw string) []int64 {
	var list []int64
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return nil
	}
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

type ShopGoods struct{ db *gorm.DB }

func NewShopGoods(db *gorm.DB) *ShopGoods { return &ShopGoods{db: db} }

type ShopGoodsFilter struct {
	CategoryID     *int64
	CategoryName   string
	Status         string
	Keyword        string
	Sort           string
	StockThreshold int
	TargetID       uint
}

type ShopGoodsWithTarget struct {
	ShopGoodsSnapshot
	TargetName           string `gorm:"column:target_name" json:"target_name"`
	TargetLastShopName   string `gorm:"column:target_last_shop_name" json:"target_last_shop_name"`
	TargetSiteURL        string `gorm:"column:target_site_url" json:"target_site_url"`
	TargetMonitorEnabled bool   `gorm:"column:target_monitor_enabled" json:"target_monitor_enabled"`
	TargetNotifyEnabled  bool   `gorm:"column:target_notify_enabled" json:"target_notify_enabled"`
	TargetStockThreshold int    `gorm:"column:target_stock_threshold" json:"target_stock_threshold"`
}

type ShopSnapshotCategory struct {
	CategoryID      int64  `json:"category_id"`
	CategoryName    string `json:"category_name"`
	GoodsCount      int64  `json:"goods_count"`
	ActiveCount     int64  `json:"active_count"`
	RemovedCount    int64  `json:"removed_count"`
	LowStockCount   int64  `json:"low_stock_count"`
	OutOfStockCount int64  `json:"out_of_stock_count"`
}

func (r *ShopGoods) ListByTarget(targetID uint) ([]ShopGoodsSnapshot, error) {
	var list []ShopGoodsSnapshot
	if err := r.db.Where("target_id = ?", targetID).Order("removed_at ASC").Order("category_name ASC").Order("name ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func (r *ShopGoods) ListActiveByTarget(targetID uint) ([]ShopGoodsSnapshot, error) {
	var list []ShopGoodsSnapshot
	if err := r.db.Where("target_id = ? AND removed_at IS NULL", targetID).Order("category_name ASC").Order("name ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func (r *ShopGoods) ListFirstMatchingWatchRule(rule ShopWatchRule, limit int) ([]ShopGoodsSnapshot, int64, error) {
	if limit <= 0 {
		limit = 10
	}
	q := r.db.Model(&ShopGoodsSnapshot{}).Where("target_id = ?", rule.TargetID)
	q = applyShopWatchRuleFilter(q, rule)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []ShopGoodsSnapshot
	if err := q.Order("removed_at ASC").Order("category_name ASC").Order("name ASC").Order("goods_key ASC").
		Limit(limit).
		Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

func (r *ShopGoods) ListPage(targetID uint, page, pageSize int) ([]ShopGoodsSnapshot, int64, error) {
	return r.ListPageFiltered(targetID, page, pageSize, ShopGoodsFilter{})
}

func (r *ShopGoods) ListPageFiltered(targetID uint, page, pageSize int, filter ShopGoodsFilter) ([]ShopGoodsSnapshot, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	q := r.db.Model(&ShopGoodsSnapshot{}).Where("target_id = ?", targetID)
	q = applyShopGoodsFilter(q, filter)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []ShopGoodsSnapshot
	if err := applyShopGoodsSort(q, filter.Sort).
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

func (r *ShopGoods) ListAllPageFiltered(page, pageSize int, filter ShopGoodsFilter) ([]ShopGoodsWithTarget, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	q := r.db.Table("shop_goods_snapshots AS s").
		Select(`s.*,
			t.name AS target_name,
			t.last_shop_name AS target_last_shop_name,
			t.site_url AS target_site_url,
			t.monitor_enabled AS target_monitor_enabled,
			t.notify_enabled AS target_notify_enabled,
			t.stock_threshold AS target_stock_threshold`).
		Joins("JOIN shop_targets AS t ON t.id = s.target_id")
	q = applyShopGoodsFilterQualified(q, filter, "s", true)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []ShopGoodsWithTarget
	if err := applyShopGoodsSortQualified(q, filter.Sort, "s", true).
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

func (r *ShopGoods) SnapshotCategories(targetID uint, stockThreshold int) ([]ShopSnapshotCategory, error) {
	threshold := stockThreshold
	if threshold <= 0 {
		threshold = 0
	}
	var list []ShopSnapshotCategory
	if err := r.db.Model(&ShopGoodsSnapshot{}).
		Select(`category_id,
			category_name,
			COUNT(*) AS goods_count,
			SUM(CASE WHEN removed_at IS NULL THEN 1 ELSE 0 END) AS active_count,
			SUM(CASE WHEN removed_at IS NOT NULL THEN 1 ELSE 0 END) AS removed_count,
			SUM(CASE WHEN ? > 0 AND removed_at IS NULL AND stock_count <= ? THEN 1 ELSE 0 END) AS low_stock_count,
			SUM(CASE WHEN removed_at IS NULL AND stock_count <= 0 THEN 1 ELSE 0 END) AS out_of_stock_count`, threshold, threshold).
		Where("target_id = ?", targetID).
		Group("category_id, category_name").
		Order("removed_count ASC").
		Order("category_name ASC").
		Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func applyShopGoodsFilter(q *gorm.DB, filter ShopGoodsFilter) *gorm.DB {
	return applyShopGoodsFilterQualified(q, filter, "", false)
}

func applyShopGoodsFilterQualified(q *gorm.DB, filter ShopGoodsFilter, alias string, useTargetThreshold bool) *gorm.DB {
	col := func(name string) string {
		if alias == "" {
			return name
		}
		return alias + "." + name
	}
	if filter.TargetID != 0 {
		q = q.Where(col("target_id")+" = ?", filter.TargetID)
	}
	if filter.CategoryID != nil {
		q = q.Where(col("category_id")+" = ?", *filter.CategoryID)
	}
	if filter.CategoryName != "" {
		q = q.Where(col("category_name")+" = ?", filter.CategoryName)
	}
	if filter.Keyword != "" {
		like := "%" + filter.Keyword + "%"
		q = q.Where("("+col("name")+" LIKE ? OR "+col("goods_key")+" LIKE ? OR "+col("category_name")+" LIKE ?)", like, like, like)
	}
	switch filter.Status {
	case "active":
		q = q.Where(col("removed_at") + " IS NULL")
	case "in_stock":
		q = q.Where(col("removed_at") + " IS NULL AND " + col("stock_count") + " > 0")
	case "removed":
		q = q.Where(col("removed_at") + " IS NOT NULL")
	case "low_stock":
		if useTargetThreshold {
			q = q.Where("t.stock_threshold > 0 AND " + col("removed_at") + " IS NULL AND " + col("stock_count") + " <= t.stock_threshold")
			break
		}
		threshold := filter.StockThreshold
		if threshold <= 0 {
			q = q.Where("1 = 0")
			break
		}
		q = q.Where(col("removed_at")+" IS NULL AND "+col("stock_count")+" <= ?", threshold)
	case "out_of_stock":
		q = q.Where(col("removed_at") + " IS NULL AND " + col("stock_count") + " <= 0")
	}
	return q
}

func applyShopGoodsSort(q *gorm.DB, sort string) *gorm.DB {
	return applyShopGoodsSortQualified(q, sort, "", false)
}

func applyShopGoodsSortQualified(q *gorm.DB, sort string, alias string, includeTarget bool) *gorm.DB {
	col := func(name string) string {
		if alias == "" {
			return name
		}
		return alias + "." + name
	}
	q = q.Order(col("removed_at") + " ASC")
	switch sort {
	case "stock_asc":
		return q.Order(col("stock_count") + " ASC").Order(col("category_name") + " ASC").Order(col("name") + " ASC").Order(col("goods_key") + " ASC")
	case "stock_desc":
		return q.Order(col("stock_count") + " DESC").Order(col("category_name") + " ASC").Order(col("name") + " ASC").Order(col("goods_key") + " ASC")
	case "price_asc":
		return q.Order(col("price") + " ASC").Order(col("category_name") + " ASC").Order(col("name") + " ASC").Order(col("goods_key") + " ASC")
	case "price_desc":
		return q.Order(col("price") + " DESC").Order(col("category_name") + " ASC").Order(col("name") + " ASC").Order(col("goods_key") + " ASC")
	case "last_seen_desc":
		return q.Order(col("last_seen_at") + " DESC").Order(col("category_name") + " ASC").Order(col("name") + " ASC").Order(col("goods_key") + " ASC")
	default:
		if includeTarget {
			q = q.Order("t.sort_order ASC").Order("t.id ASC")
		}
		return q.Order(col("category_name") + " ASC").Order(col("name") + " ASC").Order(col("goods_key") + " ASC")
	}
}

func (r *ShopGoods) FindSnapshot(targetID uint, goodsKey string) (*ShopGoodsSnapshot, error) {
	var snapshot ShopGoodsSnapshot
	err := r.db.Where("target_id = ? AND goods_key = ?", targetID, goodsKey).First(&snapshot).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &snapshot, nil
}

func (r *ShopGoods) SaveSnapshot(snapshot *ShopGoodsSnapshot) error {
	return r.db.Save(snapshot).Error
}

func (r *ShopGoods) CreateSnapshot(snapshot *ShopGoodsSnapshot) error {
	return r.db.Create(snapshot).Error
}

func (r *ShopGoods) AppendChange(log *ShopGoodsChangeLog) error {
	if log.ChangedAt.IsZero() {
		log.ChangedAt = time.Now()
	}
	return r.db.Create(log).Error
}

func (r *ShopGoods) ListChangesPage(targetID uint, page, pageSize int) ([]ShopGoodsChangeLog, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	q := r.db.Model(&ShopGoodsChangeLog{})
	if targetID != 0 {
		q = q.Where("target_id = ?", targetID)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []ShopGoodsChangeLog
	if err := q.Order("changed_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

func (r *ShopGoods) AppendMonitorLog(log *ShopMonitorLog) error {
	if log.StartedAt.IsZero() {
		log.StartedAt = time.Now()
	}
	if log.FinishedAt.IsZero() {
		log.FinishedAt = time.Now()
	}
	if log.DurationMS == 0 {
		log.DurationMS = log.FinishedAt.Sub(log.StartedAt).Milliseconds()
	}
	return r.db.Create(log).Error
}

func (r *ShopGoods) ListMonitorLogsPage(targetID uint, page, pageSize int) ([]ShopMonitorLog, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	q := r.db.Model(&ShopMonitorLog{})
	if targetID != 0 {
		q = q.Where("target_id = ?", targetID)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []ShopMonitorLog
	if err := q.Order("started_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

type ShopSyncJobs struct{ db *gorm.DB }

func NewShopSyncJobs(db *gorm.DB) *ShopSyncJobs { return &ShopSyncJobs{db: db} }

func (r *ShopSyncJobs) Create(job *ShopSyncJob) error {
	return r.db.Create(job).Error
}

func (r *ShopSyncJobs) FindByTargetAndID(targetID, id uint) (*ShopSyncJob, error) {
	var job ShopSyncJob
	if err := r.db.Where("target_id = ? AND id = ?", targetID, id).First(&job).Error; err != nil {
		return nil, err
	}
	return &job, nil
}

func (r *ShopSyncJobs) FindLatestByTarget(targetID uint) (*ShopSyncJob, error) {
	var job ShopSyncJob
	if err := r.db.Where("target_id = ?", targetID).Order("id DESC").First(&job).Error; err != nil {
		return nil, err
	}
	return &job, nil
}

func (r *ShopSyncJobs) FindActiveByTarget(targetID uint) (*ShopSyncJob, error) {
	var job ShopSyncJob
	err := r.db.Where("target_id = ? AND status IN ?", targetID, []ShopSyncJobStatus{ShopSyncJobQueued, ShopSyncJobRunning}).
		Order("id DESC").
		First(&job).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &job, nil
}

func (r *ShopSyncJobs) MarkRunning(id uint, startedAt time.Time) error {
	return r.db.Model(&ShopSyncJob{}).Where("id = ?", id).Updates(map[string]any{
		"status":     ShopSyncJobRunning,
		"started_at": startedAt,
	}).Error
}

func (r *ShopSyncJobs) Complete(id uint, status ShopSyncJobStatus, goodsCount, changedCount int, events map[string]int, errorMessage string, startedAt, finishedAt time.Time) error {
	updates := map[string]any{
		"status":        status,
		"error_message": errorMessage,
		"finished_at":   finishedAt,
		"duration_ms":   finishedAt.Sub(startedAt).Milliseconds(),
	}
	updates["goods_count"] = goodsCount
	updates["changed_count"] = changedCount
	if encoded, err := json.Marshal(events); err == nil {
		updates["events_json"] = string(encoded)
	}
	return r.db.Model(&ShopSyncJob{}).Where("id = ?", id).Updates(updates).Error
}

func (r *ShopSyncJobs) MarkInterrupted() error {
	now := time.Now()
	return r.db.Model(&ShopSyncJob{}).
		Where("status IN ?", []ShopSyncJobStatus{ShopSyncJobQueued, ShopSyncJobRunning}).
		Updates(map[string]any{
			"status":        ShopSyncJobFailed,
			"error_message": "服务重启前同步未完成",
			"finished_at":   now,
		}).Error
}
