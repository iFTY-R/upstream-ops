// Package newapi 实现对 NewAPI 风格上游站点的 connector，参考 docs/USER_BALANCE_GROUP_RATE_AUTH_API_CN-newapi.md。
package newapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/ifty-r/upstream-ops/backend/connector"
)

func init() {
	connector.Register(connector.TypeNewAPI, func() connector.Connector { return New() })
}

// Client NewAPI connector 实现。
type Client struct {
	http *resty.Client
}

func New() *Client {
	c := resty.New().
		SetTimeout(30*time.Second).
		SetHeader("User-Agent", connector.DefaultBrowserUserAgent).
		SetHeader("Accept", "application/json")
	return &Client{http: c}
}

func (c *Client) SetProxy(proxyURL string) {
	if strings.TrimSpace(proxyURL) == "" {
		return
	}
	c.http.SetProxy(proxyURL)
}

func (c *Client) SetHTTPConfig(cfg connector.HTTPConfig) {
	if cfg.Timeout > 0 {
		c.http.SetTimeout(cfg.Timeout)
	}
	if strings.TrimSpace(cfg.UserAgent) != "" {
		c.http.SetHeader("User-Agent", cfg.UserAgent)
	}
}

// newapiResp NewAPI 统一响应外壳：{ success, message, data }。
type newapiResp struct {
	Success bool            `json:"success"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func (c *Client) GetTurnstileSiteKey(ctx context.Context, ch *connector.Channel) (string, error) {
	body, err := c.getJSON(ctx, strings.TrimRight(ch.SiteURL, "/")+"/api/status", nil)
	if err != nil {
		return "", fmt.Errorf("newapi status: %w", err)
	}
	var status struct {
		TurnstileCheck   bool   `json:"turnstile_check"`
		TurnstileSiteKey string `json:"turnstile_site_key"`
	}
	if err := json.Unmarshal(body, &status); err != nil {
		return "", fmt.Errorf("newapi status decode: %w", err)
	}
	if !status.TurnstileCheck {
		return "", nil
	}
	return status.TurnstileSiteKey, nil
}

func (c *Client) Login(ctx context.Context, ch *connector.Channel) (*connector.AuthSession, error) {
	site := strings.TrimRight(ch.SiteURL, "/")
	body := map[string]any{
		"username": ch.Username,
		"password": ch.Password,
	}
	for k, v := range ch.LoginExtraParams {
		body[k] = v
	}
	req := c.http.R().
		SetContext(ctx).
		SetHeader("Content-Type", "application/json").
		SetBody(body)
	if ch.TurnstileToken != "" {
		req.SetQueryParam("turnstile", ch.TurnstileToken)
	}

	resp, err := req.Post(site + "/api/user/login")
	if err != nil {
		return nil, fmt.Errorf("newapi login http: %w", err)
	}
	if resp.IsError() {
		return nil, fmt.Errorf("newapi login: %w", connector.HTTPStatusError(resp.StatusCode(), resp.Body()))
	}
	var wrapped newapiResp
	if err := json.Unmarshal(resp.Body(), &wrapped); err != nil {
		return nil, fmt.Errorf("newapi login decode: %w", err)
	}
	if !wrapped.Success {
		return nil, fmt.Errorf("newapi login: %s", wrapped.Message)
	}

	var data struct {
		Require2FA bool  `json:"require_2fa"`
		ID         int64 `json:"id"`
	}
	_ = json.Unmarshal(wrapped.Data, &data)
	if data.Require2FA {
		return nil, errors.New("newapi account requires 2FA; please disable it for monitoring accounts")
	}

	cookie := joinCookies(resp.Cookies())
	if cookie == "" {
		return nil, errors.New("newapi login: no session cookie returned")
	}
	if data.ID == 0 {
		// 用户 id 是后续 New-Api-User 头的必需值；缺失说明响应格式不对。
		return nil, errors.New("newapi login: missing user id in response")
	}
	// NewAPI session 默认有效期较长，保守按 7 天估算；CheckAuth 会兜底失效检测。
	return &connector.AuthSession{
		UserID:    strconv.FormatInt(data.ID, 10),
		Cookie:    cookie,
		ExpiresAt: time.Now().Add(7 * 24 * time.Hour),
	}, nil
}

func (c *Client) CheckAuth(ctx context.Context, ch *connector.Channel, session *connector.AuthSession) error {
	if session == nil || !newAPIHasAuth(session) {
		return errors.New("missing newapi credential: 需要提供 Cookie 或系统访问令牌")
	}
	_, err := c.getJSON(ctx, strings.TrimRight(ch.SiteURL, "/")+"/api/user/self", session)
	return err
}

// newAPIHasAuth 判断 session 是否带有 NewAPI 通过鉴权所必需的凭据。
// NewAPI 支持两种鉴权：
//   - 浏览器 session：Cookie 头（典型值 session=xxxxx; ...）
//   - 系统访问令牌：Authorization 头（user.access_token，32 位字符串）
//
// 两者都需要搭配 New-Api-User 头，见 applyNewAPIAuth。
func newAPIHasAuth(session *connector.AuthSession) bool {
	if session == nil {
		return false
	}
	return strings.TrimSpace(session.Cookie) != "" || strings.TrimSpace(session.AccessToken) != ""
}

// applyNewAPIAuth 把当前 session 的鉴权头挂到 resty 请求上。
//   - 优先 Cookie（浏览器 session）；
//   - 没填 Cookie 但填了 AccessToken 时改走 Authorization: <token>，对应 NewAPI 的系统访问令牌；
//   - New-Api-User 始终带上（NewAPI 即便用 session 也要求这个头）。
func applyNewAPIAuth(req *resty.Request, session *connector.AuthSession) {
	if session == nil {
		return
	}
	if strings.TrimSpace(session.Cookie) != "" {
		req.SetHeader("Cookie", session.Cookie)
	} else if token := strings.TrimSpace(session.AccessToken); token != "" {
		// NewAPI middleware 会自动去掉 "Bearer " 前缀，这里直接给裸 token，行为最贴近 dashboard。
		req.SetHeader("Authorization", token)
	}
	if strings.TrimSpace(session.UserID) != "" {
		req.SetHeader("New-Api-User", session.UserID)
	}
}

func (c *Client) GetBalance(ctx context.Context, ch *connector.Channel, session *connector.AuthSession) (*connector.BalanceResult, error) {
	site := strings.TrimRight(ch.SiteURL, "/")
	statusBody, err := c.getJSON(ctx, site+"/api/status", nil)
	if err != nil {
		return nil, fmt.Errorf("newapi status: %w", err)
	}
	var status struct {
		QuotaPerUnit float64 `json:"quota_per_unit"`
		Price        float64 `json:"price"`
	}
	if err := json.Unmarshal(statusBody, &status); err != nil {
		return nil, fmt.Errorf("newapi status decode: %w", err)
	}
	if status.QuotaPerUnit <= 0 {
		status.QuotaPerUnit = 500000
	}

	selfBody, err := c.getJSON(ctx, site+"/api/user/self", session)
	if err != nil {
		return nil, fmt.Errorf("newapi self: %w", err)
	}
	var self struct {
		Quota float64 `json:"quota"`
	}
	if err := json.Unmarshal(selfBody, &self); err != nil {
		return nil, fmt.Errorf("newapi self decode: %w", err)
	}
	balance := c.quotaToUSD(self.Quota, status.QuotaPerUnit)
	multiplier := newAPIRechargeMultiplier(ch, status.Price)
	return &connector.BalanceResult{
		Balance:   connector.ApplyRechargeMultiplier(balance, multiplier, ch.RechargeMultiplierMode),
		SampledAt: time.Now(),
	}, nil
}

func (c *Client) GetCosts(ctx context.Context, ch *connector.Channel, session *connector.AuthSession) (*connector.CostResult, error) {
	site := strings.TrimRight(ch.SiteURL, "/")
	statusBody, err := c.getJSON(ctx, site+"/api/status", nil)
	if err != nil {
		return nil, fmt.Errorf("newapi status: %w", err)
	}
	var status struct {
		QuotaPerUnit float64 `json:"quota_per_unit"`
		Price        float64 `json:"price"`
	}
	if err := json.Unmarshal(statusBody, &status); err != nil {
		return nil, fmt.Errorf("newapi status decode: %w", err)
	}
	if status.QuotaPerUnit <= 0 {
		status.QuotaPerUnit = 500000
	}

	now := time.Now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()
	end := now.Unix()
	logBody, err := c.getJSON(ctx, site+"/api/log/self/stat?type=0&token_name=&model_name=&start_timestamp="+strconv.FormatInt(start, 10)+"&end_timestamp="+strconv.FormatInt(end, 10)+"&group=", session)
	if err != nil {
		return nil, fmt.Errorf("newapi self stat: %w", err)
	}
	var todayStat struct {
		Quota float64 `json:"quota"`
	}
	if err := json.Unmarshal(logBody, &todayStat); err != nil {
		return nil, fmt.Errorf("newapi self stat decode: %w", err)
	}

	usageBody, err := c.getJSON(ctx, site+"/api/user/self", session)
	if err != nil {
		return nil, fmt.Errorf("newapi self total: %w", err)
	}
	var usage struct {
		UsedQuota float64 `json:"used_quota"`
	}
	if err := json.Unmarshal(usageBody, &usage); err != nil {
		return nil, fmt.Errorf("newapi self total decode: %w", err)
	}

	todayCost := c.quotaToUSD(todayStat.Quota, status.QuotaPerUnit)
	totalCost := c.quotaToUSD(usage.UsedQuota, status.QuotaPerUnit)
	multiplier := newAPIRechargeMultiplier(ch, status.Price)
	return &connector.CostResult{
		TodayCost: connector.ApplyRechargeMultiplier(todayCost, multiplier, ch.RechargeMultiplierMode),
		TotalCost: connector.ApplyRechargeMultiplier(totalCost, multiplier, ch.RechargeMultiplierMode),
	}, nil
}

func (c *Client) GetRates(ctx context.Context, ch *connector.Channel, session *connector.AuthSession) ([]connector.RateResult, error) {
	body, err := c.getJSON(ctx, strings.TrimRight(ch.SiteURL, "/")+"/api/user/self/groups", session)
	if err != nil {
		return nil, fmt.Errorf("newapi groups: %w", err)
	}
	// data: { "default": { "ratio": 1, "desc": "..." }, "auto": { "ratio": "自动", ... } }
	raw := map[string]struct {
		Ratio json.RawMessage `json:"ratio"`
		Desc  string          `json:"desc"`
	}{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("newapi groups decode: %w", err)
	}
	out := make([]connector.RateResult, 0, len(raw))
	for name, v := range raw {
		var ratio float64
		if err := json.Unmarshal(v.Ratio, &ratio); err != nil {
			// "auto" 组的 ratio 是字符串 "自动"，跳过。
			continue
		}
		out = append(out, connector.RateResult{
			ModelName:   name,
			Description: v.Desc,
			Ratio:       ratio,
		})
	}
	return out, nil
}

func (c *Client) GetAnnouncements(ctx context.Context, ch *connector.Channel, session *connector.AuthSession) ([]connector.AnnouncementResult, error) {
	site := strings.TrimRight(ch.SiteURL, "/")
	var items []connector.AnnouncementResult

	if body, err := c.getJSON(ctx, site+"/api/status", nil); err == nil {
		var status struct {
			Announcements []struct {
				ID              any             `json:"id"`
				Title           string          `json:"title"`
				Content         string          `json:"content"`
				Type            string          `json:"type"`
				Link            string          `json:"link"`
				PublishDate     string          `json:"publishDate"`
				PublishedAt     string          `json:"published_at"`
				CreatedAt       string          `json:"created_at"`
				UpdatedAt       string          `json:"updated_at"`
				SourceUpdatedAt string          `json:"source_updated_at"`
				Extra           json.RawMessage `json:"extra"`
			} `json:"announcements"`
		}
		if err := json.Unmarshal(body, &status); err == nil {
			for _, a := range status.Announcements {
				publishedAt := parseAnnouncementTime(a.PublishDate, a.PublishedAt, a.CreatedAt)
				updatedAt := parseAnnouncementTime(a.SourceUpdatedAt, a.UpdatedAt)
				items = append(items, connector.AnnouncementResult{
					SourceKey:       newAPIAnnouncementSourceKey(a.ID, a.Title, a.Content, a.Type, a.PublishDate, a.PublishedAt, a.CreatedAt, a.UpdatedAt),
					Title:           strings.TrimSpace(a.Title),
					Content:         strings.TrimSpace(a.Content),
					Type:            strings.TrimSpace(a.Type),
					Link:            strings.TrimSpace(a.Link),
					PublishedAt:     publishedAt,
					SourceUpdatedAt: updatedAt,
				})
			}
		}
	}

	if body, err := c.getRaw(ctx, site+"/api/notice", nil); err == nil {
		var wrapped newapiResp
		if err := json.Unmarshal(body, &wrapped); err == nil && wrapped.Success {
			text := strings.TrimSpace(newAPIStringFromRaw(wrapped.Data))
			if text != "" {
				items = append(items, connector.AnnouncementResult{
					SourceKey: hashAnnouncementKey("notice", text),
					Title:     "公告",
					Content:   text,
					Type:      "notice",
				})
			}
		}
	}

	return dedupeAnnouncements(items), nil
}

func (c *Client) RedeemCode(ctx context.Context, ch *connector.Channel, session *connector.AuthSession, code string) (*connector.RedeemResult, error) {
	site := strings.TrimRight(ch.SiteURL, "/")
	statusBody, err := c.getJSON(ctx, site+"/api/status", nil)
	if err != nil {
		return nil, fmt.Errorf("newapi status: %w", err)
	}
	var status struct {
		QuotaPerUnit float64 `json:"quota_per_unit"`
		Price        float64 `json:"price"`
	}
	if err := json.Unmarshal(statusBody, &status); err != nil {
		return nil, fmt.Errorf("newapi status decode: %w", err)
	}
	if status.QuotaPerUnit <= 0 {
		status.QuotaPerUnit = 500000
	}

	req := c.http.R().
		SetContext(ctx).
		SetHeader("Content-Type", "application/json").
		SetBody(map[string]string{"key": code})
	applyNewAPIAuth(req, session)
	resp, err := req.Post(site + "/api/user/topup")
	if err != nil {
		return nil, fmt.Errorf("newapi redeem http: %w", err)
	}
	if resp.IsError() {
		return nil, fmt.Errorf("newapi redeem: %w", connector.HTTPStatusError(resp.StatusCode(), resp.Body()))
	}
	var wrapped newapiResp
	if err := json.Unmarshal(resp.Body(), &wrapped); err != nil {
		return nil, fmt.Errorf("newapi redeem decode: %w", err)
	}
	if !wrapped.Success {
		return nil, fmt.Errorf("newapi redeem: %s", wrapped.Message)
	}

	var quota float64
	if err := json.Unmarshal(wrapped.Data, &quota); err != nil {
		return nil, fmt.Errorf("newapi redeem data: %w", err)
	}
	value := quota / status.QuotaPerUnit
	multiplier := newAPIRechargeMultiplier(ch, status.Price)
	return &connector.RedeemResult{
		Message: "兑换成功",
		Type:    "balance",
		Value:   connector.ApplyRechargeMultiplier(value, multiplier, ch.RechargeMultiplierMode),
	}, nil
}

func (c *Client) GetRechargeInfo(ctx context.Context, ch *connector.Channel, session *connector.AuthSession) (*connector.RechargeInfo, error) {
	body, err := c.getJSON(ctx, strings.TrimRight(ch.SiteURL, "/")+"/api/user/topup/info", session)
	if err != nil {
		return nil, fmt.Errorf("newapi topup info: %w", err)
	}
	var raw struct {
		PayMethods    json.RawMessage `json:"pay_methods"`
		MinTopup      float64         `json:"min_topup"`
		AmountOptions json.RawMessage `json:"amount_options"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("newapi topup info decode: %w", err)
	}
	payMethods, err := parseNewAPIPayMethods(raw.PayMethods)
	if err != nil {
		return nil, fmt.Errorf("newapi topup methods decode: %w", err)
	}
	amountOptions, err := parseNewAPIAmountOptions(raw.AmountOptions)
	if err != nil {
		return nil, fmt.Errorf("newapi amount options decode: %w", err)
	}
	methods := make([]connector.RechargeMethod, 0, len(payMethods))
	for _, m := range payMethods {
		t := strings.TrimSpace(m.Type)
		if t != "alipay" && t != "wxpay" {
			continue
		}
		minAmount := raw.MinTopup
		if parsed, err := strconv.ParseFloat(strings.TrimSpace(m.MinTopup), 64); err == nil && parsed > 0 {
			minAmount = parsed
		}
		methods = append(methods, connector.RechargeMethod{
			Type:      t,
			Name:      strings.TrimSpace(m.Name),
			MinAmount: minAmount,
		})
	}
	if len(methods) == 0 {
		return nil, errors.New("上游未配置可用的支付宝/微信支付方式")
	}
	if raw.MinTopup <= 0 {
		raw.MinTopup = methods[0].MinAmount
	}
	if raw.MinTopup <= 0 {
		raw.MinTopup = 1
	}
	if len(amountOptions) == 0 && raw.MinTopup > 0 {
		amountOptions = []float64{raw.MinTopup}
	}
	return &connector.RechargeInfo{
		AmountLabel:   "充值数量",
		AmountStep:    1,
		MinAmount:     raw.MinTopup,
		PresetAmounts: amountOptions,
		Methods:       methods,
	}, nil
}

func (c *Client) CreateRecharge(ctx context.Context, ch *connector.Channel, session *connector.AuthSession, req connector.RechargeRequest) (*connector.RechargeLaunch, error) {
	if req.PaymentMethod != "alipay" && req.PaymentMethod != "wxpay" {
		return nil, errors.New("newapi 仅支持 alipay 或 wxpay")
	}
	if req.Amount <= 0 || math.Trunc(req.Amount) != req.Amount {
		return nil, errors.New("newapi 充值数量必须是正整数")
	}
	r := c.http.R().
		SetContext(ctx).
		SetHeader("Content-Type", "application/json").
		SetBody(map[string]any{
			"amount":         int64(req.Amount),
			"payment_method": req.PaymentMethod,
		})
	applyNewAPIAuth(r, session)
	resp, err := r.Post(strings.TrimRight(ch.SiteURL, "/") + "/api/user/pay")
	if err != nil {
		return nil, fmt.Errorf("newapi create recharge http: %w", err)
	}
	if resp.IsError() {
		return nil, fmt.Errorf("newapi create recharge: %w", connector.HTTPStatusError(resp.StatusCode(), resp.Body()))
	}
	var wrapped struct {
		Success bool            `json:"success"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
		URL     string          `json:"url"`
	}
	if err := json.Unmarshal(resp.Body(), &wrapped); err != nil {
		return nil, fmt.Errorf("newapi create recharge decode: %w", err)
	}
	if !wrapped.Success && !strings.EqualFold(strings.TrimSpace(wrapped.Message), "success") {
		msg := newAPIStringFromRaw(wrapped.Data)
		if msg == "" || strings.EqualFold(msg, "null") {
			msg = strings.TrimSpace(wrapped.Message)
		}
		if msg == "" {
			msg = "newapi 发起支付失败"
		}
		return nil, errors.New(msg)
	}
	fields := map[string]string{}
	if len(wrapped.Data) > 0 && string(wrapped.Data) != "null" {
		var rawFields map[string]any
		if err := json.Unmarshal(wrapped.Data, &rawFields); err != nil {
			return nil, fmt.Errorf("newapi create recharge data: %w", err)
		}
		for k, v := range rawFields {
			fields[k] = fmt.Sprint(v)
		}
	}
	action := strings.TrimSpace(wrapped.URL)
	if action == "" || len(fields) == 0 {
		return nil, errors.New("newapi 返回的支付表单信息不完整")
	}
	return &connector.RechargeLaunch{
		Mode:       "form",
		FormAction: action,
		FormFields: fields,
	}, nil
}

func (c *Client) GetSubscriptionInfo(ctx context.Context, ch *connector.Channel, session *connector.AuthSession) (*connector.SubscriptionInfo, error) {
	return nil, errors.New("newapi 不支持订阅购买")
}

func (c *Client) CreateSubscription(ctx context.Context, ch *connector.Channel, session *connector.AuthSession, req connector.SubscriptionRequest) (*connector.SubscriptionLaunch, error) {
	return nil, errors.New("newapi 不支持订阅购买")
}

func (c *Client) GetSubscriptionUsage(ctx context.Context, ch *connector.Channel, session *connector.AuthSession) (*connector.SubscriptionUsageInfo, error) {
	return nil, errors.New("newapi 不支持订阅用量")
}

func (c *Client) ListAPIKeys(ctx context.Context, ch *connector.Channel, session *connector.AuthSession, query connector.APIKeyQuery) (*connector.APIKeyPage, error) {
	page, pageSize := normalizeAPIKeyPage(query.Page, query.PageSize)
	site := strings.TrimRight(ch.SiteURL, "/")
	quotaPerUnit, err := c.newAPIQuotaPerUnit(ctx, ch)
	if err != nil {
		return nil, fmt.Errorf("newapi quota unit: %w", err)
	}
	params := url.Values{}
	params.Set("p", strconv.Itoa(page))
	params.Set("page_size", strconv.Itoa(pageSize))
	path := "/api/token/"
	search := strings.TrimSpace(query.Search)
	if search != "" {
		path = "/api/token/search"
		params.Set("keyword", search)
	}
	body, err := c.getJSON(ctx, site+path+"?"+params.Encode(), session)
	if err != nil {
		return nil, fmt.Errorf("newapi api keys: %w", err)
	}
	var raw struct {
		Items    []newAPIToken `json:"items"`
		Total    int64         `json:"total"`
		Page     int           `json:"page"`
		PageSize int           `json:"page_size"`
		Pages    int           `json:"pages"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("newapi api keys decode: %w", err)
	}
	groups, _ := c.newAPIGroupMap(ctx, ch, session)
	items := make([]connector.APIKey, 0, len(raw.Items))
	for _, item := range raw.Items {
		key := item.toConnector(quotaPerUnit)
		if g, ok := groups[key.Group]; ok {
			key.GroupName = g.Name
			key.GroupDescription = g.Description
			key.GroupRatio = g.Ratio
		}
		items = append(items, key)
	}
	if raw.Page <= 0 {
		raw.Page = page
	}
	if raw.PageSize <= 0 {
		raw.PageSize = pageSize
	}
	if raw.Pages <= 0 {
		raw.Pages = pagesFromTotal(raw.Total, raw.PageSize)
	}
	return &connector.APIKeyPage{
		Items:    items,
		Total:    raw.Total,
		Page:     raw.Page,
		PageSize: raw.PageSize,
		Pages:    raw.Pages,
	}, nil
}

func (c *Client) ListAPIKeyGroups(ctx context.Context, ch *connector.Channel, session *connector.AuthSession) ([]connector.APIKeyGroup, error) {
	groups, err := c.newAPIGroupMap(ctx, ch, session)
	if err != nil {
		return nil, err
	}
	out := make([]connector.APIKeyGroup, 0, len(groups))
	for _, g := range groups {
		out = append(out, g)
	}
	return out, nil
}

func (c *Client) ListModels(ctx context.Context, ch *connector.Channel, session *connector.AuthSession) ([]connector.ModelOption, error) {
	site := strings.TrimRight(ch.SiteURL, "/")
	body, err := c.getJSON(ctx, site+"/api/user/models", session)
	if err != nil {
		body, err = c.getJSON(ctx, site+"/api/channel/models_enabled", session)
		if err != nil {
			return nil, fmt.Errorf("newapi models: %w", err)
		}
	}
	models, err := decodeModelOptions(body, "newapi")
	if err != nil {
		return nil, fmt.Errorf("newapi models decode: %w", err)
	}
	return models, nil
}

func (c *Client) CreateAPIKey(ctx context.Context, ch *connector.Channel, session *connector.AuthSession, req connector.APIKeyCreateRequest) (*connector.APIKey, error) {
	if strings.TrimSpace(req.Name) == "" {
		return nil, errors.New("密钥名称不能为空")
	}
	quotaPerUnit, err := c.newAPIQuotaPerUnit(ctx, ch)
	if err != nil {
		return nil, fmt.Errorf("newapi quota unit: %w", err)
	}
	body := buildNewAPICreateToken(req, quotaPerUnit)
	restyReq := c.http.R().
		SetContext(ctx).
		SetHeader("Content-Type", "application/json").
		SetBody(body)
	applyNewAPIAuth(restyReq, session)
	resp, err := restyReq.Post(strings.TrimRight(ch.SiteURL, "/") + "/api/token/")
	if err != nil {
		return nil, fmt.Errorf("newapi create api key http: %w", err)
	}
	if resp.IsError() {
		return nil, fmt.Errorf("newapi create api key: %w", connector.HTTPStatusError(resp.StatusCode(), resp.Body()))
	}
	data, err := decodeNewAPIWriteData(resp.Body(), "newapi create api key")
	if err != nil {
		return nil, err
	}
	if key := newAPIKeyFromCreateData(data, req, quotaPerUnit); key != nil && key.ID > 0 {
		return key, nil
	}
	if key, err := c.findAPIKeyByName(ctx, ch, session, req.Name); err == nil && key != nil {
		return key, nil
	} else if err != nil {
		return nil, fmt.Errorf("newapi create api key lookup: %w", err)
	}
	return nil, fmt.Errorf("newapi create api key returned no id and lookup by name failed: %s", strings.TrimSpace(req.Name))
}

func (c *Client) UpdateAPIKey(ctx context.Context, ch *connector.Channel, session *connector.AuthSession, id int64, req connector.APIKeyUpdateRequest) (*connector.APIKey, error) {
	if id <= 0 {
		return nil, errors.New("密钥 ID 无效")
	}
	current, err := c.getAPIKeyByID(ctx, ch, session, id)
	if err != nil {
		return nil, fmt.Errorf("newapi get api key before update: %w", err)
	}
	quotaPerUnit, err := c.newAPIQuotaPerUnit(ctx, ch)
	if err != nil {
		return nil, fmt.Errorf("newapi quota unit: %w", err)
	}
	body := buildNewAPIUpdateToken(id, current, req, quotaPerUnit)
	restyReq := c.http.R().
		SetContext(ctx).
		SetHeader("Content-Type", "application/json").
		SetBody(body)
	applyNewAPIAuth(restyReq, session)
	resp, err := restyReq.Put(strings.TrimRight(ch.SiteURL, "/") + "/api/token/")
	if err != nil {
		return nil, fmt.Errorf("newapi update api key http: %w", err)
	}
	if resp.IsError() {
		return nil, fmt.Errorf("newapi update api key: %w", connector.HTTPStatusError(resp.StatusCode(), resp.Body()))
	}
	data, err := decodeNewAPIWriteData(resp.Body(), "newapi update api key")
	if err != nil {
		return nil, err
	}
	if req.Status != nil {
		statusReq := c.http.R().
			SetContext(ctx).
			SetHeader("Content-Type", "application/json").
			SetBody(map[string]any{
				"id":     int(id),
				"status": newAPITokenStatusFromString(*req.Status),
			})
		applyNewAPIAuth(statusReq, session)
		statusResp, err := statusReq.Put(strings.TrimRight(ch.SiteURL, "/") + "/api/token/?status_only=1")
		if err != nil {
			return nil, fmt.Errorf("newapi update api key status http: %w", err)
		}
		if statusResp.IsError() {
			return nil, fmt.Errorf("newapi update api key status: %w", connector.HTTPStatusError(statusResp.StatusCode(), statusResp.Body()))
		}
		if _, err := decodeNewAPIWriteData(statusResp.Body(), "newapi update api key status"); err != nil {
			return nil, err
		}
	}
	if refreshed, err := c.getAPIKeyByID(ctx, ch, session, id); err == nil && refreshed != nil {
		return refreshed, nil
	}
	var token newAPIToken
	if len(data) > 0 && string(data) != "null" {
		_ = json.Unmarshal(data, &token)
	}
	if token.ID == 0 {
		token.ID = int(id)
		if v, ok := body["name"].(string); ok {
			token.Name = v
		}
		if v, ok := body["status"].(int); ok {
			token.Status = v
		}
	}
	out := token.toConnector(quotaPerUnit)
	return &out, nil
}

func (c *Client) getAPIKeyByID(ctx context.Context, ch *connector.Channel, session *connector.AuthSession, id int64) (*connector.APIKey, error) {
	quotaPerUnit, err := c.newAPIQuotaPerUnit(ctx, ch)
	if err != nil {
		return nil, fmt.Errorf("newapi quota unit: %w", err)
	}
	body, err := c.getJSON(ctx, strings.TrimRight(ch.SiteURL, "/")+"/api/token/"+strconv.FormatInt(id, 10), session)
	if err != nil {
		return nil, err
	}
	var token newAPIToken
	if err := json.Unmarshal(body, &token); err != nil {
		return nil, fmt.Errorf("newapi api key decode: %w", err)
	}
	if token.ID <= 0 {
		return nil, fmt.Errorf("newapi api key %d not found", id)
	}
	out := token.toConnector(quotaPerUnit)
	if groups, err := c.newAPIGroupMap(ctx, ch, session); err == nil {
		if g, ok := groups[out.Group]; ok {
			out.GroupName = g.Name
			out.GroupDescription = g.Description
			out.GroupRatio = g.Ratio
		}
	}
	return &out, nil
}

func (c *Client) DeleteAPIKey(ctx context.Context, ch *connector.Channel, session *connector.AuthSession, id int64) error {
	if id <= 0 {
		return errors.New("密钥 ID 无效")
	}
	delReq := c.http.R().SetContext(ctx)
	applyNewAPIAuth(delReq, session)
	resp, err := delReq.Delete(strings.TrimRight(ch.SiteURL, "/") + "/api/token/" + strconv.FormatInt(id, 10))
	if err != nil {
		return fmt.Errorf("newapi delete api key http: %w", err)
	}
	if resp.IsError() {
		return fmt.Errorf("newapi delete api key: %w", connector.HTTPStatusError(resp.StatusCode(), resp.Body()))
	}
	return decodeNewAPIWrite(resp.Body(), "newapi delete api key")
}

func (c *Client) RevealAPIKey(ctx context.Context, ch *connector.Channel, session *connector.AuthSession, id int64) (string, error) {
	if id <= 0 {
		return "", errors.New("密钥 ID 无效")
	}
	revealReq := c.http.R().SetContext(ctx)
	applyNewAPIAuth(revealReq, session)
	resp, err := revealReq.Post(strings.TrimRight(ch.SiteURL, "/") + "/api/token/" + strconv.FormatInt(id, 10) + "/key")
	if err != nil {
		return "", fmt.Errorf("newapi reveal api key http: %w", err)
	}
	if resp.IsError() {
		return "", fmt.Errorf("newapi reveal api key: %w", connector.HTTPStatusError(resp.StatusCode(), resp.Body()))
	}
	data, err := decodeNewAPIWriteData(resp.Body(), "newapi reveal api key")
	if err != nil {
		return "", err
	}
	var raw struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return "", fmt.Errorf("newapi reveal api key data: %w", err)
	}
	if strings.TrimSpace(raw.Key) == "" {
		return "", errors.New("newapi 未返回完整密钥")
	}
	return raw.Key, nil
}

func newAPIKeyFromCreateData(data json.RawMessage, req connector.APIKeyCreateRequest, quotaPerUnit float64) *connector.APIKey {
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	var token newAPIToken
	if err := json.Unmarshal(data, &token); err == nil && token.ID > 0 {
		out := token.toConnector(quotaPerUnit)
		return &out
	}
	var wrapped struct {
		Token newAPIToken `json:"token"`
	}
	if err := json.Unmarshal(data, &wrapped); err == nil && wrapped.Token.ID > 0 {
		out := wrapped.Token.toConnector(quotaPerUnit)
		return &out
	}
	return nil
}

func (c *Client) findAPIKeyByName(ctx context.Context, ch *connector.Channel, session *connector.AuthSession, name string) (*connector.APIKey, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, nil
	}
	key, searchErr := c.findAPIKeyByNameQuery(ctx, ch, session, name, true)
	if key != nil {
		return key, nil
	}
	key, listErr := c.findAPIKeyByNameQuery(ctx, ch, session, name, false)
	if key != nil {
		return key, nil
	}
	if searchErr != nil {
		return nil, searchErr
	}
	return nil, listErr
}

func (c *Client) findAPIKeyByNameQuery(ctx context.Context, ch *connector.Channel, session *connector.AuthSession, name string, useSearch bool) (*connector.APIKey, error) {
	var contains *connector.APIKey
	for pageNo := 1; pageNo <= 10; pageNo++ {
		query := connector.APIKeyQuery{Page: pageNo, PageSize: 100}
		if useSearch {
			query.Search = name
		}
		page, err := c.ListAPIKeys(ctx, ch, session, query)
		if err != nil {
			return nil, err
		}
		for i := range page.Items {
			item := &page.Items[i]
			if strings.EqualFold(strings.TrimSpace(item.Name), name) {
				return item, nil
			}
			if contains == nil && strings.Contains(strings.ToLower(item.Name), strings.ToLower(name)) {
				contains = item
			}
		}
		if page.Pages <= pageNo || len(page.Items) == 0 {
			break
		}
	}
	return contains, nil
}

type newAPIPayMethod struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	MinTopup string `json:"min_topup"`
}

func parseNewAPIPayMethods(raw json.RawMessage) ([]newAPIPayMethod, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var methods []newAPIPayMethod
	if err := json.Unmarshal(raw, &methods); err == nil {
		return methods, nil
	}
	var encoded string
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, err
	}
	if strings.TrimSpace(encoded) == "" {
		return nil, nil
	}
	if err := json.Unmarshal([]byte(encoded), &methods); err != nil {
		return nil, err
	}
	return methods, nil
}

func parseNewAPIAmountOptions(raw json.RawMessage) ([]float64, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var options []float64
	if err := json.Unmarshal(raw, &options); err == nil {
		return options, nil
	}
	var encoded string
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, err
	}
	if strings.TrimSpace(encoded) == "" {
		return nil, nil
	}
	if err := json.Unmarshal([]byte(encoded), &options); err != nil {
		return nil, err
	}
	return options, nil
}

func newAPIStringFromRaw(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(string(raw))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func rawObjectToStringMap(raw json.RawMessage) (map[string]string, error) {
	fields := map[string]string{}
	if len(raw) == 0 || string(raw) == "null" {
		return fields, nil
	}
	var rawFields map[string]any
	if err := json.Unmarshal(raw, &rawFields); err != nil {
		return nil, err
	}
	for k, v := range rawFields {
		fields[k] = fmt.Sprint(v)
	}
	return fields, nil
}

func stringFieldFromRaw(raw json.RawMessage, field string) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return ""
	}
	value, ok := data[field]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func decodeNewAPIPaymentResponse(body []byte, prefix string) (json.RawMessage, string, string, error) {
	var wrapped struct {
		Success bool            `json:"success"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
		URL     string          `json:"url"`
	}
	if err := json.Unmarshal(body, &wrapped); err != nil {
		return nil, "", "", fmt.Errorf("%s decode: %w", prefix, err)
	}
	if !wrapped.Success && !strings.EqualFold(strings.TrimSpace(wrapped.Message), "success") {
		msg := strings.TrimSpace(wrapped.Message)
		if msg == "" {
			msg = newAPIStringFromRaw(wrapped.Data)
		}
		if msg == "" || strings.EqualFold(msg, "null") {
			msg = prefix + " failed"
		}
		return nil, "", "", errors.New(msg)
	}
	return wrapped.Data, strings.TrimSpace(wrapped.URL), strings.TrimSpace(wrapped.Message), nil
}

func (c *Client) getJSON(ctx context.Context, url string, session *connector.AuthSession) ([]byte, error) {
	body, err := c.getRaw(ctx, url, session)
	if err != nil {
		return nil, err
	}
	var wrapped newapiResp
	if err := json.Unmarshal(body, &wrapped); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	if !wrapped.Success {
		return nil, errors.New(wrapped.Message)
	}
	return wrapped.Data, nil
}

func (c *Client) postJSON(ctx context.Context, url string, session *connector.AuthSession, body any) ([]byte, error) {
	req := c.http.R().
		SetContext(ctx).
		SetHeader("Content-Type", "application/json").
		SetBody(body)
	applyNewAPIAuth(req, session)
	resp, err := req.Post(url)
	if err != nil {
		return nil, err
	}
	if resp.IsError() {
		return nil, connector.HTTPStatusError(resp.StatusCode(), resp.Body())
	}
	return resp.Body(), nil
}

func (c *Client) getRaw(ctx context.Context, url string, session *connector.AuthSession) ([]byte, error) {
	req := c.http.R().SetContext(ctx)
	applyNewAPIAuth(req, session)
	// NewAPI 即便用 session 鉴权也要求带 New-Api-User 头（"unauthorized, New-Api-User header not provided"）。
	resp, err := req.Get(url)
	if err != nil {
		return nil, err
	}
	if resp.IsError() {
		return nil, connector.HTTPStatusError(resp.StatusCode(), resp.Body())
	}
	return resp.Body(), nil
}

func (c *Client) quotaToUSD(quota float64, quotaPerUnit float64) float64 {
	return round4(quota / quotaPerUnit)
}

func (c *Client) newAPIQuotaPerUnit(ctx context.Context, ch *connector.Channel) (float64, error) {
	site := strings.TrimRight(ch.SiteURL, "/")
	body, err := c.getJSON(ctx, site+"/api/status", nil)
	if err != nil {
		return 500000, nil
	}
	var status struct {
		QuotaPerUnit float64 `json:"quota_per_unit"`
	}
	if err := json.Unmarshal(body, &status); err != nil {
		return 500000, nil
	}
	if status.QuotaPerUnit <= 0 {
		status.QuotaPerUnit = 500000
	}
	return status.QuotaPerUnit, nil
}

func quotaUnitsToAmount(quota int, quotaPerUnit float64) float64 {
	if quotaPerUnit <= 0 {
		quotaPerUnit = 500000
	}
	return round6(float64(quota) / quotaPerUnit)
}

func amountToQuotaUnits(amount float64, quotaPerUnit float64) int {
	if !isFinite(amount) || amount <= 0 {
		return 0
	}
	if quotaPerUnit <= 0 {
		quotaPerUnit = 500000
	}
	return int(math.Round(amount * quotaPerUnit))
}

func round6(v float64) float64 {
	return math.Round(v*1000000) / 1000000
}

func isFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

func newAPIRechargeMultiplier(ch *connector.Channel, price float64) *float64 {
	if ch.RechargeMultiplier != nil && *ch.RechargeMultiplier > 0 {
		return ch.RechargeMultiplier
	}
	if price <= 0 {
		return nil
	}
	multiplier := 1 / price
	return &multiplier
}

func round4(v float64) float64 {
	return math.Round(v*10000) / 10000
}

func joinCookies(cookies []*http.Cookie) string {
	if len(cookies) == 0 {
		return ""
	}
	parts := make([]string, 0, len(cookies))
	for _, c := range cookies {
		parts = append(parts, c.Name+"="+c.Value)
	}
	return strings.Join(parts, "; ")
}

func parseAnnouncementTime(values ...string) *time.Time {
	for _, raw := range values {
		raw = strings.TrimSpace(raw)
		if raw == "" || raw == "0" {
			continue
		}
		if t, ok := parseFlexibleTime(raw); ok {
			return &t
		}
	}
	return nil
}

func parseFlexibleTime(raw string) (time.Time, bool) {
	if raw == "" {
		return time.Time{}, false
	}
	layouts := []string{
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t, true
		}
	}
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
		switch {
		case n > 1e12:
			return time.UnixMilli(n), true
		case n > 1e9:
			return time.Unix(n, 0), true
		}
	}
	return time.Time{}, false
}

func newAPIAnnouncementSourceKey(id any, parts ...string) string {
	if id != nil {
		switch v := id.(type) {
		case string:
			if s := strings.TrimSpace(v); s != "" {
				return "id:" + s
			}
		case float64:
			if v != 0 {
				return "id:" + strconv.FormatInt(int64(v), 10)
			}
		default:
			s := strings.TrimSpace(fmt.Sprint(v))
			if s != "" && s != "<nil>" && s != "0" {
				return "id:" + s
			}
		}
	}
	return hashAnnouncementKey(parts...)
}

func hashAnnouncementKey(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(strings.TrimSpace(p)))
		h.Write([]byte{0})
	}
	return "hash:" + hex.EncodeToString(h.Sum(nil))
}

func dedupeAnnouncements(items []connector.AnnouncementResult) []connector.AnnouncementResult {
	if len(items) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(items))
	out := make([]connector.AnnouncementResult, 0, len(items))
	for _, item := range items {
		key := strings.TrimSpace(item.SourceKey)
		if key == "" {
			key = hashAnnouncementKey(item.Title, item.Content, item.Type, item.Link)
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		item.SourceKey = key
		out = append(out, item)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].PublishedAt == nil || out[j].PublishedAt == nil {
			return i < j
		}
		return out[i].PublishedAt.After(*out[j].PublishedAt)
	})
	return out
}

func (c *Client) newAPIGroupMap(ctx context.Context, ch *connector.Channel, session *connector.AuthSession) (map[string]connector.APIKeyGroup, error) {
	body, err := c.getJSON(ctx, strings.TrimRight(ch.SiteURL, "/")+"/api/user/self/groups", session)
	if err != nil {
		return nil, fmt.Errorf("newapi api key groups: %w", err)
	}
	raw := map[string]struct {
		Ratio json.RawMessage `json:"ratio"`
		Desc  string          `json:"desc"`
	}{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("newapi api key groups decode: %w", err)
	}
	out := make(map[string]connector.APIKeyGroup, len(raw))
	for name, v := range raw {
		var ratio float64
		if err := json.Unmarshal(v.Ratio, &ratio); err != nil {
			continue
		}
		out[name] = connector.APIKeyGroup{
			Name:        name,
			Description: v.Desc,
			Ratio:       ratio,
		}
	}
	return out, nil
}

type newAPIToken struct {
	ID                 int     `json:"id"`
	Key                string  `json:"key"`
	Status             int     `json:"status"`
	Name               string  `json:"name"`
	CreatedTime        int64   `json:"created_time"`
	AccessedTime       int64   `json:"accessed_time"`
	ExpiredTime        int64   `json:"expired_time"`
	RemainQuota        int     `json:"remain_quota"`
	UsedQuota          int     `json:"used_quota"`
	RemainAmount       float64 `json:"remain_amount"`
	UsedAmount         float64 `json:"used_amount"`
	UnlimitedQuota     bool    `json:"unlimited_quota"`
	ModelLimitsEnabled bool    `json:"model_limits_enabled"`
	ModelLimits        string  `json:"model_limits"`
	AllowIPs           *string `json:"allow_ips"`
	Group              string  `json:"group"`
	CrossGroupRetry    bool    `json:"cross_group_retry"`
}

func (t newAPIToken) toConnector(quotaPerUnit float64) connector.APIKey {
	var createdAt *time.Time
	if t.CreatedTime > 0 {
		v := time.Unix(t.CreatedTime, 0)
		createdAt = &v
	}
	var lastUsedAt *time.Time
	if t.AccessedTime > 0 {
		v := time.Unix(t.AccessedTime, 0)
		lastUsedAt = &v
	}
	allowIPs := ""
	if t.AllowIPs != nil {
		allowIPs = *t.AllowIPs
	}
	remainAmount := t.RemainAmount
	if remainAmount <= 0 && t.RemainQuota > 0 {
		remainAmount = quotaUnitsToAmount(t.RemainQuota, quotaPerUnit)
	}
	usedAmount := t.UsedAmount
	if usedAmount <= 0 && t.UsedQuota > 0 {
		usedAmount = quotaUnitsToAmount(t.UsedQuota, quotaPerUnit)
	}
	return connector.APIKey{
		ID:                 int64(t.ID),
		Key:                t.Key,
		Name:               t.Name,
		Status:             newAPITokenStatusToString(t.Status),
		Group:              t.Group,
		RemainAmount:       remainAmount,
		UsedAmount:         usedAmount,
		Quota:              remainAmount,
		QuotaUsed:          usedAmount,
		UnlimitedQuota:     t.UnlimitedQuota,
		ExpiredTime:        t.ExpiredTime,
		CreatedAt:          createdAt,
		LastUsedAt:         lastUsedAt,
		AllowIPs:           allowIPs,
		ModelLimitsEnabled: t.ModelLimitsEnabled,
		ModelLimits:        t.ModelLimits,
		CrossGroupRetry:    t.CrossGroupRetry,
	}
}

func buildNewAPICreateToken(req connector.APIKeyCreateRequest, quotaPerUnit float64) map[string]any {
	remainAmount := apiKeyCreateRemainAmount(req)
	remainQuota := valueOr(req.RemainQuota, 0)
	if req.RemainQuota == nil {
		remainQuota = amountToQuotaUnits(remainAmount, quotaPerUnit)
	}
	body := map[string]any{
		"name":                 strings.TrimSpace(req.Name),
		"expired_time":         valueOr(req.ExpiredTime, int64(-1)),
		"remain_amount":        remainAmount,
		"remain_quota":         remainQuota,
		"unlimited_quota":      valueOr(req.UnlimitedQuota, false),
		"model_limits_enabled": valueOr(req.ModelLimitsEnabled, false),
		"model_limits":         req.ModelLimits,
		"allow_ips":            req.AllowIPs,
		"group":                req.Group,
		"cross_group_retry":    valueOr(req.CrossGroupRetry, false),
	}
	if strings.TrimSpace(req.CustomKey) != "" {
		body["custom_key"] = strings.TrimSpace(req.CustomKey)
	}
	return body
}

func buildNewAPIUpdateToken(id int64, current *connector.APIKey, req connector.APIKeyUpdateRequest, quotaPerUnit float64) map[string]any {
	name := ""
	status := "active"
	group := ""
	remainAmount := 0.0
	remainQuota := 0
	unlimitedQuota := false
	expiredTime := int64(-1)
	modelLimitsEnabled := false
	modelLimits := ""
	allowIPs := ""
	crossGroupRetry := false
	if current != nil {
		name = current.Name
		status = current.Status
		group = current.Group
		remainAmount = current.Quota
		remainQuota = amountToQuotaUnits(remainAmount, quotaPerUnit)
		unlimitedQuota = current.UnlimitedQuota
		expiredTime = current.ExpiredTime
		modelLimitsEnabled = current.ModelLimitsEnabled
		modelLimits = current.ModelLimits
		allowIPs = current.AllowIPs
		crossGroupRetry = current.CrossGroupRetry
	}
	if req.Name != nil {
		name = strings.TrimSpace(*req.Name)
	}
	if req.Status != nil {
		status = *req.Status
	}
	if req.Group != nil {
		group = strings.TrimSpace(*req.Group)
	}
	if req.RemainQuota != nil {
		remainQuota = *req.RemainQuota
		remainAmount = quotaUnitsToAmount(*req.RemainQuota, quotaPerUnit)
	} else if amount, ok := apiKeyUpdateRemainAmount(req); ok {
		remainAmount = amount
		remainQuota = amountToQuotaUnits(amount, quotaPerUnit)
	}
	if req.UnlimitedQuota != nil {
		unlimitedQuota = *req.UnlimitedQuota
	}
	if req.ExpiredTime != nil {
		expiredTime = *req.ExpiredTime
	}
	if req.ModelLimitsEnabled != nil {
		modelLimitsEnabled = *req.ModelLimitsEnabled
	}
	if req.ModelLimits != nil {
		modelLimits = *req.ModelLimits
	}
	if req.AllowIPs != nil {
		allowIPs = *req.AllowIPs
	}
	if req.CrossGroupRetry != nil {
		crossGroupRetry = *req.CrossGroupRetry
	}
	body := map[string]any{
		"id":                   int(id),
		"name":                 strings.TrimSpace(name),
		"status":               newAPITokenStatusFromString(status),
		"expired_time":         expiredTime,
		"remain_amount":        remainAmount,
		"remain_quota":         remainQuota,
		"unlimited_quota":      unlimitedQuota,
		"model_limits_enabled": modelLimitsEnabled,
		"model_limits":         modelLimits,
		"allow_ips":            allowIPs,
		"group":                group,
		"cross_group_retry":    crossGroupRetry,
	}
	return body
}

func apiKeyCreateRemainAmount(req connector.APIKeyCreateRequest) float64 {
	switch {
	case req.RemainAmount != nil:
		return *req.RemainAmount
	case req.Quota != nil:
		return *req.Quota
	default:
		return 0
	}
}

func apiKeyUpdateRemainAmount(req connector.APIKeyUpdateRequest) (float64, bool) {
	switch {
	case req.RemainAmount != nil:
		return *req.RemainAmount, true
	case req.Quota != nil:
		return *req.Quota, true
	default:
		return 0, false
	}
}

func decodeNewAPIWrite(body []byte, prefix string) error {
	_, err := decodeNewAPIWriteData(body, prefix)
	return err
}

func decodeNewAPIWriteData(body []byte, prefix string) (json.RawMessage, error) {
	var wrapped newapiResp
	if err := json.Unmarshal(body, &wrapped); err != nil {
		return nil, fmt.Errorf("%s decode: %w", prefix, err)
	}
	if !wrapped.Success {
		msg := strings.TrimSpace(wrapped.Message)
		if msg == "" {
			msg = newAPIStringFromRaw(wrapped.Data)
		}
		if msg == "" {
			msg = prefix + " failed"
		}
		return nil, errors.New(msg)
	}
	return wrapped.Data, nil
}

func newAPITokenStatusToString(status int) string {
	switch status {
	case 1:
		return "active"
	case 2:
		return "disabled"
	case 3:
		return "expired"
	case 4:
		return "quota_exhausted"
	default:
		return "unknown"
	}
}

func newAPITokenStatusFromString(status string) int {
	switch strings.TrimSpace(status) {
	case "active":
		return 1
	case "disabled":
		return 2
	case "expired":
		return 3
	case "quota_exhausted":
		return 4
	default:
		return 0
	}
}

func normalizeAPIKeyPage(page, pageSize int) (int, int) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	return page, pageSize
}

func pagesFromTotal(total int64, pageSize int) int {
	if pageSize <= 0 {
		pageSize = 20
	}
	pages := int(math.Ceil(float64(total) / float64(pageSize)))
	if pages < 1 {
		return 1
	}
	return pages
}

func valueOr[T any](ptr *T, fallback T) T {
	if ptr == nil {
		return fallback
	}
	return *ptr
}

func decodeModelOptions(body []byte, source string) ([]connector.ModelOption, error) {
	var raw any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	return modelOptionsFromAny(raw, source), nil
}

func modelOptionsFromAny(raw any, source string) []connector.ModelOption {
	seen := map[string]connector.ModelOption{}
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case []any:
			for _, item := range x {
				walk(item)
			}
		case map[string]any:
			if data, ok := x["data"]; ok {
				walk(data)
				return
			}
			if models, ok := x["models"]; ok {
				walk(models)
				return
			}
			id := strings.TrimSpace(fmt.Sprint(firstNonNil(x["id"], x["Id"], x["name"], x["Name"])))
			if id == "" || id == "<nil>" {
				return
			}
			opt := connector.ModelOption{
				ID:      id,
				Name:    strings.TrimSpace(fmt.Sprint(firstNonNil(x["display_name"], x["DisplayName"], x["name"], x["Name"]))),
				OwnedBy: strings.TrimSpace(fmt.Sprint(firstNonNil(x["owned_by"], x["OwnedBy"]))),
				Source:  source,
			}
			if opt.Name == "<nil>" {
				opt.Name = ""
			}
			if opt.OwnedBy == "<nil>" {
				opt.OwnedBy = ""
			}
			seen[id] = opt
		case map[string][]string:
			for _, list := range x {
				for _, id := range list {
					id = strings.TrimSpace(id)
					if id != "" {
						seen[id] = connector.ModelOption{ID: id, Source: source}
					}
				}
			}
		case string:
			id := strings.TrimSpace(x)
			if id != "" {
				seen[id] = connector.ModelOption{ID: id, Source: source}
			}
		}
	}
	walk(raw)
	out := make([]connector.ModelOption, 0, len(seen))
	for _, opt := range seen {
		out = append(out, opt)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].ID) < strings.ToLower(out[j].ID)
	})
	return out
}

func firstNonNil(values ...any) any {
	for _, v := range values {
		if v != nil {
			return v
		}
	}
	return ""
}
