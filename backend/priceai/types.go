package priceai

import (
	"encoding/json"
	"time"
)

const (
	DiscoverySchemaVersion = "price-radar.discovery.v1"
	FeedSchemaVersion      = "price-radar.v1"
)

// DiscoveryDocument is PriceAI's documented public Feed discovery contract.
type DiscoveryDocument struct {
	SchemaVersion          string `json:"schema_version"`
	Name                   string `json:"name"`
	Description            string `json:"description"`
	LatestURL              string `json:"latest_url"`
	SchemaURL              string `json:"schema_url"`
	DocumentationURL       string `json:"documentation_url"`
	RefreshIntervalSeconds int    `json:"refresh_interval_seconds"`
	Authentication         string `json:"authentication"`
	License                string `json:"license"`
	Homepage               string `json:"homepage"`
}

type Pointer struct {
	SchemaVersion        string    `json:"schema_version"`
	SnapshotID           string    `json:"snapshot_id"`
	GeneratedAt          time.Time `json:"generated_at"`
	PublishedAt          time.Time `json:"published_at"`
	Stale                bool      `json:"stale"`
	RankingPolicyVersion string    `json:"ranking_policy_version"`
	SnapshotURL          string    `json:"snapshot_url"`
	ProductCount         int       `json:"product_count"`
	ResourceCount        int       `json:"resource_count"`
}

type Snapshot struct {
	SchemaVersion        string        `json:"schema_version"`
	SnapshotID           string        `json:"snapshot_id"`
	GeneratedAt          time.Time     `json:"generated_at"`
	PublishedAt          time.Time     `json:"published_at"`
	Stale                bool          `json:"stale"`
	RankingPolicyVersion string        `json:"ranking_policy_version"`
	Products             []FeedProduct `json:"products"`
}

type FeedProduct struct {
	ID                  string          `json:"id"`
	Slug                string          `json:"slug"`
	Name                string          `json:"name"`
	Platform            string          `json:"platform"`
	ProductType         string          `json:"product_type"`
	Spec                *string         `json:"spec"`
	Summary             *string         `json:"summary"`
	OfferCount          int             `json:"offer_count"`
	InStockCount        int             `json:"in_stock_count"`
	LowestPrice         *float64        `json:"lowest_price"`
	LowestOffer         *FeedOffer      `json:"lowest_offer"`
	LatestSeenAt        *time.Time      `json:"latest_seen_at"`
	SnapshotGeneratedAt time.Time       `json:"snapshot_generated_at"`
	Total               int             `json:"total"`
	TopOffers           []FeedOffer     `json:"top_offers"`
	Presets             []FeedPreset    `json:"presets"`
	RawJSON             json.RawMessage `json:"-"`
}

type FeedPreset struct {
	ID          string          `json:"id"`
	Label       string          `json:"label"`
	Group       string          `json:"group"`
	Description string          `json:"description"`
	Total       int             `json:"total"`
	GeneratedAt time.Time       `json:"generated_at"`
	TopOffers   []FeedOffer     `json:"top_offers"`
	RawJSON     json.RawMessage `json:"-"`
}

// FeedOffer includes the documented core fields and the observed, additive
// fields emitted by the live Feed. RawJSON is retained for future-compatible
// storage without using undocumented endpoints.
type FeedOffer struct {
	ID               string          `json:"id"`
	SourceID         *string         `json:"source_id"`
	SourceName       string          `json:"source_name"`
	SourceStoreName  *string         `json:"source_store_name"`
	Title            string          `json:"title"`
	Price            float64         `json:"price"`
	Currency         string          `json:"currency"`
	Status           string          `json:"status"`
	URL              string          `json:"url"`
	StockCount       *float64        `json:"stock_count"`
	MinOrderQuantity *float64        `json:"min_order_quantity"`
	CapturedAt       *time.Time      `json:"captured_at"`
	LastSeenAt       *time.Time      `json:"last_seen_at"`
	VerifiedAt       *time.Time      `json:"verified_at"`
	ExpiresAt        *time.Time      `json:"expires_at"`
	EffectiveStatus  *string         `json:"effective_status"`
	FreshnessStatus  *string         `json:"freshness_status"`
	RawJSON          json.RawMessage `json:"-"`
}

func (p *FeedProduct) UnmarshalJSON(data []byte) error {
	type decoded FeedProduct
	var value decoded
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*p = FeedProduct(value)
	p.RawJSON = append(p.RawJSON[:0], data...)
	return nil
}

func (p *FeedPreset) UnmarshalJSON(data []byte) error {
	type decoded FeedPreset
	var value decoded
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*p = FeedPreset(value)
	p.RawJSON = append(p.RawJSON[:0], data...)
	return nil
}

func (o *FeedOffer) UnmarshalJSON(data []byte) error {
	type decoded FeedOffer
	var value decoded
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*o = FeedOffer(value)
	o.RawJSON = append(o.RawJSON[:0], data...)
	return nil
}
