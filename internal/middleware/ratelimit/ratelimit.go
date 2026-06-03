package ratelimit

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kiritosuki/GoVideo/internal/middleware/jwt"
	rediscache "github.com/kiritosuki/GoVideo/internal/middleware/redis"
)

// KeyFunc 获取key subject的函数
type KeyFunc func(ctx *gin.Context) (string, bool)

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
		allowed, _, err := cache.SlidingWindowAllow(c.Request.Context(), key, maxRequests, window)
		if err != nil {
			c.Next()
			return
		}
		if !allowed {
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

// KeyByIP 获取ip作为key subject
func KeyByIP(c *gin.Context) (string, bool) {
	ip := strings.TrimSpace(c.ClientIP())
	if ip == "" {
		return "", false
	}
	return ip, true
}

// KeyByAccount 获取accountID作为key subject
func KeyByAccount(c *gin.Context) (string, bool) {
	accountID, err := jwt.GetAccountID(c)
	if err != nil || accountID == 0 {
		return "", false
	}
	return strconv.FormatUint(uint64(accountID), 10), true
}
