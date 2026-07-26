package storage

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// PriceAI owns persistence for the documented public Feed, local catalog
// projections, risk cache, and PriceAI-managed LDXP target bindings.
type PriceAI struct{ db *gorm.DB }

func NewPriceAI(db *gorm.DB) *PriceAI { return &PriceAI{db: db} }

// Transaction keeps a Feed import's current-state updates, history, and audit
// records on the same database transaction.
func (r *PriceAI) Transaction(fn func(tx *PriceAI) error) error {
	if fn == nil {
		return fmt.Errorf("priceai transaction callback is nil")
	}
	return r.db.Transaction(func(tx *gorm.DB) error { return fn(NewPriceAI(tx)) })
}

func (r *PriceAI) FindFeedState(sourceKey string) (*PriceAIFeedState, error) {
	var state PriceAIFeedState
	err := r.db.Where("source_key = ?", sourceKey).First(&state).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &state, nil
}

func (r *PriceAI) UpsertFeedState(state *PriceAIFeedState) error {
	if state == nil || strings.TrimSpace(state.SourceKey) == "" {
		return fmt.Errorf("priceai feed state source key is required")
	}
	return r.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "source_key"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"latest_url", "schema_url", "etag", "last_modified", "snapshot_id", "snapshot_url",
			"schema_version", "generated_at", "published_at", "feed_stale", "last_attempt_at",
			"last_success_at", "consecutive_failures", "last_error", "default_watch_seeded_slugs_json", "updated_at",
		}),
	}).Create(state).Error
}

func (r *PriceAI) FindProductByID(id uint) (*PriceAIProduct, error) {
	var product PriceAIProduct
	err := r.db.First(&product, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &product, nil
}

func (r *PriceAI) FindProductBySlug(slug string) (*PriceAIProduct, error) {
	var product PriceAIProduct
	err := r.db.Where("slug = ?", slug).First(&product).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &product, nil
}

func (r *PriceAI) FindProductByRemoteID(remoteID string) (*PriceAIProduct, error) {
	var product PriceAIProduct
	err := r.db.Where("remote_id = ?", remoteID).First(&product).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &product, nil
}

func (r *PriceAI) UpsertProduct(product *PriceAIProduct) (*PriceAIProduct, error) {
	if product == nil || strings.TrimSpace(product.RemoteID) == "" || strings.TrimSpace(product.Slug) == "" {
		return nil, fmt.Errorf("priceai product remote id and slug are required")
	}
	if err := r.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "remote_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"slug", "name", "platform", "product_type", "spec", "summary", "offer_count", "in_stock_count",
			"lowest_price", "lowest_price_currency", "latest_seen_at", "product_snapshot_generated_at",
			"last_snapshot_id", "last_seen_at", "missing_from_latest_at", "raw_json", "updated_at",
		}),
	}).Create(product).Error; err != nil {
		return nil, err
	}
	stored, err := r.FindProductByRemoteID(product.RemoteID)
	if err != nil {
		return nil, err
	}
	if stored == nil {
		return nil, fmt.Errorf("priceai product %q was not persisted", product.RemoteID)
	}
	*product = *stored
	return product, nil
}

type PriceAIProductPageOptions struct {
	Page           int
	PageSize       int
	Query          string
	Platform       string
	ProductType    string
	WatchState     string
	Availability   string
	IncludeMissing bool
	Sort           string
}

func (r *PriceAI) ListProductsPage(options PriceAIProductPageOptions) ([]PriceAIProduct, int64, error) {
	page, pageSize := normalizePriceAIPage(options.Page, options.PageSize)
	q := r.db.Model(&PriceAIProduct{}).
		Joins("LEFT JOIN priceai_watch_targets AS priceai_watch_targets ON priceai_watch_targets.product_id = priceai_products.id")
	if !options.IncludeMissing {
		q = q.Where("missing_from_latest_at IS NULL")
	}
	if value := strings.TrimSpace(options.Platform); value != "" {
		q = q.Where("platform = ?", value)
	}
	if value := strings.TrimSpace(options.ProductType); value != "" {
		q = q.Where("product_type = ?", value)
	}
	if value := strings.ToLower(strings.TrimSpace(options.Query)); value != "" {
		like := "%" + value + "%"
		q = q.Where("LOWER(priceai_products.name) LIKE ? OR LOWER(priceai_products.slug) LIKE ? OR LOWER(priceai_products.summary) LIKE ? OR LOWER(priceai_products.platform) LIKE ?", like, like, like, like)
	}
	switch strings.TrimSpace(options.WatchState) {
	case "watched":
		q = q.Where("priceai_watch_targets.id IS NOT NULL")
	case "unwatched":
		q = q.Where("priceai_watch_targets.id IS NULL")
	}
	switch strings.TrimSpace(options.Availability) {
	case "in_stock":
		q = q.Where("priceai_products.in_stock_count > 0")
	case "out_of_stock":
		q = q.Where("priceai_products.in_stock_count <= 0")
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	q = q.Select("priceai_products.*")
	q = applyPriceAIProductSort(q, options.Sort)
	var products []PriceAIProduct
	if err := q.Offset((page - 1) * pageSize).Limit(pageSize).Find(&products).Error; err != nil {
		return nil, 0, err
	}
	return products, total, nil
}

func normalizePriceAIPage(page, pageSize int) (int, int) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	return page, pageSize
}

func applyPriceAIProductSort(q *gorm.DB, sort string) *gorm.DB {
	switch strings.TrimSpace(sort) {
	case "lowest_price_asc":
		return q.Order("priceai_products.lowest_price IS NULL ASC").Order("priceai_products.lowest_price ASC").Order("priceai_products.name ASC").Order("priceai_products.id ASC")
	case "lowest_price_desc":
		return q.Order("priceai_products.lowest_price IS NULL ASC").Order("priceai_products.lowest_price DESC").Order("priceai_products.name ASC").Order("priceai_products.id ASC")
	case "name_asc":
		return q.Order("priceai_products.name ASC").Order("priceai_products.id ASC")
	case "in_stock_desc":
		return q.Order("priceai_products.in_stock_count DESC").Order("priceai_products.name ASC").Order("priceai_products.id ASC")
	case "latest_seen_desc":
		return q.Order("priceai_products.latest_seen_at DESC").Order("priceai_products.id DESC")
	default:
		return q.Order("priceai_products.last_seen_at DESC").Order("priceai_products.id DESC")
	}
}

func (r *PriceAI) MarkProductsMissingFromLatest(snapshotID string, at time.Time) (int64, error) {
	if strings.TrimSpace(snapshotID) == "" {
		return 0, fmt.Errorf("snapshot id is required when marking missing products")
	}
	result := r.db.Model(&PriceAIProduct{}).
		Where("last_snapshot_id <> ? AND missing_from_latest_at IS NULL", snapshotID).
		Update("missing_from_latest_at", at)
	return result.RowsAffected, result.Error
}

func (r *PriceAI) ListProductsMissingFromLatest(snapshotID string) ([]PriceAIProduct, error) {
	if strings.TrimSpace(snapshotID) == "" {
		return nil, fmt.Errorf("snapshot id is required when listing missing products")
	}
	var products []PriceAIProduct
	if err := r.db.Where("last_snapshot_id <> ? AND missing_from_latest_at IS NULL", snapshotID).
		Order("id ASC").Find(&products).Error; err != nil {
		return nil, err
	}
	return products, nil
}

func (r *PriceAI) FindWatchTargetByProductID(productID uint) (*PriceAIWatchTarget, error) {
	var target PriceAIWatchTarget
	err := r.db.Where("product_id = ?", productID).First(&target).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &target, nil
}

func (r *PriceAI) FindWatchTargetByID(id uint) (*PriceAIWatchTarget, error) {
	if id == 0 {
		return nil, fmt.Errorf("priceai watch target id is required")
	}
	var target PriceAIWatchTarget
	err := r.db.First(&target, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &target, nil
}

func (r *PriceAI) ListWatchTargets() ([]PriceAIWatchTarget, error) {
	var targets []PriceAIWatchTarget
	if err := r.db.Order("id ASC").Find(&targets).Error; err != nil {
		return nil, err
	}
	return targets, nil
}

func (r *PriceAI) CreateWatchTarget(target *PriceAIWatchTarget) error {
	if target == nil || target.ProductID == 0 {
		return fmt.Errorf("priceai watch target product id is required")
	}
	return r.db.Create(target).Error
}

func (r *PriceAI) UpdateWatchTarget(target *PriceAIWatchTarget) error {
	if target == nil || target.ID == 0 {
		return fmt.Errorf("priceai watch target id is required")
	}
	return r.db.Save(target).Error
}

// MarkWatchTargetNotified persists target-level de-duplication after a
// notification dispatch succeeds. It intentionally does not use the global
// notification cooldown table, whose identity is an upstream channel.
func (r *PriceAI) MarkWatchTargetNotified(id uint, snapshotID string, at time.Time) error {
	if id == 0 || strings.TrimSpace(snapshotID) == "" {
		return fmt.Errorf("priceai watch target id and snapshot id are required")
	}
	result := r.db.Model(&PriceAIWatchTarget{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"last_notified_snapshot_id": snapshotID,
			"last_notified_at":          at,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *PriceAI) DeleteWatchTarget(id uint) error {
	return r.db.Delete(&PriceAIWatchTarget{}, id).Error
}

func (r *PriceAI) UpsertPreset(preset *PriceAIPreset) (*PriceAIPreset, error) {
	if preset == nil || preset.ProductID == 0 || strings.TrimSpace(preset.RemoteID) == "" {
		return nil, fmt.Errorf("priceai preset product id and remote id are required")
	}
	if err := r.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "product_id"}, {Name: "remote_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"label", "group_name", "description", "total", "generated_at", "last_snapshot_id", "raw_json", "updated_at",
		}),
	}).Create(preset).Error; err != nil {
		return nil, err
	}
	var stored PriceAIPreset
	if err := r.db.Where("product_id = ? AND remote_id = ?", preset.ProductID, preset.RemoteID).First(&stored).Error; err != nil {
		return nil, err
	}
	*preset = stored
	return preset, nil
}

func (r *PriceAI) UpsertOffer(offer *PriceAIOffer) (*PriceAIOffer, error) {
	if offer == nil || offer.ProductID == 0 || strings.TrimSpace(offer.DedupeKey) == "" {
		return nil, fmt.Errorf("priceai offer product id and dedupe key are required")
	}
	if err := r.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "product_id"}, {Name: "dedupe_key"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"remote_id", "source_id", "source_name", "source_store_name", "merchant_key", "title", "normalized_title",
			"price", "currency", "status", "url", "last_snapshot_id", "last_seen_at", "raw_json", "updated_at",
		}),
	}).Create(offer).Error; err != nil {
		return nil, err
	}
	var stored PriceAIOffer
	if err := r.db.Where("product_id = ? AND dedupe_key = ?", offer.ProductID, offer.DedupeKey).First(&stored).Error; err != nil {
		return nil, err
	}
	*offer = stored
	return offer, nil
}

func (r *PriceAI) UpsertOfferRanking(ranking *PriceAIOfferRanking) (*PriceAIOfferRanking, error) {
	if ranking == nil || ranking.ProductID == 0 || ranking.OfferID == 0 || ranking.BoardKind == "" {
		return nil, fmt.Errorf("priceai offer ranking product, offer, and board kind are required")
	}
	if err := r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "product_id"}, {Name: "board_kind"}, {Name: "preset_id"}, {Name: "offer_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"rank", "board_generated_at", "last_snapshot_id", "updated_at"}),
	}).Create(ranking).Error; err != nil {
		return nil, err
	}
	var stored PriceAIOfferRanking
	if err := r.db.Where("product_id = ? AND board_kind = ? AND preset_id = ? AND offer_id = ?", ranking.ProductID, ranking.BoardKind, ranking.PresetID, ranking.OfferID).
		First(&stored).Error; err != nil {
		return nil, err
	}
	*ranking = stored
	return ranking, nil
}

// ListOffers returns only the current PriceAI public-board offers retained for
// a product. It is not a claim about offers outside the published boards.
func (r *PriceAI) ListOffers(productID uint) ([]PriceAIOffer, error) {
	if productID == 0 {
		return nil, fmt.Errorf("priceai product id is required")
	}
	var offers []PriceAIOffer
	if err := r.db.Where("product_id = ?", productID).Order("id ASC").Find(&offers).Error; err != nil {
		return nil, err
	}
	return offers, nil
}

func (r *PriceAI) FindOfferByID(id uint) (*PriceAIOffer, error) {
	if id == 0 {
		return nil, fmt.Errorf("priceai offer id is required")
	}
	var offer PriceAIOffer
	err := r.db.First(&offer, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &offer, nil
}

func (r *PriceAI) ListPresets(productID uint) ([]PriceAIPreset, error) {
	if productID == 0 {
		return nil, fmt.Errorf("priceai product id is required")
	}
	var presets []PriceAIPreset
	if err := r.db.Where("product_id = ?", productID).Order("label ASC").Order("remote_id ASC").Find(&presets).Error; err != nil {
		return nil, err
	}
	return presets, nil
}

// PriceAIOfferBoardRow preserves every current public-board membership for an
// offer so callers can group offers without treating the board as a full market.
type PriceAIOfferBoardRow struct {
	OfferID          uint             `gorm:"column:offer_id" json:"offer_id"`
	RemoteID         string           `gorm:"column:remote_id" json:"remote_id,omitempty"`
	DedupeKey        string           `gorm:"column:dedupe_key" json:"dedupe_key"`
	SourceID         string           `gorm:"column:source_id" json:"source_id,omitempty"`
	SourceName       string           `gorm:"column:source_name" json:"source_name,omitempty"`
	SourceStoreName  string           `gorm:"column:source_store_name" json:"source_store_name,omitempty"`
	MerchantKey      string           `gorm:"column:merchant_key" json:"merchant_key"`
	Title            string           `gorm:"column:title" json:"title"`
	NormalizedTitle  string           `gorm:"column:normalized_title" json:"normalized_title"`
	Price            float64          `gorm:"column:price" json:"price"`
	Currency         string           `gorm:"column:currency" json:"currency,omitempty"`
	Status           string           `gorm:"column:status" json:"status,omitempty"`
	URL              string           `gorm:"column:url" json:"url"`
	LastSnapshotID   string           `gorm:"column:last_snapshot_id" json:"last_snapshot_id"`
	LastSeenAt       time.Time        `gorm:"column:last_seen_at" json:"last_seen_at"`
	BoardKind        PriceAIBoardKind `gorm:"column:board_kind" json:"board_kind"`
	PresetID         string           `gorm:"column:preset_id" json:"preset_id,omitempty"`
	Rank             int              `gorm:"column:rank" json:"rank"`
	BoardGeneratedAt time.Time        `gorm:"column:board_generated_at" json:"board_generated_at"`
}

func (r *PriceAI) ListOfferBoardRows(productID uint) ([]PriceAIOfferBoardRow, error) {
	if productID == 0 {
		return nil, fmt.Errorf("priceai product id is required")
	}
	var rows []PriceAIOfferBoardRow
	err := r.db.Table("priceai_offer_rankings AS rankings").
		Select("offers.id AS offer_id, offers.remote_id, offers.dedupe_key, offers.source_id, offers.source_name, offers.source_store_name, offers.merchant_key, offers.title, offers.normalized_title, offers.price, offers.currency, offers.status, offers.url, offers.last_snapshot_id, offers.last_seen_at, rankings.board_kind, rankings.preset_id, rankings.rank, rankings.board_generated_at").
		Joins("JOIN priceai_offers AS offers ON offers.id = rankings.offer_id").
		Where("rankings.product_id = ?", productID).
		Order("rankings.board_kind ASC").Order("rankings.preset_id ASC").Order("rankings.rank ASC").Order("offers.id ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// PriceAIPublicBoardEntry is the compact, current-state representation used
// for public-board change detection. It intentionally has no claim about
// offers outside the imported public boards.
type PriceAIPublicBoardEntry struct {
	OfferID    uint             `gorm:"column:offer_id"`
	DedupeKey  string           `gorm:"column:dedupe_key"`
	BoardKind  PriceAIBoardKind `gorm:"column:board_kind"`
	PresetID   string           `gorm:"column:preset_id"`
	Rank       int              `gorm:"column:rank"`
	Price      float64          `gorm:"column:price"`
	Status     string           `gorm:"column:status"`
	Currency   string           `gorm:"column:currency"`
	MerchantID string           `gorm:"column:merchant_key"`
}

func (r *PriceAI) ListCurrentBoardEntries(productID uint) ([]PriceAIPublicBoardEntry, error) {
	var entries []PriceAIPublicBoardEntry
	err := r.db.Table("priceai_offer_rankings AS rankings").
		Select("rankings.offer_id, offers.dedupe_key, rankings.board_kind, rankings.preset_id, rankings.rank, offers.price, offers.status, offers.currency, offers.merchant_key").
		Joins("JOIN priceai_offers AS offers ON offers.id = rankings.offer_id").
		Where("rankings.product_id = ?", productID).
		Order("rankings.board_kind ASC").Order("rankings.preset_id ASC").Order("rankings.rank ASC").Order("rankings.offer_id ASC").
		Scan(&entries).Error
	if err != nil {
		return nil, err
	}
	return entries, nil
}

func (r *PriceAI) UpsertProductHistory(history *PriceAIProductHistory) error {
	if history == nil || history.ProductID == 0 || strings.TrimSpace(history.SnapshotID) == "" {
		return fmt.Errorf("priceai product history product id and snapshot id are required")
	}
	return r.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "product_id"}, {Name: "snapshot_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"lowest_price", "lowest_price_currency", "in_stock_count", "offer_count", "product_snapshot_generated_at", "feed_stale", "captured_at",
		}),
	}).Create(history).Error
}

func (r *PriceAI) ListProductHistory(productID uint, page, pageSize int) ([]PriceAIProductHistory, int64, error) {
	page, pageSize = normalizePriceAIPage(page, pageSize)
	q := r.db.Model(&PriceAIProductHistory{}).Where("product_id = ?", productID)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []PriceAIProductHistory
	if err := q.Order("captured_at DESC").Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

func (r *PriceAI) AppendChangeLog(log *PriceAIChangeLog) error {
	if log == nil || log.Event == "" {
		return fmt.Errorf("priceai change log event is required")
	}
	if log.OccurredAt.IsZero() {
		log.OccurredAt = time.Now()
	}
	return r.db.Create(log).Error
}

func (r *PriceAI) ListChangeLogs(productID uint, page, pageSize int) ([]PriceAIChangeLog, int64, error) {
	page, pageSize = normalizePriceAIPage(page, pageSize)
	q := r.db.Model(&PriceAIChangeLog{})
	if productID != 0 {
		q = q.Where("product_id = ?", productID)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []PriceAIChangeLog
	if err := q.Order("occurred_at DESC").Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

func (r *PriceAI) AppendSyncLog(log *PriceAISyncLog) error {
	if log == nil || log.JobKind == "" {
		return fmt.Errorf("priceai sync log job kind is required")
	}
	if log.StartedAt.IsZero() {
		log.StartedAt = time.Now()
	}
	if log.FinishedAt.IsZero() {
		log.FinishedAt = log.StartedAt
	}
	if log.DurationMS == 0 {
		log.DurationMS = log.FinishedAt.Sub(log.StartedAt).Milliseconds()
	}
	return r.db.Create(log).Error
}

func (r *PriceAI) ListSyncLogs(jobKind PriceAISyncJobKind, page, pageSize int) ([]PriceAISyncLog, int64, error) {
	page, pageSize = normalizePriceAIPage(page, pageSize)
	q := r.db.Model(&PriceAISyncLog{})
	if jobKind != "" {
		q = q.Where("job_kind = ?", jobKind)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []PriceAISyncLog
	if err := q.Order("started_at DESC").Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

func (r *PriceAI) UpsertRiskFeedback(feedback *PriceAIRiskFeedback) (*PriceAIRiskFeedback, error) {
	if feedback == nil || feedback.ProductID == 0 || feedback.Scope == "" || strings.TrimSpace(feedback.SubjectRemoteID) == "" {
		return nil, fmt.Errorf("priceai risk feedback product, scope, and subject are required")
	}
	if err := r.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "product_id"}, {Name: "scope"}, {Name: "subject_remote_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"status", "feedback_count", "reasons_json", "summaries_json", "latest_at", "page_url", "fetched_at", "last_error", "raw_json", "updated_at",
		}),
	}).Create(feedback).Error; err != nil {
		return nil, err
	}
	var stored PriceAIRiskFeedback
	if err := r.db.Where("product_id = ? AND scope = ? AND subject_remote_id = ?", feedback.ProductID, feedback.Scope, feedback.SubjectRemoteID).
		First(&stored).Error; err != nil {
		return nil, err
	}
	*feedback = stored
	return feedback, nil
}

func (r *PriceAI) ListRiskFeedback(productID uint) ([]PriceAIRiskFeedback, error) {
	var feedback []PriceAIRiskFeedback
	if err := r.db.Where("product_id = ?", productID).Order("scope ASC").Order("subject_remote_id ASC").Find(&feedback).Error; err != nil {
		return nil, err
	}
	return feedback, nil
}

// SetRiskFeedbackError preserves the last successfully extracted payload while
// exposing the most recent page-fetch or parser failure to the UI.
func (r *PriceAI) SetRiskFeedbackError(productID uint, message string) (int64, error) {
	if productID == 0 {
		return 0, fmt.Errorf("priceai product id is required")
	}
	result := r.db.Model(&PriceAIRiskFeedback{}).
		Where("product_id = ?", productID).
		Update("last_error", strings.TrimSpace(message))
	return result.RowsAffected, result.Error
}

type PriceAIBoardPruneResult struct {
	RankingsDeleted int64
	OffersDeleted   int64
	PresetsDeleted  int64
}

// PruneCurrentBoards removes only public-board state that was absent from the
// committed snapshot. Product history is intentionally untouched.
func (r *PriceAI) PruneCurrentBoards(snapshotID string) (PriceAIBoardPruneResult, error) {
	if strings.TrimSpace(snapshotID) == "" {
		return PriceAIBoardPruneResult{}, fmt.Errorf("snapshot id is required when pruning priceai boards")
	}
	result := PriceAIBoardPruneResult{}
	rankings := r.db.Where("last_snapshot_id <> ?", snapshotID).Delete(&PriceAIOfferRanking{})
	if rankings.Error != nil {
		return result, rankings.Error
	}
	result.RankingsDeleted = rankings.RowsAffected

	activeRanking := r.db.Model(&PriceAIOfferRanking{}).
		Select("1").
		Where("priceai_offer_rankings.offer_id = priceai_offers.id")
	offers := r.db.Where("NOT EXISTS (?)", activeRanking).Delete(&PriceAIOffer{})
	if offers.Error != nil {
		return result, offers.Error
	}
	result.OffersDeleted = offers.RowsAffected

	presets := r.db.Where("last_snapshot_id <> ?", snapshotID).Delete(&PriceAIPreset{})
	if presets.Error != nil {
		return result, presets.Error
	}
	result.PresetsDeleted = presets.RowsAffected
	return result, nil
}

func (r *PriceAI) DeleteProductHistoryBefore(cutoff time.Time) (int64, error) {
	result := r.db.Where("captured_at < ?", cutoff).Delete(&PriceAIProductHistory{})
	return result.RowsAffected, result.Error
}

func (r *PriceAI) DeleteChangeLogsBefore(cutoff time.Time) (int64, error) {
	result := r.db.Where("occurred_at < ?", cutoff).Delete(&PriceAIChangeLog{})
	return result.RowsAffected, result.Error
}

func (r *PriceAI) DeleteSyncLogsBefore(cutoff time.Time) (int64, error) {
	result := r.db.Where("started_at < ?", cutoff).Delete(&PriceAISyncLog{})
	return result.RowsAffected, result.Error
}

func (r *PriceAI) FindLDXPTargetBinding(platform ShopPlatform, baseURL, token string) (*PriceAILDXPTargetBinding, error) {
	platform, baseURL, token = normalizePriceAILDXPBindingIdentity(platform, baseURL, token)
	var binding PriceAILDXPTargetBinding
	err := r.db.Where("platform = ? AND base_url = ? AND token = ?", platform, baseURL, token).First(&binding).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &binding, nil
}

func (r *PriceAI) CreateLDXPTargetBinding(binding *PriceAILDXPTargetBinding) error {
	if binding == nil || binding.ShopTargetID == 0 {
		return fmt.Errorf("priceai ldxp target binding shop target id is required")
	}
	binding.Platform, binding.BaseURL, binding.Token = normalizePriceAILDXPBindingIdentity(binding.Platform, binding.BaseURL, binding.Token)
	if binding.Platform == "" || binding.BaseURL == "" || binding.Token == "" {
		return fmt.Errorf("priceai ldxp target binding identity is required")
	}
	return r.db.Create(binding).Error
}

func (r *PriceAI) DeleteLDXPTargetBindingByShopTargetID(shopTargetID uint) error {
	return r.db.Where("shop_target_id = ?", shopTargetID).Delete(&PriceAILDXPTargetBinding{}).Error
}

func normalizePriceAILDXPBindingIdentity(platform ShopPlatform, baseURL, token string) (ShopPlatform, string, string) {
	platform = ShopPlatform(strings.ToLower(strings.TrimSpace(string(platform))))
	baseURL = strings.TrimRight(strings.ToLower(strings.TrimSpace(baseURL)), "/")
	token = strings.TrimSpace(token)
	return platform, baseURL, token
}
