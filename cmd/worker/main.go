package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kiritosuki/GoVideo/internal/api/comment"
	"github.com/kiritosuki/GoVideo/internal/api/like"
	"github.com/kiritosuki/GoVideo/internal/api/social"
	"github.com/kiritosuki/GoVideo/internal/api/video"
	"github.com/kiritosuki/GoVideo/internal/config"
	"github.com/kiritosuki/GoVideo/internal/db"
	"github.com/kiritosuki/GoVideo/internal/middleware/rabbitmq"
	rediscache "github.com/kiritosuki/GoVideo/internal/middleware/redis"
	"github.com/kiritosuki/GoVideo/internal/observability"
	"github.com/kiritosuki/GoVideo/internal/worker"
	"gorm.io/gorm"
)

func connectWithRetry(name string, maxRetries int, fn func() error) {
	for i := 0; i < maxRetries; i++ {
		if err := fn(); err == nil {
			return
		}
		wait := time.Duration(1<<i) * time.Second
		if wait > 30*time.Second {
			wait = 30 * time.Second
		}
		log.Printf("%s 不可用，%v 后重试 (%d/%d)...", name, wait, i+1, maxRetries)
		time.Sleep(wait)
	}
	log.Fatalf("%s: 超过最大重试次数", name)
}

func main() {
	// 加载配置
	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "configs/config.yaml"
	}
	log.Printf("Loading config from %s", configPath)
	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}
	// 连接数据库（带重试）
	var sqlDB *gorm.DB
	connectWithRetry("MySQL", 10, func() error {
		var err error
		sqlDB, err = db.NewDB(cfg.Database)
		return err
	})
	defer db.CloseDB(sqlDB)

	// 连接 Redis（用于流行度更新）
	cache, err := rediscache.NewClient(&cfg.Redis)
	if err != nil {
		log.Printf("Redis config error (popularity worker disabled): %v", err)
		cache = nil
	} else {
		pingCtx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		if err := cache.Ping(pingCtx); err != nil {
			log.Printf("Redis not available (popularity worker disabled): %v", err)
			_ = cache.Close()
			cache = nil
		} else {
			defer cache.Close()
			log.Printf("Redis connected (popularity worker enabled)")
		}
	}
	// 连接 RabbitMQ（带重试）并通过统一封装声明拓扑
	var rmq *rabbitmq.RabbitMQ
	connectWithRetry("RabbitMQ", 10, func() error {
		var err error
		rmq, err = rabbitmq.NewRabbitMQ(&cfg.RabbitMQ)
		return err
	})
	defer rmq.Close()

	if _, err := rabbitmq.NewSocialMQ(rmq); err != nil {
		log.Fatalf("Failed to declare social topology: %v", err)
	}
	if _, err := rabbitmq.NewLikeMQ(rmq); err != nil {
		log.Fatalf("Failed to declare like topology: %v", err)
	}
	if _, err := rabbitmq.NewCommentMQ(rmq); err != nil {
		log.Fatalf("Failed to declare comment topology: %v", err)
	}
	if cache != nil {
		if _, err := rabbitmq.NewPopularityMQ(rmq); err != nil {
			log.Fatalf("Failed to declare popularity topology: %v", err)
		}
	}
	// 限制Ch最多同时消费50条未ack的消息
	if err := rmq.Ch.Qos(50, 0, false); err != nil {
		log.Fatalf("Failed to set qos: %v", err)
	}

	socialRepo := social.NewSocialRepo(sqlDB)
	socialWorker := worker.NewSocialWorker(rmq.Ch, socialRepo, rabbitmq.SocialQueue)
	videoRepo := video.NewVideoRepo(sqlDB)
	likeRepo := like.NewLikeRepo(sqlDB)
	commentRepo := comment.NewCommentRepo(sqlDB)
	likeWorker := worker.NewLikeWorker(rmq.Ch, likeRepo, videoRepo, rabbitmq.LikeQueue)
	commentWorker := worker.NewCommentWorker(rmq.Ch, commentRepo, videoRepo, rabbitmq.CommentQueue)
	var popularityWorker *worker.PopularityWorker
	if cache != nil {
		popularityWorker = worker.NewPopularityWorker(rmq.Ch, cache, rabbitmq.PopularityQueue)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pprofServer, err := observability.NewPprofServer(
		"Worker",
		cfg.Observability.Pprof.Enabled,
		cfg.Observability.Pprof.WorkerAddr,
	)
	if err != nil {
		log.Printf("Failed to start worker pprof server: %v", err)
	}
	if pprofServer != nil {
		defer pprofServer.Close()
	}

	errCh := make(chan error, 4)
	log.Printf("Worker started, consuming queue=%s", rabbitmq.SocialQueue)
	go func() {
		errCh <- socialWorker.Run(ctx)
	}()
	log.Printf("Worker started, consuming queue=%s", rabbitmq.LikeQueue)
	go func() {
		errCh <- likeWorker.Run(ctx)
	}()
	log.Printf("Worker started, consuming queue=%s", rabbitmq.CommentQueue)
	go func() {
		errCh <- commentWorker.Run(ctx)
	}()
	if popularityWorker != nil {
		log.Printf("Worker started, consuming queue=%s", rabbitmq.PopularityQueue)
		go func() {
			errCh <- popularityWorker.Run(ctx)
		}()
	}

	err = <-errCh
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("Worker stopped: %v", err)
	}
	log.Printf("Worker stopped")
}
