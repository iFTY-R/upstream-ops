package channel

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/bejix/upstream-ops/backend/config"
	"github.com/bejix/upstream-ops/backend/connector"
	"github.com/bejix/upstream-ops/backend/crypto"
	"github.com/bejix/upstream-ops/backend/storage"
)

type fakeHTTPConfigConnector struct {
	cfg connector.HTTPConfig
}

func (f *fakeHTTPConfigConnector) SetHTTPConfig(cfg connector.HTTPConfig) {
	f.cfg = cfg
}

func testService(t *testing.T) (*Service, *crypto.Cipher) {
	t.Helper()
	db, err := storage.Open(storage.DBConfig{
		Driver: storage.DBDriverSQLite,
		Path:   filepath.Join(t.TempDir(), "test.db"),
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	cipher, err := crypto.NewCipher("12345678901234567890123456789012")
	if err != nil {
		t.Fatalf("new cipher: %v", err)
	}
	svc := NewService(
		storage.NewChannels(db),
		storage.NewAuthSessions(db),
		storage.NewCaptchas(db),
		storage.NewRates(db),
		storage.NewMonitorLogs(db),
		cipher,
	)
	return svc, cipher
}

func TestResolveAppliesProxyOnlyWhenEnabled(t *testing.T) {
	svc, cipher := testService(t)
	svc.UpdateProxyConfig(config.ProxyConfig{
		Enabled:  true,
		Protocol: "socks5",
		Host:     "127.0.0.1",
		Port:     1080,
		Username: "u",
		Password: "p",
	})

	enc, err := cipher.Encrypt("secret")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	withProxy := &storage.Channel{
		ID:             1,
		Name:           "with-proxy",
		Type:           storage.ChannelTypeNewAPI,
		SiteURL:        "https://example.com",
		Username:       "u",
		PasswordCipher: enc,
		ProxyEnabled:   true,
	}
	resolved, err := svc.Resolve(context.Background(), withProxy)
	if err != nil {
		t.Fatalf("resolve with proxy: %v", err)
	}
	if resolved.ProxyURL != "socks5://u:p@127.0.0.1:1080" {
		t.Fatalf("proxy url = %q", resolved.ProxyURL)
	}

	withoutProxy := *withProxy
	withoutProxy.ProxyEnabled = false
	resolved, err = svc.Resolve(context.Background(), &withoutProxy)
	if err != nil {
		t.Fatalf("resolve without proxy: %v", err)
	}
	if resolved.ProxyURL != "" {
		t.Fatalf("proxy url = %q, want empty", resolved.ProxyURL)
	}
}

func TestResolveSkipsProxyWhenGlobalProxyDisabled(t *testing.T) {
	svc, cipher := testService(t)
	svc.UpdateProxyConfig(config.ProxyConfig{
		Protocol: "socks5",
		Host:     "127.0.0.1",
		Port:     1080,
	})
	enc, err := cipher.Encrypt("secret")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	resolved, err := svc.Resolve(context.Background(), &storage.Channel{
		ID:             1,
		Name:           "with-proxy",
		Type:           storage.ChannelTypeNewAPI,
		SiteURL:        "https://example.com",
		Username:       "u",
		PasswordCipher: enc,
		ProxyEnabled:   true,
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved.ProxyURL != "" {
		t.Fatalf("proxy url = %q, want empty", resolved.ProxyURL)
	}
}

func TestApplyHTTPConfigUsesUpstreamConfig(t *testing.T) {
	svc, _ := testService(t)
	svc.UpdateUpstreamConfig(config.UpstreamConfig{
		TimeoutSeconds: 45,
		UserAgent:      "custom-agent",
	})

	conn := &fakeHTTPConfigConnector{}
	svc.ApplyHTTPConfig(conn)

	if conn.cfg.Timeout != 45*time.Second {
		t.Fatalf("timeout = %s", conn.cfg.Timeout)
	}
	if conn.cfg.UserAgent != "custom-agent" {
		t.Fatalf("user agent = %q", conn.cfg.UserAgent)
	}
}

func TestApplyHTTPConfigUsesDefaults(t *testing.T) {
	svc, _ := testService(t)
	conn := &fakeHTTPConfigConnector{}
	svc.ApplyHTTPConfig(conn)

	if conn.cfg.Timeout != time.Duration(config.DefaultUpstreamTimeoutSeconds)*time.Second {
		t.Fatalf("timeout = %s", conn.cfg.Timeout)
	}
	if conn.cfg.UserAgent != config.DefaultUpstreamUserAgent {
		t.Fatalf("user agent = %q", conn.cfg.UserAgent)
	}
}
