// Package api 注册所有 HTTP 路由，组装各业务 handler。
//
// 单用户场景下走 HMAC token 鉴权：账号密码写在 config 里，登录后下发 token。
package api

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/ifty-r/upstream-ops/backend/autogroup"
	"github.com/ifty-r/upstream-ops/backend/channel"
	"github.com/ifty-r/upstream-ops/backend/connector"
	"github.com/ifty-r/upstream-ops/backend/crypto"
	"github.com/ifty-r/upstream-ops/backend/notify"
	"github.com/ifty-r/upstream-ops/backend/runtimeconfig"
	"github.com/ifty-r/upstream-ops/backend/shopmonitor"
	"github.com/ifty-r/upstream-ops/backend/storage"
	"github.com/ifty-r/upstream-ops/backend/upstreamcap"
	"gorm.io/gorm"
)

type monitorService interface {
	RefreshBalance(ctx context.Context, c *storage.Channel) error
	RefreshRates(ctx context.Context, c *storage.Channel) error
	CheckSubscriptionUsageAlerts(ctx context.Context, c *storage.Channel) error
}

type channelService interface {
	Create(in channel.CreateInput) (*storage.Channel, error)
	Update(id uint, in channel.UpdateInput) (*storage.Channel, error)
	Delete(id uint) error
	ClearLoginInfo(id uint) (*storage.Channel, error)
	TestLogin(ctx context.Context, channelID uint) error
	RedeemCode(ctx context.Context, channelID uint, code string) (*connector.RedeemResult, error)
}

type upstreamCapabilityService interface {
	Matrix(ctx context.Context, channelID uint) (*upstreamcap.CapabilityMatrix, error)
}

type upstreamRechargeService interface {
	upstreamcap.RechargeCapability
}

type upstreamSubscriptionService interface {
	upstreamcap.SubscriptionCapability
}

type shopSyncJobRunner interface {
	Start(targetID uint) (*storage.ShopSyncJob, bool, error)
	Get(targetID, jobID uint) (*storage.ShopSyncJob, error)
	Latest(targetID uint) (*storage.ShopSyncJob, error)
}

type upstreamAPIKeyService interface {
	upstreamcap.APIKeyCapability
	upstreamcap.GroupCapability
}

// Deps 把所有 handler 需要的依赖打包传入。
type Deps struct {
	DB             *gorm.DB
	Cipher         *crypto.Cipher
	Runtime        *runtimeconfig.Manager
	Channels       *storage.Channels
	Sessions       *storage.AuthSessions
	Captchas       *storage.Captchas
	Notifies       *storage.Notifications
	ShopTargets    *storage.ShopTargets
	ShopWatchRules *storage.ShopWatchRules
	ShopGoods      *storage.ShopGoods
	ShopSyncRunner shopSyncJobRunner
	AutoGroups     *storage.AutoGroups
	Announcements  *storage.UpstreamAnnouncements
	Rates          *storage.Rates
	MonLogs        *storage.MonitorLogs
	ChannelSvc     channelService
	UpstreamCap    upstreamCapabilityService
	UpstreamOps    any
	Monitor        monitorService
	Dispatcher     *notify.Dispatcher
	ShopMonitor    *shopmonitor.Service
	AutoGroup      *autogroup.Service
	Log            *slog.Logger

	// Frontend 可选：传入嵌入的前端 dist 文件系统。nil 表示不挂载（本地开发用 vite dev server）。
	Frontend fs.FS
}

// Register 把所有路由挂到给定 gin engine。
func Register(r *gin.Engine, d *Deps) {
	r.GET("/healthz", func(c *gin.Context) {
		sqlDB, err := d.DB.DB()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"status": "down", "err": err.Error()})
			return
		}
		if err := sqlDB.Ping(); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "db_down", "err": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	api := r.Group("/api")
	if d.Runtime != nil {
		api.Use(d.Runtime.AuthMiddleware())
	}
	{
		registerVersion(api, d)
		registerAuth(api, d)
		registerChannels(api, d)
		registerCaptchas(api, d)
		registerNotifications(api, d)
		registerShopTargets(api, d)
		registerAutoGroups(api, d)
		registerAnnouncements(api, d)
		registerRates(api, d)
		registerMonitorLogs(api, d)
		registerDashboard(api, d)
		registerSettings(api, d)
	}

	if d.Frontend != nil {
		registerFrontend(r, d.Frontend)
	}
}

// registerFrontend 把嵌入的前端 dist 挂在根路径，并处理 SPA fallback：
//
//   - GET /assets/*  → 直接返回文件
//   - GET /          → 返回 index.html
//   - GET /channels  → 返回 index.html（React Router 客户端路由）
//
// /api/*、/healthz 都已被前面的具体路由占了，不会走到这里。
// 安全起见仍然做一次前缀拦截，避免任何意外情况下"未鉴权读 index.html"压到 /api 上。
func registerFrontend(r *gin.Engine, dist fs.FS) {
	fileServer := http.FileServer(http.FS(dist))

	r.NoRoute(func(c *gin.Context) {
		path := c.Request.URL.Path

		// 永远不让 SPA fallback 覆盖 API / 健康检查路径。
		if strings.HasPrefix(path, "/api/") || path == "/healthz" {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}

		// 文件存在就直接 serve，否则回落到 index.html。
		clean := strings.TrimPrefix(path, "/")
		if clean == "" {
			clean = "index.html"
		}
		if _, err := fs.Stat(dist, clean); err != nil {
			c.Request.URL.Path = "/"
		}
		fileServer.ServeHTTP(c.Writer, c.Request)
	})
}

// fail 统一错误响应。
func fail(c *gin.Context, status int, err error) {
	c.JSON(status, gin.H{"error": err.Error()})
}

// 店铺接口依赖 LDXP 等外部服务。依赖失败不代表 Ops 网关故障，使用 424
// 避免边缘代理把应用层的上游错误包装成 Cloudflare 502。
func failShopUpstream(c *gin.Context, err error) {
	fail(c, http.StatusFailedDependency, fmt.Errorf("店铺上游不可用：%w", err))
}
