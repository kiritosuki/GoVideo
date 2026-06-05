package ratelimit

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/kiritosuki/GoVideo/internal/config"
	rediscache "github.com/kiritosuki/GoVideo/internal/middleware/redis"
)

// 测试限流key的拼接规则
// keyPrefix为空时会使用default 避免生成缺少业务前缀的key
func TestBuildKey(t *testing.T) {
	got := buildKey("  login  ", "  127.0.0.1  ")
	want := "govideo:ratelimit:login:127.0.0.1"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}

	got = buildKey("  ", "user-1")
	want = "govideo:ratelimit:default:user-1"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

// 测试可以从gin context中读取accountID作为限流主体
func TestKeyByAccount(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("accountID", uint(42))

	subject, ok := KeyByAccount(c)
	if !ok || subject != "42" {
		t.Fatalf("expected account subject 42, got subject=%q ok=%v", subject, ok)
	}
}

// 测试没有accountID或类型错误时 不启用账号维度限流
func TestKeyByAccountMissingOrInvalid(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	if subject, ok := KeyByAccount(c); ok || subject != "" {
		t.Fatalf("expected missing accountID to return false, got subject=%q ok=%v", subject, ok)
	}

	c.Set("accountID", "42")
	if subject, ok := KeyByAccount(c); ok || subject != "" {
		t.Fatalf("expected invalid accountID type to return false, got subject=%q ok=%v", subject, ok)
	}
}

// 测试Redis未启用时限流中间件会直接放行
// 这是项目的降级策略: 限流故障不能影响核心接口可用性
func TestLimitAllowsWhenCacheIsNil(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/test", Limit(nil, "login", 1, time.Minute, KeyByIP), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	resp := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	r.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected request to pass when cache is nil, got status %d", resp.Code)
	}
}

// 测试滑动窗口限流超过阈值后返回429
// 这里使用miniredis替代真实Redis 只验证限流中间件与Redis封装的协作逻辑
func TestLimitRejectsWhenSlidingWindowExceeded(t *testing.T) {
	cache, cleanup := newRateLimitRedisClient(t)
	defer cleanup()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/test", Limit(cache, "login", 1, time.Minute, KeyByIP), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	first := httptest.NewRecorder()
	r.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/test", nil))
	if first.Code != http.StatusOK {
		t.Fatalf("expected first request to pass, got status %d", first.Code)
	}

	second := httptest.NewRecorder()
	r.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/test", nil))
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("expected second request to be rate limited, got status %d", second.Code)
	}
}

func newRateLimitRedisClient(t *testing.T) (*rediscache.Client, func()) {
	t.Helper()

	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	cache, err := rediscache.NewClient(&config.RedisConfig{
		Host: mr.Host(),
		Port: miniRedisPort(t, mr),
		DB:   0,
	})
	if err != nil {
		mr.Close()
		t.Fatalf("new redis client: %v", err)
	}
	return cache, func() {
		cache.Close()
		mr.Close()
	}
}

func miniRedisPort(t *testing.T, mr *miniredis.Miniredis) int {
	t.Helper()

	_, portStr, err := net.SplitHostPort(mr.Addr())
	if err != nil {
		t.Fatalf("parse miniredis addr: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse miniredis port: %v", err)
	}
	return port
}
