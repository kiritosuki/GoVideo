package ratelimit

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	rediscache "github.com/kiritosuki/GoVideo/internal/middleware/redis"
)

type KeyFunc func(ctx *gin.Context) (string, bool)

// TODO 限流机制 接着实现...

// Limit 限流函数
// 如果函数中出现error则会跳过限流机制 防止限流故障导致系统崩溃
func Limit(cache *rediscache.Client, keyPrefix string, maxRequests int64, window time.Duration, keyFunc KeyFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cache == nil || keyFunc == nil || maxRequests <= 0 || window <= 0 {
			c.Next()
			return
		}
		subject, ok := keyFunc(c)
		if !ok {
			c.Next()
			return
		}
		key := buildKey(keyPrefix, subject)
		count, err := cache.IncrementWithExpr(c.Request.Context(), key, window)
		if err != nil {
			c.Next()
			return
		}
		if count > maxRequests {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "too many requests"})
			return
		}
		c.Next()
	}
}

// buildKey 创建限流key 拼接前缀与subject
func buildKey(keyPrefix string, subject string) string {
	keyPrefix = strings.TrimSpace(keyPrefix)
	if keyPrefix == "" {
		keyPrefix = "default"
	}
	return fmt.Sprintf("govideo:ratelimit:%s:%s", keyPrefix, strings.TrimSpace(subject))
}
