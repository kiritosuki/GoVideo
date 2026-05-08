package jwt

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kiritosuki/GoVideo/internal/api/account"
	"github.com/kiritosuki/GoVideo/internal/auth"
	rediscache "github.com/kiritosuki/GoVideo/internal/middleware/redis"
)

// JWTAuth 返回验证jwt的中间件函数 用于业务函数之前
// 返回的函数用于校验token 如果合法则会把claims存入上下文并放行 如果不合法则会截断请求返回401
func JWTAuth(accountRepo *account.AccountRepo, cache *rediscache.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if header == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing authorization header"})
			return
		}
		parts := strings.SplitN(header, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid authorization header"})
			return
		}
		tokenStr := parts[1]
		claims, err := auth.ParseToken(tokenStr)
		if err != nil {
			c.AbortWithStatusPureJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}
		check(c, claims, tokenStr, accountRepo, cache)
	}
}

// SoftJWTAuth 返回验证jwt的中间件函数 用于业务函数之前
// 返回的函数用于校验token 允许token为空时放行 如果token不为空则会正常验证token
// token不为空时 如果合法则会把claims存入上下文并放行 如果不合法则会截断请求返回401
func SoftJWTAuth(accountRepo *account.AccountRepo, cache *rediscache.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if header == "" {
			c.Next()
			return
		}
		parts := strings.SplitN(header, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid authorization header"})
			return
		}
		tokenStr := parts[1]
		claims, err := auth.ParseToken(tokenStr)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}
		check(c, claims, tokenStr, accountRepo, cache)
	}
}

// check 实际查询redis/数据库 验证请求头中的token是否有效
func check(c *gin.Context, claims *auth.Claims, tokenStr string, accountRepo *account.AccountRepo, cache *rediscache.Client) {
	key := cache.Key("account:%d", claims.AccountID)
	// 先查redis
	if cache != nil {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 50*time.Millisecond)
		defer cancel()
		bytes, err := cache.GetBytes(ctx, key)
		if err == nil {
			if string(bytes) != tokenStr {
				// redis缓存的token与请求头token不匹配
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "token has been revoked"})
				return
			}
			// redis缓存的token与请求头token匹配
			// claims存入gin ctx上下文
			c.Set("accountID", claims.AccountID)
			c.Set("username", claims.Username)
			c.Next()
			return
		}
	}
	// redis故障/未启用 查db兜底
	accountInfo, err := accountRepo.FindByID(c.Request.Context(), claims.AccountID)
	if err != nil || accountInfo.Token == "" || accountInfo.Token != tokenStr {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "token has been revoked"})
		return
	}
	// 数据库存储的token与请求头token匹配
	// 把token加入到缓存
	if cache != nil {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 50*time.Millisecond)
		defer cancel()
		if err := cache.SetBytes(ctx, key, []byte(tokenStr), 24*time.Hour); err != nil {
			log.Printf("failed to set cache: %v\n", err)
		}
	}
	// claims存入gin ctx上下文
	c.Set("accountID", claims.AccountID)
	c.Set("username", claims.Username)
	c.Next()
}

// GetAccountID 从gin请求上下文中获取accountID
func GetAccountID(c *gin.Context) (uint, error) {
	uid, ok := c.Get("accountID")
	if !ok {
		return 0, errors.New("accountID not found")
	}
	accountID, ok := uid.(uint)
	if !ok {
		return 0, errors.New("accountID has invalid type")
	}
	return accountID, nil
}

// GetUsername 从gin请求上下文中获取username
func GetUsername(c *gin.Context) (string, error) {
	name, ok := c.Get("username")
	if !ok {
		return "", errors.New("username not found")
	}
	username, ok := name.(string)
	if !ok {
		return "", errors.New("username has invalid type")
	}
	return username, nil
}
