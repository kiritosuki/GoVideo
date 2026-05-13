package http

import (
	"log"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kiritosuki/GoVideo/internal/api/account"
	"github.com/kiritosuki/GoVideo/internal/api/like"
	"github.com/kiritosuki/GoVideo/internal/api/video"
	"github.com/kiritosuki/GoVideo/internal/middleware/jwt"
	"github.com/kiritosuki/GoVideo/internal/middleware/rabbitmq"
	"github.com/kiritosuki/GoVideo/internal/middleware/ratelimit"
	rediscache "github.com/kiritosuki/GoVideo/internal/middleware/redis"
	"gorm.io/gorm"
)

// SetRouter
// TODO 参数后续需要加上MQ
func SetRouter(db *gorm.DB, cache *rediscache.Client, mq *rabbitmq.RabbitMQ) *gin.Engine {
	r := gin.Default()
	// 设置信任的ip 默认是信任所有ip
	// 对于信任的ip: 从header中获取clientIP
	// 对于不信任的ip: 根据连接目标的ip地址获取clientIP
	if err := r.SetTrustedProxies(nil); err != nil {
		log.Printf("setTrustedProxies failed: %v\n", err)
	}

	// 获取限流函数
	// 根据ip限流
	loginLimiter := ratelimit.Limit(cache, "account_login", 10, time.Minute, ratelimit.KeyByIP)    // 每分钟10次
	registerLimiter := ratelimit.Limit(cache, "account_register", 5, time.Hour, ratelimit.KeyByIP) // 每小时5次
	// 根据账号限流
	likeLimiter := ratelimit.Limit(cache, "like_write", 30, time.Minute, ratelimit.KeyByAccount) // 每分钟30次
	//commentLimiter := ratelimit.Limit(cache, "comment_write", 10, time.Minute, ratelimit.KeyByAccount) // 每分钟10次
	//socialLimiter := ratelimit.Limit(cache, "social_write", 20, time.Minute, ratelimit.KeyByAccount)   // 每分钟20次

	// account 路由
	// TODO /getProfile 用户主页(视频数/获赞/粉丝)
	accountRepo := account.NewAccountRepo(db)
	accountService := account.NewAccountService(accountRepo, cache)
	accountHandler := account.NewAccountHandler(accountService)
	accountGroup := r.Group("/account")
	{
		accountGroup.POST("/register", registerLimiter, accountHandler.CreateAccount)
		accountGroup.POST("/login", loginLimiter, accountHandler.Login)
		accountGroup.POST("/refresh", accountHandler.Refresh)
		accountGroup.POST("/changePassword", accountHandler.ChangePassword)
		accountGroup.POST("/findByID", accountHandler.FindByID)
		accountGroup.POST("/findByUsername", accountHandler.FindByUsername)
	}
	protectedAccountGroup := accountGroup.Group("")
	protectedAccountGroup.Use(jwt.JWTAuth(accountRepo, cache))
	{
		protectedAccountGroup.POST("/rename", accountHandler.Rename)
		protectedAccountGroup.POST("/logout", accountHandler.Logout)
		protectedAccountGroup.POST("/uploadAvatar", accountHandler.UploadAvatar)
		protectedAccountGroup.POST("/updateProfile", accountHandler.UpdateProfile)
	}

	// video 路由
	videoRepo := video.NewVideoRepo(db)
	videoService := video.NewVideoService(videoRepo, cache)
	videoHandler := video.NewVideoHandler(videoService)
	videoGroup := r.Group("/video")
	{
		videoGroup.POST("/listByAuthorID", videoHandler.ListByAuthorID)
		videoGroup.POST("/getDetail", videoHandler.GetDetail)
	}
	protectedVideoGroup := videoGroup.Group("")
	protectedVideoGroup.Use(jwt.JWTAuth(accountRepo, cache))
	{
		protectedVideoGroup.POST("/uploadVideo", videoHandler.UploadVideo)
		protectedVideoGroup.POST("/uploadCover", videoHandler.UploadCover)
		protectedVideoGroup.POST("/publish", videoHandler.PublishVideo)
	}

	// like 路由
	likeRepo := like.NewLikeRepo(db)
	likeMQ, err := rabbitmq.NewLikeMQ(mq)
	if err != nil {
		log.Printf("LikeMQ init failed (like_mq disabled): %v\n", err)
		likeMQ = nil
	}
	popularityMQ, err := rabbitmq.NewPopularityMQ(mq)
	if err != nil {
		log.Printf("PopularityMQ init failed (popularity_mq disabled): %v\n", err)
		popularityMQ = nil
	}
	likeService := like.NewLikeService(likeRepo, videoRepo, cache, likeMQ, popularityMQ)
	likeHandler := like.NewLikeHandler(likeService)
	likeGroup := r.Group("/like")
	protectedLikeGroup := likeGroup.Group("")
	protectedLikeGroup.Use(jwt.JWTAuth(accountRepo, cache))
	{
		protectedLikeGroup.POST("/like", likeLimiter, likeHandler.Like)
		protectedLikeGroup.POST("/unlike", likeLimiter, likeHandler.Unlike)
		protectedLikeGroup.POST("/isLiked", likeHandler.IsLiked)
		protectedLikeGroup.POST("/listMyLikedVideos", likeHandler.ListMyLikedVideos)
	}
	return r
}
