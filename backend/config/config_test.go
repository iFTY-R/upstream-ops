package config

import (
	"path/filepath"
	"testing"
)

func TestLoadAppliesUpstreamDefaults(t *testing.T) {
	cfg, err := LoadFile(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if cfg.Upstream.TimeoutSeconds != DefaultUpstreamTimeoutSeconds {
		t.Fatalf("timeout seconds = %d", cfg.Upstream.TimeoutSeconds)
	}
	if cfg.Upstream.UserAgent != DefaultUpstreamUserAgent {
		t.Fatalf("user agent = %q", cfg.Upstream.UserAgent)
	}
	if cfg.Upstream.ShopRequestIntervalMilliseconds != DefaultShopRequestIntervalMilliseconds {
		t.Fatalf("shop request interval = %d", cfg.Upstream.ShopRequestIntervalMilliseconds)
	}
	if cfg.Upstream.ShopInfoTTLHours != DefaultShopInfoTTLHours {
		t.Fatalf("shop info TTL = %d", cfg.Upstream.ShopInfoTTLHours)
	}
}

func TestLoadAppliesStaggeredShopCronDefault(t *testing.T) {
	cfg, err := LoadFile(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if cfg.Scheduler.ShopCron != "41 7,37 8-22 * * *" {
		t.Fatalf("shop cron = %q", cfg.Scheduler.ShopCron)
	}
}

func TestLoadEnablesAuthByDefault(t *testing.T) {
	cfg, err := LoadFile(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if !cfg.Auth.Enabled {
		t.Fatal("auth should be enabled by default")
	}
}

func TestLoadAppliesSub2APIEmbedDefaults(t *testing.T) {
	cfg, err := LoadFile(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if cfg.Auth.Sub2APIEmbed.Enabled {
		t.Fatalf("sub2api embed enabled by default")
	}
	if !cfg.Auth.Sub2APIEmbed.RequireAdmin {
		t.Fatalf("sub2api embed should require admin by default")
	}
}

func TestUpstreamConfigWithDefaultsKeepsCustomUserAgent(t *testing.T) {
	cfg := UpstreamConfig{
		TimeoutSeconds: 0,
		UserAgent:      "custom-agent",
	}.WithDefaults()
	if cfg.TimeoutSeconds != DefaultUpstreamTimeoutSeconds {
		t.Fatalf("timeout seconds = %d", cfg.TimeoutSeconds)
	}
	if cfg.UserAgent != "custom-agent" {
		t.Fatalf("user agent = %q", cfg.UserAgent)
	}
}

func TestUpstreamConfigWithDefaultsRemovesLegacyOpsUserAgent(t *testing.T) {
	cfg := UpstreamConfig{UserAgent: LegacyUpstreamUserAgent}.WithDefaults()
	if cfg.UserAgent != "" {
		t.Fatalf("legacy user agent = %q, want empty provider default", cfg.UserAgent)
	}
}

func TestLoadRuntimeFileAppliesBootstrapEnvOverrides(t *testing.T) {
	t.Setenv("APP_SECRET", "env-app-secret")
	t.Setenv("AUTH_ENABLED", "true")
	t.Setenv("ADMIN_USERNAME", "env-admin")
	t.Setenv("ADMIN_PASSWORD", "env-password")
	t.Setenv("AUTH_TOKEN_SECRET", "env-token-secret")

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := Save(path, &Config{
		Security: SecurityConfig{AppSecret: "file-app-secret"},
		Auth: AuthConfig{
			Enabled:         false,
			Username:        "file-admin",
			Password:        "file-password",
			TokenSecret:     "file-token-secret",
			SessionTTLHours: 24,
		},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cfg, err := LoadRuntimeFile(path)
	if err != nil {
		t.Fatalf("LoadRuntimeFile: %v", err)
	}
	if cfg.Security.AppSecret != "env-app-secret" {
		t.Fatalf("app secret = %q", cfg.Security.AppSecret)
	}
	if !cfg.Auth.Enabled {
		t.Fatalf("auth should be enabled by env override")
	}
	if cfg.Auth.Username != "env-admin" {
		t.Fatalf("username = %q", cfg.Auth.Username)
	}
	if cfg.Auth.Password != "env-password" {
		t.Fatalf("password = %q", cfg.Auth.Password)
	}
	if cfg.Auth.TokenSecret != "env-token-secret" {
		t.Fatalf("token secret = %q", cfg.Auth.TokenSecret)
	}
	if cfg.Auth.SessionTTLHours != 24 {
		t.Fatalf("session ttl = %d", cfg.Auth.SessionTTLHours)
	}
}

func TestPrepareForInitialSaveScrubsBootstrapSecrets(t *testing.T) {
	t.Setenv("APP_SECRET", "env-app-secret")
	t.Setenv("AUTH_ENABLED", "true")
	t.Setenv("ADMIN_USERNAME", "env-admin")
	t.Setenv("ADMIN_PASSWORD", "env-password")
	t.Setenv("AUTH_TOKEN_SECRET", "env-token-secret")

	sanitized := PrepareForInitialSave(&Config{
		Security: SecurityConfig{AppSecret: "env-app-secret"},
		Auth: AuthConfig{
			Enabled:         true,
			Username:        "env-admin",
			Password:        "env-password",
			TokenSecret:     "env-token-secret",
			SessionTTLHours: 72,
		},
	})

	if sanitized.Security.AppSecret != "" {
		t.Fatalf("app secret should be blank, got %q", sanitized.Security.AppSecret)
	}
	if sanitized.Auth.Enabled {
		t.Fatalf("auth enabled should not be persisted from env bootstrap")
	}
	if sanitized.Auth.Username != DefaultAuthUsername {
		t.Fatalf("username = %q", sanitized.Auth.Username)
	}
	if sanitized.Auth.Password != "" {
		t.Fatalf("password should be blank, got %q", sanitized.Auth.Password)
	}
	if sanitized.Auth.TokenSecret != "" {
		t.Fatalf("token secret should be blank, got %q", sanitized.Auth.TokenSecret)
	}
	if sanitized.Auth.SessionTTLHours != 72 {
		t.Fatalf("session ttl = %d", sanitized.Auth.SessionTTLHours)
	}
}
