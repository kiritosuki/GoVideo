package http

import (
	"log"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kiritosuki/GoVideo/internal/account"
	"github.com/kiritosuki/GoVideo/internal/middleware/ratelimit"
	rediscache "github.com/kiritosuki/GoVideo/internal/middleware/redis"
	"gorm.io/gorm"
)

// SetRouter
// TODO 参数后续需要加上MQ
func SetRouter(db *gorm.DB, cache *rediscache.Client) *gin.Engine {
	r := gin.Default()
	// 设置信任的ip 默认是信任所有ip
	// 对于信任的ip: 从header中获取clientIP
	// 对于不信任的ip: 根据连接目标的ip地址获取clientIP
	if err := r.SetTrustedProxies(nil); err != nil {
		log.Printf("setTrustedProxies failed: %v\n", err)
	}

	// 获取限流函数
	// 根据ip限流
	//loginLimiter := ratelimit.Limit(cache, "account_login", 10, time.Minute, ratelimit.KeyByIP)    // 每分钟10次
	registerLimiter := ratelimit.Limit(cache, "account_register", 5, time.Hour, ratelimit.KeyByIP) // 每小时5次
	// 根据账号限流
	//likeLimiter := ratelimit.Limit(cache, "like_write", 30, time.Minute, ratelimit.KeyByAccount)       // 每分钟30次
	//commentLimiter := ratelimit.Limit(cache, "comment_write", 10, time.Minute, ratelimit.KeyByAccount) // 每分钟10次
	//socialLimiter := ratelimit.Limit(cache, "social_write", 20, time.Minute, ratelimit.KeyByAccount)   // 每分钟20次

	// account 路由
	accountRepo := account.NewAccountRepo(db)
	accountService := account.NewAccountService(accountRepo, cache)
	accountHandler := account.NewAccountHandler(accountService)
	accountGroup := r.Group("/account")
	{
		accountGroup.POST("/register", registerLimiter, accountHandler.CreateAccount)
	}
	
	return r
}
