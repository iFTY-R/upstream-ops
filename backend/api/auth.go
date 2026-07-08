package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ifty-r/upstream-ops/backend/config"
)

func registerAuth(g *gin.RouterGroup, d *Deps) {
	g.POST("/auth/login", func(c *gin.Context) { login(c, d) })
	g.POST("/auth/sub2api/exchange", func(c *gin.Context) { sub2apiExchange(c, d) })
	g.GET("/auth/me", func(c *gin.Context) { whoami(c, d) })
	g.POST("/auth/logout", func(c *gin.Context) {
		// 无状态 token，客户端丢弃即可；这个接口仅作语义存在。
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
}

type loginInput struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type sub2apiExchangeInput struct {
	Token   string `json:"token" binding:"required"`
	UserID  string `json:"user_id"`
	SrcHost string `json:"src_host"`
}

type sub2apiCurrentUser struct {
	ID    any    `json:"id"`
	Role  string `json:"role"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

type sub2apiStatusError struct {
	StatusCode int
}

func (e sub2apiStatusError) Error() string {
	return fmt.Sprintf("sub2api token rejected: status %d", e.StatusCode)
}

func login(c *gin.Context, d *Deps) {
	// 鉴权关闭：任何登录请求都直接成功；前端在 /auth/me 已经知道无需登录。
	authSvc := d.Runtime.CurrentAuth()
	if authSvc == nil {
		c.JSON(http.StatusOK, gin.H{
			"data": gin.H{
				"auth_disabled": true,
				"username":      "anonymous",
			},
		})
		return
	}
	var in loginInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	token, exp, err := authSvc.Login(in.Username, in.Password)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"token":      token,
			"expires_at": exp.Unix(),
			"username":   authSvc.Username(),
		},
	})
}

func sub2apiExchange(c *gin.Context, d *Deps) {
	if d == nil || d.Runtime == nil {
		fail(c, http.StatusServiceUnavailable, errors.New("runtime is unavailable"))
		return
	}
	authSvc := d.Runtime.CurrentAuth()
	if authSvc == nil {
		c.JSON(http.StatusOK, gin.H{
			"data": gin.H{
				"auth_disabled": true,
				"username":      "anonymous",
			},
		})
		return
	}

	var in sub2apiExchangeInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}

	cfg, err := config.Load(d.Runtime.ConfigPath())
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	embed := cfg.Auth.Sub2APIEmbed
	if !embed.Enabled {
		fail(c, http.StatusForbidden, errors.New("sub2api embedded login is disabled"))
		return
	}
	if strings.TrimSpace(embed.BaseURL) == "" {
		fail(c, http.StatusBadRequest, errors.New("sub2api baseURL is required"))
		return
	}
	if err := checkSub2APISource(embed.AllowedOrigins, in.SrcHost); err != nil {
		fail(c, http.StatusForbidden, err)
		return
	}

	user, err := verifySub2APIToken(c.Request.Context(), embed.BaseURL, in.Token)
	if err != nil {
		fail(c, http.StatusUnauthorized, err)
		return
	}
	if embed.RequireAdmin && user.Role != "admin" {
		fail(c, http.StatusForbidden, errors.New("sub2api admin role is required"))
		return
	}

	token, exp, err := authSvc.Issue()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"token":      token,
			"expires_at": exp.Unix(),
			"username":   authSvc.Username(),
			"source":     "sub2api",
		},
	})
}

// whoami 既是"前端启动探测"接口也是"已登录信息"接口。
//
//   - 鉴权关闭 → 返回 {auth_disabled: true}，前端据此跳过登录页
//   - 鉴权开启但未带 token → 中间件已经在前面 401 拦截，根本走不到这里
//   - 鉴权开启 + 有效 token → 返回 username
func whoami(c *gin.Context, d *Deps) {
	if d.Runtime.CurrentAuth() == nil {
		c.JSON(http.StatusOK, gin.H{
			"data": gin.H{
				"auth_disabled": true,
				"username":      "anonymous",
			},
		})
		return
	}
	sub, _ := c.Get("authSubject")
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"username": sub}})
}

func checkSub2APISource(allowed []string, srcHost string) error {
	if len(allowed) == 0 {
		return nil
	}
	normalizedSrc := normalizeOrigin(srcHost)
	if normalizedSrc == "" {
		return errors.New("src_host is required")
	}
	for _, item := range allowed {
		if normalizeOrigin(item) == normalizedSrc {
			return nil
		}
	}
	return errors.New("src_host is not allowed")
}

func normalizeOrigin(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return strings.TrimRight(raw, "/")
	}
	return strings.ToLower(u.Scheme + "://" + u.Host)
}

func verifySub2APIToken(ctx context.Context, baseURL, token string) (*sub2apiCurrentUser, error) {
	var lastErr error
	for index, meURL := range sub2APIMeURLs(baseURL) {
		user, err := requestSub2APICurrentUser(ctx, meURL, token)
		if err == nil {
			return user, nil
		}
		lastErr = err
		var statusErr sub2apiStatusError
		if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusNotFound || index > 0 {
			return nil, err
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("sub2api me url is empty")
}

func sub2APIMeURLs(baseURL string) []string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil
	}
	parsed, err := url.Parse(baseURL)
	if err == nil {
		path := strings.TrimRight(parsed.Path, "/")
		if strings.HasSuffix(path, "/api/v1") || strings.HasSuffix(path, "/api") {
			if meURL, joinErr := url.JoinPath(baseURL, "auth/me"); joinErr == nil {
				return []string{meURL}
			}
			return nil
		}
	}

	// Sub2API 的前端默认 baseURL 是 /api/v1；保留 /api 兜底兼容早期或反代自定义部署。
	candidates := make([]string, 0, 2)
	for _, path := range []string{"api/v1/auth/me", "api/auth/me"} {
		if meURL, joinErr := url.JoinPath(baseURL, path); joinErr == nil {
			candidates = append(candidates, meURL)
		}
	}
	return candidates
}

func requestSub2APICurrentUser(ctx context.Context, meURL, token string) (*sub2apiCurrentUser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, meURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build sub2api me request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 10 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("verify sub2api token: %w", err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read sub2api me response: %w", err)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, sub2apiStatusError{StatusCode: res.StatusCode}
	}

	user, err := decodeSub2APIUser(body)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(user.Role) == "" {
		return nil, errors.New("sub2api user role is missing")
	}
	return user, nil
}

func decodeSub2APIUser(body []byte) (*sub2apiCurrentUser, error) {
	var direct sub2apiCurrentUser
	if err := json.Unmarshal(body, &direct); err != nil {
		return nil, fmt.Errorf("decode sub2api me response: %w", err)
	}
	if direct.Role != "" || direct.ID != nil || direct.Email != "" || direct.Name != "" {
		return &direct, nil
	}

	var wrapped struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &wrapped); err != nil {
		return nil, fmt.Errorf("decode sub2api me envelope: %w", err)
	}
	if len(wrapped.Data) == 0 || string(wrapped.Data) == "null" {
		return nil, errors.New("sub2api me response data is empty")
	}
	var user sub2apiCurrentUser
	if err := json.Unmarshal(wrapped.Data, &user); err != nil {
		return nil, fmt.Errorf("decode sub2api me data: %w", err)
	}
	return &user, nil
}
