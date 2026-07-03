package shopprovider

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/ifty-r/upstream-ops/backend/storage"
)

type Target struct {
	ID       uint
	Name     string
	Platform storage.ShopPlatform
	SiteURL  string
	BaseURL  string
	Token    string
}

type ShopInfo struct {
	Name       string `json:"name"`
	Link       string `json:"link"`
	Avatar     string `json:"avatar,omitempty"`
	GoodsCount int    `json:"goods_count"`
	RawJSON    string `json:"raw_json,omitempty"`
}

type CategoryRequest struct {
	GoodsType   string
	CategoryKey string
}

type Category struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Image      string `json:"image,omitempty"`
	GoodsCount int    `json:"goods_count"`
}

type GoodsRequest struct {
	GoodsType  string
	CategoryID int64
	Keywords   string
	Page       int
	PageSize   int
}

type GoodsPage struct {
	Total int     `json:"total"`
	List  []Goods `json:"list"`
}

type Goods struct {
	GoodsKey      string  `json:"goods_key"`
	GoodsType     string  `json:"goods_type"`
	Name          string  `json:"name"`
	Link          string  `json:"link"`
	Price         float64 `json:"price"`
	MarketPrice   float64 `json:"market_price"`
	CategoryID    int64   `json:"category_id"`
	CategoryName  string  `json:"category_name"`
	StockCount    int     `json:"stock_count"`
	LimitCount    int     `json:"limit_count"`
	SendOrder     int     `json:"send_order"`
	ContactFormat string  `json:"contact_format"`
	RawJSON       string  `json:"raw_json,omitempty"`
}

type PriceRequest struct {
	GoodsKey string
	Quantity int
}

type PriceResult struct {
	OriginalAmount float64 `json:"original_amount"`
	TotalAmount    float64 `json:"total_amount"`
	RawJSON        string  `json:"raw_json,omitempty"`
}

type Provider interface {
	Info(ctx context.Context, target Target) (*ShopInfo, error)
	Categories(ctx context.Context, target Target, req CategoryRequest) ([]Category, error)
	Goods(ctx context.Context, target Target, req GoodsRequest) (*GoodsPage, error)
	Price(ctx context.Context, target Target, req PriceRequest) (*PriceResult, error)
}

type ProxySetter interface {
	SetProxy(proxyURL string)
}

type HTTPConfig struct {
	Timeout   time.Duration
	UserAgent string
}

type HTTPConfigSetter interface {
	SetHTTPConfig(cfg HTTPConfig)
}

type Factory func() Provider

var (
	mu       sync.RWMutex
	registry = map[storage.ShopPlatform]Factory{}
)

func Register(platform storage.ShopPlatform, factory Factory) {
	mu.Lock()
	defer mu.Unlock()
	registry[platform] = factory
}

func For(platform storage.ShopPlatform) (Provider, error) {
	mu.RLock()
	defer mu.RUnlock()
	factory, ok := registry[platform]
	if !ok {
		return nil, fmt.Errorf("shop provider %q is not registered", platform)
	}
	return factory(), nil
}

type ParsedURL struct {
	Platform  storage.ShopPlatform `json:"platform"`
	BaseURL   string               `json:"base_url"`
	Token     string               `json:"token"`
	Name      string               `json:"name,omitempty"`
	NameError string               `json:"name_error,omitempty"`
}

func ParseShopURL(raw string) (*ParsedURL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid shop url")
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	for i := 0; i < len(parts)-1; i++ {
		if strings.EqualFold(parts[i], "shop") && strings.TrimSpace(parts[i+1]) != "" {
			return &ParsedURL{
				Platform: storage.ShopPlatformLDXP,
				BaseURL:  parsed.Scheme + "://" + parsed.Host,
				Token:    strings.TrimSpace(parts[i+1]),
			}, nil
		}
	}
	return nil, fmt.Errorf("unsupported shop url")
}
