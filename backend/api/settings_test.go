package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ifty-r/upstream-ops/backend/config"
	"github.com/ifty-r/upstream-ops/backend/runtimeconfig"
	"github.com/ifty-r/upstream-ops/backend/scheduler"
	"github.com/ifty-r/upstream-ops/backend/storage"
)

func TestSaveSettingsKeepsAppVersion(t *testing.T) {
	gin.SetMode(gin.TestMode)

	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := &config.Config{
		App: config.AppConfig{
			Title:              "Old",
			NotificationPrefix: "[Old] ",
		},
	}
	if err := config.Save(path, cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	r := gin.New()
	api := r.Group("/api")
	registerSettings(api, &Deps{
		Runtime: runtimeconfig.New(path, "", nil, nil, nil, nil, nil, nil, config.ProxyConfig{}, config.UpstreamConfig{}, nil),
	})

	body := `{
		"app":{"title":"New","notificationPrefix":"[New] "},
		"auth":{"enabled":false,"username":"admin","password":"","tokenSecret":"","sessionTTLHours":168},
		"scheduler":{"balanceCron":"37 */15 * * * *","rateCron":"13 */30 * * * *","concurrency":4,"retention":{"cron":"0 17 3 * * *","monitorLogsDays":30,"balanceSnapshotsDays":90,"notificationLogsDays":90,"announcementsDays":90,"shopHighFrequencyChangeLogsDays":15,"shopOtherChangeLogsDays":90,"shopMonitorLogsDays":30,"shopSyncJobsDays":30}},
		"notifications":{"batchRateChanges":true,"minChangePct":0,"balanceLowCooldownMinutes":60,"subscriptionDailyRemainingThresholdPct":0,"subscriptionWeeklyRemainingThresholdPct":0,"subscriptionMonthlyRemainingThresholdPct":0,"subscriptionExpiryThresholdHours":0,"subscriptionAlertCooldownMinutes":1440,"sendMaxAttempts":3},
		"proxy":{"enabled":true,"versionCheckEnabled":true,"protocol":"socks5","host":"127.0.0.1","port":1080,"username":"u","password":"p"},
		"upstream":{"timeoutSeconds":45,"userAgent":"custom-agent"}
	}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings/config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	got, err := config.LoadFile(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if got.App.Title != "New" {
		t.Fatalf("title = %q", got.App.Title)
	}
	if got.App.NotificationPrefix != "[New] " {
		t.Fatalf("notification prefix = %q", got.App.NotificationPrefix)
	}
	if !got.Proxy.Enabled || !got.Proxy.VersionCheckEnabled || got.Proxy.Protocol != "socks5" || got.Proxy.Host != "127.0.0.1" || got.Proxy.Port != 1080 || got.Proxy.Username != "u" || got.Proxy.Password != "p" {
		t.Fatalf("proxy = %#v", got.Proxy)
	}
	if got.Upstream.TimeoutSeconds != 45 || got.Upstream.UserAgent != "custom-agent" {
		t.Fatalf("upstream = %#v", got.Upstream)
	}
	retention := got.Scheduler.Retention
	if retention.ShopHighFrequencyChangeLogsDays != 15 || retention.ShopOtherChangeLogsDays != 90 || retention.ShopMonitorLogsDays != 30 || retention.ShopSyncJobsDays != 30 {
		t.Fatalf("shop retention = %#v", retention)
	}
}

func TestGetSettingsConfigRedactsSecrets(t *testing.T) {
	gin.SetMode(gin.TestMode)

	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := &config.Config{
		Auth: config.AuthConfig{
			Enabled:         true,
			Username:        "admin",
			Password:        "super-secret",
			TokenSecret:     "token-secret",
			SessionTTLHours: 168,
		},
		Proxy: config.ProxyConfig{
			Enabled:             true,
			VersionCheckEnabled: true,
			Protocol:            "http",
			Host:                "127.0.0.1",
			Port:                1080,
			Username:            "proxy-user",
			Password:            "proxy-secret",
		},
	}
	if err := config.Save(path, cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	r := gin.New()
	api := r.Group("/api")
	registerSettings(api, &Deps{
		Runtime: runtimeconfig.New(path, "", nil, nil, nil, nil, nil, nil, config.ProxyConfig{}, config.UpstreamConfig{}, nil),
	})

	req := httptest.NewRequest(http.MethodGet, "/api/settings/config", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "super-secret") || strings.Contains(rec.Body.String(), "token-secret") || strings.Contains(rec.Body.String(), "proxy-secret") {
		t.Fatalf("response leaked secret: %s", rec.Body.String())
	}

	var resp struct {
		Data struct {
			Config struct {
				Auth struct {
					Password    string `json:"password"`
					TokenSecret string `json:"tokenSecret"`
				} `json:"auth"`
				Proxy struct {
					Password string `json:"password"`
				} `json:"proxy"`
			} `json:"config"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Data.Config.Auth.Password != redactedSecret {
		t.Fatalf("auth password = %q", resp.Data.Config.Auth.Password)
	}
	if resp.Data.Config.Auth.TokenSecret != redactedSecret {
		t.Fatalf("auth token secret = %q", resp.Data.Config.Auth.TokenSecret)
	}
	if resp.Data.Config.Proxy.Password != redactedSecret {
		t.Fatalf("proxy password = %q", resp.Data.Config.Proxy.Password)
	}
}

func TestCleanupShopHistoryRunsWithSubmittedPolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := openTestDB(t)
	goods := storage.NewShopGoods(db)
	jobs := storage.NewShopSyncJobs(db)
	now := time.Now()

	for _, change := range []storage.ShopGoodsChangeLog{
		{TargetID: 1, Event: storage.ShopChangeStockChanged, Summary: "old stock", ChangedAt: now.AddDate(0, 0, -20)},
		{TargetID: 1, Event: storage.ShopChangePriceChanged, Summary: "old price", ChangedAt: now.AddDate(0, 0, -100)},
	} {
		change := change
		if err := goods.AppendChange(&change); err != nil {
			t.Fatalf("append change: %v", err)
		}
	}
	oldMonitorAt := now.AddDate(0, 0, -31)
	if err := goods.AppendMonitorLog(&storage.ShopMonitorLog{TargetID: 1, Success: true, StartedAt: oldMonitorAt, FinishedAt: oldMonitorAt.Add(time.Second)}); err != nil {
		t.Fatalf("append monitor log: %v", err)
	}
	oldFinishedAt := now.AddDate(0, 0, -31)
	if err := jobs.Create(&storage.ShopSyncJob{TargetID: 1, Status: storage.ShopSyncJobSucceeded, FinishedAt: &oldFinishedAt, CreatedAt: oldFinishedAt}); err != nil {
		t.Fatalf("create sync job: %v", err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	sch := scheduler.New(config.SchedulerConfig{}, nil, nil, nil, nil, nil, nil, nil, goods, jobs, nil, nil, config.ProxyConfig{}, log)
	runtimeMgr := runtimeconfig.New(filepath.Join(t.TempDir(), "config.yaml"), "", log, nil, nil, nil, nil, sch, config.ProxyConfig{}, config.UpstreamConfig{}, nil)
	router := gin.New()
	registerSettings(router.Group("/api"), &Deps{Runtime: runtimeMgr})

	body := `{"shopHighFrequencyChangeLogsDays":15,"shopOtherChangeLogsDays":90,"shopMonitorLogsDays":30,"shopSyncJobsDays":30}`
	req := httptest.NewRequest(http.MethodPost, "/api/settings/retention/shop/cleanup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Data scheduler.ShopRetentionResult `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	result := response.Data
	if result.HighFrequencyChangesDeleted != 1 || result.OtherChangesDeleted != 1 || result.MonitorLogsDeleted != 1 || result.SyncJobsDeleted != 1 || result.TotalDeleted != 4 {
		t.Fatalf("cleanup result = %#v", result)
	}
}

func TestSaveSettingsPreservesEnvManagedBootstrapAndRedactedSecrets(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("AUTH_ENABLED", "true")
	t.Setenv("ADMIN_USERNAME", "env-admin")
	t.Setenv("ADMIN_PASSWORD", "env-password")
	t.Setenv("AUTH_TOKEN_SECRET", "env-token-secret")

	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := &config.Config{
		App: config.AppConfig{
			Title:              "Old",
			NotificationPrefix: "[Old] ",
		},
		Auth: config.AuthConfig{
			Enabled:         false,
			Username:        "file-admin",
			Password:        "file-password",
			TokenSecret:     "file-token-secret",
			SessionTTLHours: 168,
		},
		Proxy: config.ProxyConfig{
			Enabled:             true,
			VersionCheckEnabled: true,
			Protocol:            "http",
			Host:                "127.0.0.1",
			Port:                1080,
			Username:            "proxy-user",
			Password:            "file-proxy-password",
		},
	}
	if err := config.Save(path, cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}

	r := gin.New()
	api := r.Group("/api")
	registerSettings(api, &Deps{
		Runtime: runtimeconfig.New(path, "", nil, nil, nil, nil, nil, nil, config.ProxyConfig{}, config.UpstreamConfig{}, nil),
	})

	body := `{
		"app":{"title":"New","notificationPrefix":"[New] "},
		"auth":{"enabled":true,"username":"ui-admin","password":"` + redactedSecret + `","tokenSecret":"` + redactedSecret + `","sessionTTLHours":24,"sub2apiEmbed":{"enabled":false,"baseURL":"","allowedOrigins":[],"requireAdmin":true}},
		"scheduler":{"balanceCron":"37 */15 * * * *","rateCron":"13 */30 * * * *","concurrency":4,"retention":{"cron":"0 17 3 * * *","monitorLogsDays":30,"balanceSnapshotsDays":90,"notificationLogsDays":90,"announcementsDays":90}},
		"notifications":{"batchRateChanges":true,"minChangePct":0,"balanceLowCooldownMinutes":60,"subscriptionDailyRemainingThresholdPct":0,"subscriptionWeeklyRemainingThresholdPct":0,"subscriptionMonthlyRemainingThresholdPct":0,"subscriptionExpiryThresholdHours":0,"subscriptionAlertCooldownMinutes":1440,"sendMaxAttempts":3},
		"proxy":{"enabled":true,"versionCheckEnabled":true,"protocol":"socks5","host":"127.0.0.1","port":1080,"username":"proxy-user","password":"` + redactedSecret + `"},
		"upstream":{"timeoutSeconds":45,"userAgent":"custom-agent"}
	}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings/config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	got, err := config.LoadFile(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if got.Auth.Enabled {
		t.Fatalf("auth enabled should preserve file value under env authority")
	}
	if got.Auth.Username != "file-admin" {
		t.Fatalf("username = %q", got.Auth.Username)
	}
	if got.Auth.Password != "file-password" {
		t.Fatalf("password = %q", got.Auth.Password)
	}
	if got.Auth.TokenSecret != "file-token-secret" {
		t.Fatalf("token secret = %q", got.Auth.TokenSecret)
	}
	if got.Auth.SessionTTLHours != 24 {
		t.Fatalf("session ttl = %d", got.Auth.SessionTTLHours)
	}
	if got.Proxy.Password != "file-proxy-password" {
		t.Fatalf("proxy password = %q", got.Proxy.Password)
	}
}
