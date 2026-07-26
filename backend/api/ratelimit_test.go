package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestPublicRateLimiterEnforcesPerKeyWindow(t *testing.T) {
	limiter := newPublicRateLimiter(3, time.Minute)
	current := time.Unix(1700000000, 0)
	limiter.now = func() time.Time { return current }

	for i := 0; i < 3; i++ {
		if !limiter.allow("1.2.3.4") {
			t.Fatalf("request %d within quota should pass", i+1)
		}
	}
	if limiter.allow("1.2.3.4") {
		t.Fatal("request over quota should be limited")
	}
	if !limiter.allow("5.6.7.8") {
		t.Fatal("a different client must not share the quota")
	}

	current = current.Add(time.Minute)
	if !limiter.allow("1.2.3.4") {
		t.Fatal("a new window should reset the quota")
	}
}

func TestPublicRateLimiterFailsOpenAtCapacity(t *testing.T) {
	limiter := newPublicRateLimiter(1, time.Minute)
	limiter.maxKeys = 2
	current := time.Unix(1700000000, 0)
	limiter.now = func() time.Time { return current }

	if !limiter.allow("k1") || !limiter.allow("k2") {
		t.Fatal("seed keys should pass")
	}
	if !limiter.allow("k3") {
		t.Fatal("over-capacity key should fail open instead of blocking new clients")
	}
	if _, tracked := limiter.entries["k3"]; tracked {
		t.Fatal("over-capacity key must not grow the map")
	}

	current = current.Add(2 * time.Minute)
	if !limiter.allow("k3") {
		t.Fatal("expired entries should be evicted to make room")
	}
	if _, tracked := limiter.entries["k3"]; !tracked {
		t.Fatal("k3 should be tracked once capacity is reclaimed")
	}
}

func TestPublicRateLimitMiddlewareReturns429(t *testing.T) {
	gin.SetMode(gin.TestMode)
	limiter := newPublicRateLimiter(2, time.Minute)
	router := gin.New()
	router.GET("/api/public/ping", limiter.middleware(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	do := func(remoteAddr string) int {
		req := httptest.NewRequest(http.MethodGet, "/api/public/ping", nil)
		req.RemoteAddr = remoteAddr
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w.Code
	}

	if code := do("9.9.9.9:1000"); code != http.StatusOK {
		t.Fatalf("first request status = %d, want 200", code)
	}
	if code := do("9.9.9.9:1001"); code != http.StatusOK {
		t.Fatalf("second request status = %d, want 200", code)
	}
	if code := do("9.9.9.9:1002"); code != http.StatusTooManyRequests {
		t.Fatalf("third request status = %d, want 429", code)
	}
	if code := do("8.8.8.8:1000"); code != http.StatusOK {
		t.Fatalf("other client status = %d, want 200", code)
	}
}
