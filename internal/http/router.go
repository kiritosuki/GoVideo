package http

import (
	"context"
	"log"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kiritosuki/GoVideo/internal/api/account"
	"github.com/kiritosuki/GoVideo/internal/api/comment"
	"github.com/kiritosuki/GoVideo/internal/api/feed"
	"github.com/kiritosuki/GoVideo/internal/api/like"
	"github.com/kiritosuki/GoVideo/internal/api/message"
	"github.com/kiritosuki/GoVideo/internal/api/profile"
	"github.com/kiritosuki/GoVideo/internal/api/social"
	"github.com/kiritosuki/GoVideo/internal/api/video"
	"github.com/kiritosuki/GoVideo/internal/middleware/jwt"
	"github.com/kiritosuki/GoVideo/internal/middleware/rabbitmq"
	"github.com/kiritosuki/GoVideo/internal/middleware/ratelimit"
	rediscache "github.com/kiritosuki/GoVideo/internal/middleware/redis"
	"github.com/kiritosuki/GoVideo/internal/worker"
	"gorm.io/gorm"
)

// SetRouter 配置全局路由
func SetRouter(db *gorm.DB, cache *rediscache.Client, rmq *rabbitmq.RabbitMQ) *gin.Engine {
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
	likeLimiter := ratelimit.Limit(cache, "like_write", 30, time.Minute, ratelimit.KeyByAccount)       // 每分钟30次
	commentLimiter := ratelimit.Limit(cache, "comment_write", 10, time.Minute, ratelimit.KeyByAccount) // 每分钟10次
	socialLimiter := ratelimit.Limit(cache, "social_write", 20, time.Minute, ratelimit.KeyByAccount)   // 每分钟20次

	// account 路由
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
	likeClient, err := rmq.NewChannelClient()
	if err != nil {
		log.Printf("LikeMQ channel init failed (like_mq disabled): %v\n", err)
	}
	likeMQ, err := rabbitmq.NewLikeMQ(likeClient)
	if err != nil {
		log.Printf("LikeMQ init failed (like_mq disabled): %v\n", err)
		likeMQ = nil
	}
	popularityClient, err := rmq.NewChannelClient()
	if err != nil {
		log.Printf("PopularityMQ channel init failed (popularity_mq disabled): %v\n", err)
	}
	popularityMQ, err := rabbitmq.NewPopularityMQ(popularityClient)
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

	// comment路由
	commentRepo := comment.NewCommentRepo(db)
	commentClient, err := rmq.NewChannelClient()
	if err != nil {
		log.Printf("CommentMQ channel init failed (comment_mq disabled): %v\n", err)
	}
	commentMQ, err := rabbitmq.NewCommentMQ(commentClient)
	if err != nil {
		log.Printf("CommentMQ init failed (comment_mq disabled): %v\n", err)
		commentMQ = nil
	}
	commentService := comment.NewCommentService(commentRepo, videoRepo, cache, commentMQ, popularityMQ)
	commentHandler := comment.NewCommentHandler(commentService, accountService)
	commentGroup := r.Group("/comment")
	{
		commentGroup.POST("/listAll", commentHandler.ListAllComments)
	}
	protectedCommentGroup := commentGroup.Group("")
	protectedCommentGroup.Use(jwt.JWTAuth(accountRepo, cache))
	{
		protectedCommentGroup.POST("/publish", commentLimiter, commentHandler.PublishComment)
		protectedCommentGroup.POST("/delete", commentLimiter, commentHandler.DeleteComment)
	}

	// social路由
	socialRepo := social.NewSocialRepo(db)
	socialClient, err := rmq.NewChannelClient()
	if err != nil {
		log.Printf("SocialMQ channel init failed (social_mq disabled): %v\n", err)
	}
	socialMQ, err := rabbitmq.NewSocialMQ(socialClient)
	if err != nil {
		log.Printf("SocialMQ init failed (social_mq disabled): %v\n", err)
		socialMQ = nil
	}
	socialService := social.NewSocialService(socialRepo, accountRepo, socialMQ)
	socialHandler := social.NewSocialHandler(socialService)
	socialGroup := r.Group("/social")
	protectedSocialGroup := socialGroup.Group("")
	protectedSocialGroup.Use(jwt.JWTAuth(accountRepo, cache))
	{
		protectedSocialGroup.POST("/follow", socialLimiter, socialHandler.Follow)
		protectedSocialGroup.POST("/unfollow", socialLimiter, socialHandler.Unfollow)
		protectedSocialGroup.POST("/listAllFollowers", socialHandler.ListAllFollowers)
		protectedSocialGroup.POST("/listAllVloggers", socialHandler.ListAllVloggers)
		protectedSocialGroup.POST("/getCounts", socialHandler.GetCounts)
	}

	// feed路由
	feedRepo := feed.NewFeedRepo(db)
	feedService := feed.NewFeedService(feedRepo, likeRepo, cache)
	feedHandler := feed.NewFeedHandler(feedService)
	feedGroup := r.Group("/feed")
	protectedFeedGroup := feedGroup.Group("")
	protectedFeedGroup.Use(jwt.JWTAuth(accountRepo, cache))
	{
		protectedFeedGroup.POST("/listByFollowing", feedHandler.ListByFollowing)
	}
	softFeedGroup := feedGroup.Group("")
	softFeedGroup.Use(jwt.SoftJWTAuth(accountRepo, cache))
	{
		softFeedGroup.POST("/listLatest", feedHandler.ListLatest)
		softFeedGroup.POST("/listLikesCount", feedHandler.ListLikesCount)
		softFeedGroup.POST("/listByPopularity", feedHandler.ListByPopularity)
		softFeedGroup.POST("/listByTag", feedHandler.ListByTag)
	}

	// message路由
	messageRepo := message.NewMessageRepo(db)
	messageService := message.NewMessageService(messageRepo)
	messageHandler := message.NewMessageHandler(messageService)
	messageGroup := r.Group("/message")
	protectedMessageGroup := messageGroup.Group("")
	protectedMessageGroup.Use(jwt.JWTAuth(accountRepo, cache))
	{
		protectedMessageGroup.POST("/send", messageHandler.Send)
		protectedMessageGroup.POST("/list", messageHandler.List)
	}

	// profile路由
	profileService := profile.NewProfileService(accountRepo, videoRepo, socialRepo, cache)
	profileHandler := profile.NewProfileHandler(profileService)
	profileGroup := r.Group("/profile")
	{
		profileGroup.POST("/getAccountProfile", profileHandler.GetAccountProfile)
	}

	// SSE notification
	// notification_mq
	notificationClient, err := rmq.NewChannelClient()
	if err != nil {
		log.Printf("NotificationMQ channel init failed (notification_mq disabled): %v\n", err)
	}
	if _, err := rabbitmq.NewNotificationMQ(notificationClient); err != nil {
		log.Printf("NotificationMQ init failed (notification_mq disabled): %v\n", err)
		notificationClient = nil
	}
	// sseHub
	sseHub := worker.NewSSEHub(db)
	notifGroup := r.Group("/notification")
	notifGroup.Use(sseHub.SSERequireAuth()) // SSE鉴权 token可放在query中
	{
		notifGroup.GET("/stream", sseHub.SSEHandler)
		notifGroup.POST("/list", sseHub.ListHandler)
		notifGroup.POST("/markRead", sseHub.MarkReadHandler)
		notifGroup.POST("/unreadCount", sseHub.UnreadCountHandler)
	}

	worker.StartNotificationWorkers(context.Background(), notificationClient, db, sseHub)

	return r
}
