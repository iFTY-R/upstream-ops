package api

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// publicRateLimitPerMinute 是 /api/public 端点每个客户端每分钟的请求配额。
// 公开页面 30s 轮询 + 正常翻页远低于该值；超限只影响该客户端。
const publicRateLimitPerMinute = 120

// publicRateLimiter 是给未鉴权公共端点用的每客户端固定窗口限流器。
// 匿名流量每个请求都会落到数据库查询，这里保证单个来源无法无限放大。
type publicRateLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	maxKeys int
	now     func() time.Time
	entries map[string]*rateLimitWindow
}

type rateLimitWindow struct {
	start time.Time
	count int
}

func newPublicRateLimiter(limit int, window time.Duration) *publicRateLimiter {
	return &publicRateLimiter{
		limit:   limit,
		window:  window,
		maxKeys: 10000,
		now:     time.Now,
		entries: make(map[string]*rateLimitWindow),
	}
}

func (l *publicRateLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if entry, ok := l.entries[key]; ok && now.Sub(entry.start) < l.window {
		if entry.count >= l.limit {
			return false
		}
		entry.count++
		return true
	}
	if len(l.entries) >= l.maxKeys {
		for k, entry := range l.entries {
			if now.Sub(entry.start) >= l.window {
				delete(l.entries, k)
			}
		}
		if len(l.entries) >= l.maxKeys {
			// 活跃 key 超出容量（多半是伪造来源地址）。此时放行且不记账：
			// 内存上限优先于严格限流，避免新访客被恶意流量挤出。
			return true
		}
	}
	l.entries[key] = &rateLimitWindow{start: now, count: 1}
	return true
}

func (l *publicRateLimiter) middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if l.allow(c.ClientIP()) {
			c.Next()
			return
		}
		c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "请求过于频繁，请稍后再试"})
	}
}

// publicRateLimitMiddleware 返回带默认配额的公共端点限流中间件。
func publicRateLimitMiddleware() gin.HandlerFunc {
	return newPublicRateLimiter(publicRateLimitPerMinute, time.Minute).middleware()
}
