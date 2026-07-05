package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRegisterRoutesDoesNotPanic(t *testing.T) {
	gin.SetMode(gin.TestMode)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("register routes panicked: %v", r)
		}
	}()

	Register(gin.New(), &Deps{})
}

func TestAutoGroupRoutesReturnServiceUnavailableWhenDepsMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	Register(router, &Deps{})

	for _, path := range []string{
		"/api/auto-groups",
		"/api/auto-groups/1",
		"/api/auto-groups/1/candidates",
		"/api/auto-groups/1/evaluation-logs",
		"/api/channels/1/auto-group-policy",
		"/api/channels/1/auto-group-policy/evaluate",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if path == "/api/channels/1/auto-group-policy/evaluate" {
			req = httptest.NewRequest(http.MethodPost, path, nil)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s status = %d, want %d; body=%s", path, w.Code, http.StatusServiceUnavailable, w.Body.String())
		}
	}
}
