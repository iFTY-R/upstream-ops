package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ifty-r/upstream-ops/backend/auth"
	"github.com/ifty-r/upstream-ops/backend/config"
	"github.com/ifty-r/upstream-ops/backend/runtimeconfig"
)

func TestSub2APIExchangeIssuesOpsToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sub2api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/auth/me" {
			t.Fatalf("sub2api path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sub-token" {
			t.Fatalf("authorization = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"id":   1,
				"role": "admin",
			},
		})
	}))
	defer sub2api.Close()

	router, authSvc := newSub2APIExchangeTestRouter(t, config.Sub2APIEmbedConfig{
		Enabled:      true,
		BaseURL:      sub2api.URL,
		RequireAdmin: true,
	})

	body := bytes.NewBufferString(`{"token":"sub-token","src_host":"` + sub2api.URL + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/sub2api/exchange", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("exchange status = %d, body=%s", w.Code, w.Body.String())
	}

	var out struct {
		Data struct {
			Token    string `json:"token"`
			Username string `json:"username"`
			Source   string `json:"source"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode exchange response: %v", err)
	}
	if out.Data.Username != "admin" || out.Data.Source != "sub2api" {
		t.Fatalf("exchange data = %#v", out.Data)
	}
	if sub, err := authSvc.Verify(out.Data.Token); err != nil || sub != "admin" {
		t.Fatalf("issued token verify = %q, %v", sub, err)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+out.Data.Token)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("me status = %d, body=%s", w.Code, w.Body.String())
	}
}

func TestSub2APIExchangeSupportsBaseURLWithAPIV1Prefix(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sub2api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/auth/me" {
			t.Fatalf("sub2api path = %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"id":   1,
				"role": "admin",
			},
		})
	}))
	defer sub2api.Close()

	router, _ := newSub2APIExchangeTestRouter(t, config.Sub2APIEmbedConfig{
		Enabled:      true,
		BaseURL:      sub2api.URL + "/api/v1",
		RequireAdmin: true,
	})

	req := httptest.NewRequest(http.MethodPost, "/api/auth/sub2api/exchange", bytes.NewBufferString(`{"token":"sub-token"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("exchange status = %d, body=%s", w.Code, w.Body.String())
	}
}

func TestSub2APIExchangeFallsBackToLegacyAPIPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sub2api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/auth/me" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path != "/api/auth/me" {
			t.Fatalf("sub2api path = %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"id":   1,
				"role": "admin",
			},
		})
	}))
	defer sub2api.Close()

	router, _ := newSub2APIExchangeTestRouter(t, config.Sub2APIEmbedConfig{
		Enabled:      true,
		BaseURL:      sub2api.URL,
		RequireAdmin: true,
	})

	req := httptest.NewRequest(http.MethodPost, "/api/auth/sub2api/exchange", bytes.NewBufferString(`{"token":"sub-token"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("exchange status = %d, body=%s", w.Code, w.Body.String())
	}
}

func TestSub2APIExchangeDoesNotFallbackWhenAPIV1RejectsToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	legacyCalled := false
	sub2api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/auth/me" {
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/api/auth/me" {
			legacyCalled = true
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"id":   1,
					"role": "admin",
				},
			})
			return
		}
		t.Fatalf("sub2api path = %s", r.URL.Path)
	}))
	defer sub2api.Close()

	router, _ := newSub2APIExchangeTestRouter(t, config.Sub2APIEmbedConfig{
		Enabled:      true,
		BaseURL:      sub2api.URL,
		RequireAdmin: true,
	})

	req := httptest.NewRequest(http.MethodPost, "/api/auth/sub2api/exchange", bytes.NewBufferString(`{"token":"bad-token"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("exchange status = %d, body=%s", w.Code, w.Body.String())
	}
	if legacyCalled {
		t.Fatalf("legacy auth/me should not be called after api/v1 rejects token")
	}
}

func TestSub2APIExchangeRejectsWhenDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router, _ := newSub2APIExchangeTestRouter(t, config.Sub2APIEmbedConfig{Enabled: false})

	req := httptest.NewRequest(http.MethodPost, "/api/auth/sub2api/exchange", bytes.NewBufferString(`{"token":"sub-token"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
}

func TestSub2APIExchangeRejectsNonAdminWhenRequired(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sub2api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"id":   2,
				"role": "user",
			},
		})
	}))
	defer sub2api.Close()

	router, _ := newSub2APIExchangeTestRouter(t, config.Sub2APIEmbedConfig{
		Enabled:      true,
		BaseURL:      sub2api.URL,
		RequireAdmin: true,
	})

	req := httptest.NewRequest(http.MethodPost, "/api/auth/sub2api/exchange", bytes.NewBufferString(`{"token":"sub-token"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
}

func newSub2APIExchangeTestRouter(t *testing.T, embed config.Sub2APIEmbedConfig) (*gin.Engine, *auth.Service) {
	t.Helper()
	cfg := &config.Config{
		Auth: config.AuthConfig{
			Enabled:         true,
			Username:        "admin",
			Password:        "password",
			TokenSecret:     "test-secret",
			SessionTTLHours: 1,
			Sub2APIEmbed:    embed,
		},
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := config.Save(path, cfg); err != nil {
		t.Fatalf("save config: %v", err)
	}
	authSvc, err := auth.New("admin", "password", "test-secret", time.Hour)
	if err != nil {
		t.Fatalf("auth.New: %v", err)
	}
	runtimeMgr := runtimeconfig.New(path, "test-secret", nil, nil, nil, nil, authSvc, nil, config.ProxyConfig{}, config.UpstreamConfig{}, nil)
	router := gin.New()
	api := router.Group("/api")
	api.Use(runtimeMgr.AuthMiddleware())
	registerAuth(api, &Deps{Runtime: runtimeMgr})
	return router, authSvc
}
