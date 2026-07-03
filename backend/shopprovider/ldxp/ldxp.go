package ldxp

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/ifty-r/upstream-ops/backend/shopprovider"
	"github.com/ifty-r/upstream-ops/backend/storage"
)

func init() {
	shopprovider.Register(storage.ShopPlatformLDXP, func() shopprovider.Provider { return New() })
}

type Client struct {
	http *resty.Client
}

func New() *Client {
	return &Client{
		http: resty.New().
			SetTimeout(30*time.Second).
			SetHeader("Accept", "application/json").
			SetHeader("User-Agent", "upstream-ops/0.1"),
	}
}

func (c *Client) SetProxy(proxyURL string) {
	if strings.TrimSpace(proxyURL) != "" {
		c.http.SetProxy(proxyURL)
	}
}

func (c *Client) SetHTTPConfig(cfg shopprovider.HTTPConfig) {
	if cfg.Timeout > 0 {
		c.http.SetTimeout(cfg.Timeout)
	}
	if strings.TrimSpace(cfg.UserAgent) != "" {
		c.http.SetHeader("User-Agent", cfg.UserAgent)
	}
}

type envelope struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

func (c *Client) Info(ctx context.Context, target shopprovider.Target) (*shopprovider.ShopInfo, error) {
	var data struct {
		Nickname   string `json:"nickname"`
		Link       string `json:"link"`
		Avatar     string `json:"avatar"`
		GoodsCount int    `json:"goods_count"`
	}
	raw, err := c.post(ctx, target, "/shopApi/Shop/info", map[string]any{"token": target.Token}, &data)
	if err != nil {
		return nil, err
	}
	return &shopprovider.ShopInfo{
		Name:       data.Nickname,
		Link:       data.Link,
		Avatar:     data.Avatar,
		GoodsCount: data.GoodsCount,
		RawJSON:    string(raw),
	}, nil
}

func (c *Client) Categories(ctx context.Context, target shopprovider.Target, req shopprovider.CategoryRequest) ([]shopprovider.Category, error) {
	var data []struct {
		ID         int64  `json:"id"`
		Name       string `json:"name"`
		Image      string `json:"image"`
		GoodsCount int    `json:"goods_count"`
	}
	body := map[string]any{
		"token":      target.Token,
		"goods_type": emptyDefault(req.GoodsType, "card"),
	}
	if strings.TrimSpace(req.CategoryKey) != "" {
		body["category_key"] = strings.TrimSpace(req.CategoryKey)
	}
	if _, err := c.post(ctx, target, "/shopApi/Shop/categoryList", body, &data); err != nil {
		return nil, err
	}
	out := make([]shopprovider.Category, 0, len(data))
	for _, item := range data {
		out = append(out, shopprovider.Category{
			ID:         item.ID,
			Name:       item.Name,
			Image:      item.Image,
			GoodsCount: item.GoodsCount,
		})
	}
	return out, nil
}

func (c *Client) Goods(ctx context.Context, target shopprovider.Target, req shopprovider.GoodsRequest) (*shopprovider.GoodsPage, error) {
	if req.Page <= 0 {
		req.Page = 1
	}
	if req.PageSize <= 0 {
		req.PageSize = 50
	}
	var data struct {
		Total int               `json:"total"`
		List  []json.RawMessage `json:"list"`
	}
	body := map[string]any{
		"token":       target.Token,
		"keywords":    req.Keywords,
		"category_id": req.CategoryID,
		"goods_type":  emptyDefault(req.GoodsType, "card"),
		"current":     req.Page,
		"pageSize":    req.PageSize,
	}
	if _, err := c.post(ctx, target, "/shopApi/Shop/goodsList", body, &data); err != nil {
		return nil, err
	}
	out := &shopprovider.GoodsPage{Total: data.Total, List: make([]shopprovider.Goods, 0, len(data.List))}
	for _, raw := range data.List {
		var item struct {
			Link          string  `json:"link"`
			GoodsType     string  `json:"goods_type"`
			GoodsKey      string  `json:"goods_key"`
			Name          string  `json:"name"`
			Price         float64 `json:"price"`
			MarketPrice   float64 `json:"market_price"`
			ContactFormat string  `json:"contact_format"`
			Category      struct {
				ID   int64  `json:"id"`
				Name string `json:"name"`
			} `json:"category"`
			Extend struct {
				StockCount int `json:"stock_count"`
				LimitCount int `json:"limit_count"`
				SendOrder  int `json:"send_order"`
			} `json:"extend"`
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, fmt.Errorf("decode ldxp goods item: %w", err)
		}
		out.List = append(out.List, shopprovider.Goods{
			GoodsKey:      item.GoodsKey,
			GoodsType:     item.GoodsType,
			Name:          item.Name,
			Link:          item.Link,
			Price:         item.Price,
			MarketPrice:   item.MarketPrice,
			CategoryID:    item.Category.ID,
			CategoryName:  item.Category.Name,
			StockCount:    item.Extend.StockCount,
			LimitCount:    item.Extend.LimitCount,
			SendOrder:     item.Extend.SendOrder,
			ContactFormat: item.ContactFormat,
			RawJSON:       string(raw),
		})
	}
	return out, nil
}

func (c *Client) Price(ctx context.Context, target shopprovider.Target, req shopprovider.PriceRequest) (*shopprovider.PriceResult, error) {
	if req.Quantity <= 0 {
		req.Quantity = 1
	}
	var data struct {
		OriginalAmount float64 `json:"original_amount"`
		TotalAmount    float64 `json:"total_amount"`
	}
	raw, err := c.post(ctx, target, "/shopApi/Shop/getGoodsPrice", map[string]any{
		"goods_key": req.GoodsKey,
		"quantity":  req.Quantity,
	}, &data)
	if err != nil {
		return nil, err
	}
	return &shopprovider.PriceResult{
		OriginalAmount: data.OriginalAmount,
		TotalAmount:    data.TotalAmount,
		RawJSON:        string(raw),
	}, nil
}

func (c *Client) post(ctx context.Context, target shopprovider.Target, path string, body any, out any) (json.RawMessage, error) {
	base := strings.TrimRight(target.BaseURL, "/")
	if base == "" {
		base = strings.TrimRight(target.SiteURL, "/")
	}
	resp, err := c.postOnce(ctx, base+path, body, "")
	if err != nil {
		return nil, fmt.Errorf("ldxp %s http: %w", path, err)
	}
	if cookie, ok := acwSCV2Cookie(resp.Body()); ok {
		resp, err = c.postOnce(ctx, base+path, body, cookie)
		if err != nil {
			return nil, fmt.Errorf("ldxp %s acw retry http: %w", path, err)
		}
	}
	if resp.IsError() {
		return nil, fmt.Errorf("ldxp %s: http %d: %s", path, resp.StatusCode(), string(resp.Body()))
	}
	var wrapped envelope
	if err := json.Unmarshal(resp.Body(), &wrapped); err != nil {
		return nil, fmt.Errorf("ldxp %s decode: %w", path, err)
	}
	if wrapped.Code != 1 {
		if wrapped.Msg == "" {
			wrapped.Msg = "remote returned non-success code"
		}
		return nil, fmt.Errorf("ldxp %s: %s", path, wrapped.Msg)
	}
	if out != nil && len(wrapped.Data) > 0 && string(wrapped.Data) != "null" {
		if err := json.Unmarshal(wrapped.Data, out); err != nil {
			return nil, fmt.Errorf("ldxp %s data decode: %w", path, err)
		}
	}
	return wrapped.Data, nil
}

func (c *Client) postOnce(ctx context.Context, url string, body any, cookie string) (*resty.Response, error) {
	req := c.http.R().
		SetContext(ctx).
		SetHeader("Content-Type", "application/json;charset=UTF-8").
		SetHeader("Accept", "application/json").
		SetBody(body)
	if cookie != "" {
		req.SetHeader("Cookie", cookie)
	}
	return req.Post(url)
}

var acwArgRe = regexp.MustCompile(`arg1\s*=\s*['"]([0-9A-Fa-f]{40})['"]`)

func acwSCV2Cookie(body []byte) (string, bool) {
	text := string(body)
	if !strings.Contains(text, "acw_sc__v2") {
		return "", false
	}
	match := acwArgRe.FindStringSubmatch(text)
	if len(match) != 2 {
		return "", false
	}
	value, ok := acwSCV2Value(match[1])
	if !ok {
		return "", false
	}
	return "acw_sc__v2=" + value, true
}

func acwSCV2Value(arg1 string) (string, bool) {
	indexes := []int{
		0xf, 0x23, 0x1d, 0x18, 0x21, 0x10, 0x1, 0x26, 0xa, 0x9,
		0x13, 0x1f, 0x28, 0x1b, 0x16, 0x17, 0x19, 0xd, 0x6, 0xb,
		0x27, 0x12, 0x14, 0x8, 0xe, 0x15, 0x20, 0x1a, 0x2, 0x1e,
		0x7, 0x4, 0x11, 0x5, 0x3, 0x1c, 0x22, 0x25, 0xc, 0x24,
	}
	const key = "3000176000856006061501533003690027800375"
	if len(arg1) < len(indexes) || len(key) < len(indexes) {
		return "", false
	}
	unsboxed := make([]byte, len(indexes))
	for i := 0; i < len(indexes); i++ {
		position := indexes[i] - 1
		if position < 0 || position >= len(arg1) {
			return "", false
		}
		unsboxed[i] = arg1[position]
	}
	var b strings.Builder
	for i := 0; i < len(unsboxed) && i < len(key); i += 2 {
		left, err1 := strconv.ParseInt(string(unsboxed[i:i+2]), 16, 64)
		right, err2 := strconv.ParseInt(key[i:i+2], 16, 64)
		if err1 != nil || err2 != nil {
			return "", false
		}
		part := strconv.FormatInt(left^right, 16)
		if len(part) == 1 {
			b.WriteByte('0')
		}
		b.WriteString(part)
	}
	return b.String(), true
}

func emptyDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
