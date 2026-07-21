package shopprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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
	GoodsKey      string        `json:"goods_key"`
	GoodsType     string        `json:"goods_type"`
	Name          string        `json:"name"`
	Link          string        `json:"link"`
	Price         float64       `json:"price"`
	MarketPrice   float64       `json:"market_price"`
	CategoryID    int64         `json:"category_id"`
	CategoryName  string        `json:"category_name"`
	StockCount    int           `json:"stock_count"`
	LimitCount    int           `json:"limit_count"`
	SendOrder     int           `json:"send_order"`
	ContactFormat string        `json:"contact_format"`
	PaymentQuote  *PaymentQuote `json:"payment_quote,omitempty"`
	RawJSON       string        `json:"raw_json,omitempty"`
}

type PaymentChannel struct {
	ID          int64   `json:"id"`
	Name        string  `json:"name"`
	DisplayName string  `json:"display_name"`
	Rate        float64 `json:"rate"`
}

type PaymentQuote struct {
	ChannelID      int64     `json:"channel_id"`
	ChannelName    string    `json:"channel_name"`
	ChannelRate    float64   `json:"channel_rate"`
	Quantity       int       `json:"quantity"`
	OriginalAmount float64   `json:"original_amount"`
	Fee            float64   `json:"fee"`
	FeePayer       int       `json:"fee_payer"`
	TotalAmount    float64   `json:"total_amount"`
	QuotedAt       time.Time `json:"quoted_at"`
}

type PriceRequest struct {
	GoodsKey  string
	Quantity  int
	ChannelID int64
}

type PriceResult struct {
	OriginalAmount float64 `json:"original_amount"`
	TotalAmount    float64 `json:"total_amount"`
	Fee            float64 `json:"fee"`
	FeePayer       int     `json:"fee_payer"`
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
	Timeout         time.Duration
	UserAgent       string
	RequestInterval time.Duration
}

type HTTPConfigSetter interface {
	SetHTTPConfig(cfg HTTPConfig)
}

// UpstreamBlockedError marks an origin-wide rejection such as a WAF challenge.
// Bulk synchronization may stop calling the same origin for the rest of the run,
// while single-target operations still return the original error unchanged.
type UpstreamBlockedError struct {
	Err error
}

func (e *UpstreamBlockedError) Error() string {
	if e == nil || e.Err == nil {
		return "shop upstream blocked the request"
	}
	return e.Err.Error()
}

func (e *UpstreamBlockedError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// IsUpstreamBlocked reports whether a provider rejected the request at origin scope.
func IsUpstreamBlocked(err error) bool {
	var blocked *UpstreamBlockedError
	return errors.As(err, &blocked)
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
	SiteURL   string               `json:"site_url"`
	BaseURL   string               `json:"base_url"`
	Token     string               `json:"token"`
	Name      string               `json:"name,omitempty"`
	NameError string               `json:"name_error,omitempty"`
}

type PaymentChannelProvider interface {
	PaymentChannels(ctx context.Context, target Target) ([]PaymentChannel, error)
}

func ParseShopURL(raw string) (*ParsedURL, error) {
	return ParseShopURLContext(context.Background(), raw)
}

func ParseShopURLContext(ctx context.Context, raw string) (*ParsedURL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid shop url")
	}
	baseURL := parsed.Scheme + "://" + parsed.Host
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	for i := 0; i < len(parts)-1; i++ {
		if strings.EqualFold(parts[i], "shop") && strings.TrimSpace(parts[i+1]) != "" {
			return &ParsedURL{
				Platform: storage.ShopPlatformLDXP,
				SiteURL:  baseURL + "/shop/" + strings.TrimSpace(parts[i+1]),
				BaseURL:  baseURL,
				Token:    strings.TrimSpace(parts[i+1]),
			}, nil
		}
		if strings.EqualFold(parts[i], "item") && strings.TrimSpace(parts[i+1]) != "" {
			return resolveLDXPItemURL(ctx, baseURL, strings.TrimSpace(parts[i+1]))
		}
	}
	return nil, fmt.Errorf("unsupported shop url")
}

func resolveLDXPItemURL(ctx context.Context, baseURL, goodsKey string) (*ParsedURL, error) {
	if strings.TrimSpace(goodsKey) == "" {
		return nil, fmt.Errorf("unsupported shop url")
	}
	payload, err := json.Marshal(map[string]string{"goods_key": goodsKey})
	if err != nil {
		return nil, fmt.Errorf("encode goods info request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/shopApi/Shop/goodsInfo", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build goods info request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", baseURL)
	req.Header.Set("Referer", strings.TrimRight(baseURL, "/")+"/item/"+goodsKey)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/137.0.0.0 Safari/537.36")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("resolve item url: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("resolve item url: http %d", resp.StatusCode)
	}
	var envelope struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			User struct {
				Nickname string `json:"nickname"`
				Token    string `json:"token"`
				Link     string `json:"link"`
			} `json:"user"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("decode goods info response: %w", err)
	}
	if envelope.Code != 1 {
		if strings.TrimSpace(envelope.Msg) == "" {
			envelope.Msg = "goods info lookup failed"
		}
		return nil, fmt.Errorf("resolve item url: %s", envelope.Msg)
	}
	token := strings.TrimSpace(envelope.Data.User.Token)
	if token == "" {
		return nil, fmt.Errorf("resolve item url: shop token not found")
	}
	siteURL := strings.TrimSpace(envelope.Data.User.Link)
	if siteURL == "" {
		siteURL = strings.TrimRight(baseURL, "/") + "/shop/" + token
	}
	return &ParsedURL{
		Platform: storage.ShopPlatformLDXP,
		SiteURL:  siteURL,
		BaseURL:  strings.TrimRight(baseURL, "/"),
		Token:    token,
		Name:     strings.TrimSpace(envelope.Data.User.Nickname),
	}, nil
}
