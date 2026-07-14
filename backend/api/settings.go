package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/ifty-r/upstream-ops/backend/config"
)

const redactedSecret = "********"

type settingsConfigView struct {
	App           config.AppConfig           `json:"app"`
	Auth          settingsAuthView           `json:"auth"`
	Scheduler     config.SchedulerConfig     `json:"scheduler"`
	Notifications config.NotificationsConfig `json:"notifications"`
	Proxy         settingsProxyView          `json:"proxy"`
	Upstream      config.UpstreamConfig      `json:"upstream"`
}

type settingsAuthView struct {
	Enabled         bool                      `json:"enabled"`
	Username        string                    `json:"username"`
	Password        string                    `json:"password"`
	TokenSecret     string                    `json:"tokenSecret"`
	SessionTTLHours int                       `json:"sessionTTLHours"`
	Sub2APIEmbed    config.Sub2APIEmbedConfig `json:"sub2apiEmbed"`
}

type settingsProxyView struct {
	Enabled             bool   `json:"enabled"`
	VersionCheckEnabled bool   `json:"versionCheckEnabled"`
	Protocol            string `json:"protocol"`
	Host                string `json:"host"`
	Port                int    `json:"port"`
	Username            string `json:"username"`
	Password            string `json:"password"`
}

type settingsConfigInput struct {
	App           config.AppConfig           `json:"app" binding:"required"`
	Auth          config.AuthConfig          `json:"auth" binding:"required"`
	Scheduler     config.SchedulerConfig     `json:"scheduler" binding:"required"`
	Notifications config.NotificationsConfig `json:"notifications" binding:"required"`
	Proxy         config.ProxyConfig         `json:"proxy"`
	Upstream      config.UpstreamConfig      `json:"upstream"`
}

func registerSettings(g *gin.RouterGroup, d *Deps) {
	gs := g.Group("/settings")
	gs.GET("/config", func(c *gin.Context) { getSettingsConfig(c, d) })
	gs.PUT("/config", func(c *gin.Context) { saveSettingsConfig(c, d) })
	gs.POST("/apply", func(c *gin.Context) { applySettingsConfig(c, d) })
	gs.POST("/proxy/test", func(c *gin.Context) { testProxy(c) })
}

func getSettingsConfig(c *gin.Context, d *Deps) {
	cfg, err := config.LoadRuntimeFile(d.Runtime.ConfigPath())
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"config_path": d.Runtime.ConfigPath(),
			"config": settingsConfigView{
				App:           cfg.App,
				Auth:          settingsAuthViewFromConfig(cfg.Auth),
				Scheduler:     cfg.Scheduler,
				Notifications: cfg.Notifications,
				Proxy:         settingsProxyViewFromConfig(cfg.Proxy),
				Upstream:      cfg.Upstream,
			},
		},
	})
}

func saveSettingsConfig(c *gin.Context, d *Deps) {
	var in settingsConfigInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}

	path := d.Runtime.ConfigPath()
	cfg, err := config.LoadFile(path)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}

	authority := config.DetectBootstrapEnvAuthority()
	fileAuth := cfg.Auth
	fileProxyPassword := cfg.Proxy.Password
	cfg.App.Title = in.App.Title
	cfg.App.NotificationPrefix = in.App.NotificationPrefix
	cfg.Auth.Enabled = in.Auth.Enabled
	cfg.Auth.Username = in.Auth.Username
	cfg.Auth.Password = preserveRedactedSecret(fileAuth.Password, in.Auth.Password)
	cfg.Auth.TokenSecret = preserveRedactedSecret(fileAuth.TokenSecret, in.Auth.TokenSecret)
	cfg.Auth.SessionTTLHours = in.Auth.SessionTTLHours
	cfg.Auth.Sub2APIEmbed = in.Auth.Sub2APIEmbed
	if authority.AuthEnabled {
		cfg.Auth.Enabled = fileAuth.Enabled
	}
	if authority.AuthUsername {
		cfg.Auth.Username = fileAuth.Username
	}
	if authority.AuthPassword {
		cfg.Auth.Password = fileAuth.Password
	}
	if authority.AuthTokenSecret {
		cfg.Auth.TokenSecret = fileAuth.TokenSecret
	}
	cfg.Scheduler = in.Scheduler
	cfg.Notifications = in.Notifications
	cfg.Proxy = in.Proxy
	cfg.Proxy.Password = preserveRedactedSecret(fileProxyPassword, in.Proxy.Password)
	cfg.Upstream = in.Upstream.WithDefaults()

	if err := config.Save(path, cfg); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"config_path": path,
			"message":     "已写入配置文件",
		},
	})
}

func applySettingsConfig(c *gin.Context, d *Deps) {
	result, err := d.Runtime.ApplyFromFile()
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": result})
}

func settingsAuthViewFromConfig(cfg config.AuthConfig) settingsAuthView {
	return settingsAuthView{
		Enabled:         cfg.Enabled,
		Username:        cfg.Username,
		Password:        redactSecret(cfg.Password),
		TokenSecret:     redactSecret(cfg.TokenSecret),
		SessionTTLHours: cfg.SessionTTLHours,
		Sub2APIEmbed:    cfg.Sub2APIEmbed,
	}
}

func settingsProxyViewFromConfig(cfg config.ProxyConfig) settingsProxyView {
	return settingsProxyView{
		Enabled:             cfg.Enabled,
		VersionCheckEnabled: cfg.VersionCheckEnabled,
		Protocol:            cfg.Protocol,
		Host:                cfg.Host,
		Port:                cfg.Port,
		Username:            cfg.Username,
		Password:            redactSecret(cfg.Password),
	}
}

func redactSecret(value string) string {
	if value == "" {
		return ""
	}
	return redactedSecret
}

func preserveRedactedSecret(existing, incoming string) string {
	if incoming == redactedSecret {
		return existing
	}
	return incoming
}
