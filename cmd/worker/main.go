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

const WorkerPrefetchCount = 50

type workerError struct {
	name string
	err  error
}

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

func newWorkerClient(rmq *rabbitmq.RabbitMQ, name string, consumer bool) (*rabbitmq.RabbitMQ, error) {
	client, err := rmq.NewChannelClient()
	if err != nil {
		return nil, err
	}
	if consumer {
		// 限制消费者最多提前读取WorkerPrefetchCount条未ack的消息
		// 没处理完消息队列不会再多推送
		if err := client.Ch.Qos(WorkerPrefetchCount, 0, false); err != nil {
			_ = client.CloseChannel()
			return nil, err
		}
	}
	log.Printf("RabbitMQ channel created for %s", name)
	return client, nil
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

	// 创建repo
	socialRepo := social.NewSocialRepo(sqlDB)
	videoRepo := video.NewVideoRepo(sqlDB)
	likeRepo := like.NewLikeRepo(sqlDB)
	commentRepo := comment.NewCommentRepo(sqlDB)

	// 创建常驻worker
	// social_channel
	socialClient, err := newWorkerClient(rmq, "social_worker", true)
	if err != nil {
		log.Fatalf("Failed to create social worker channel: %v", err)
	}
	defer socialClient.CloseChannel()
	if _, err := rabbitmq.NewSocialMQ(socialClient); err != nil {
		log.Fatalf("Failed to declare social topology: %v", err)
	}
	// social_worker
	socialWorker := worker.NewSocialWorker(socialClient.Ch, socialRepo, rabbitmq.SocialQueue)

	// like_channel
	likeClient, err := newWorkerClient(rmq, "like_worker", true)
	if err != nil {
		log.Fatalf("Failed to create like worker channel: %v", err)
	}
	defer likeClient.CloseChannel()
	if _, err := rabbitmq.NewLikeMQ(likeClient); err != nil {
		log.Fatalf("Failed to declare like topology: %v", err)
	}
	// like_worker
	likeWorker := worker.NewLikeWorker(likeClient.Ch, likeRepo, videoRepo, rabbitmq.LikeQueue)

	// comment_channel
	commentClient, err := newWorkerClient(rmq, "comment_worker", true)
	if err != nil {
		log.Fatalf("Failed to create comment worker channel: %v", err)
	}
	defer commentClient.CloseChannel()
	if _, err := rabbitmq.NewCommentMQ(commentClient); err != nil {
		log.Fatalf("Failed to declare comment topology: %v", err)
	}
	// comment_worker
	commentWorker := worker.NewCommentWorker(commentClient.Ch, commentRepo, videoRepo, rabbitmq.CommentQueue)

	// outbox_channel
	outboxClient, err := newWorkerClient(rmq, "outbox_worker", false)
	if err != nil {
		log.Fatalf("Failed to create outbox worker channel: %v", err)
	}
	defer outboxClient.CloseChannel()
	timelineMQ, err := rabbitmq.NewTimelineMQ(outboxClient)
	if err != nil {
		log.Fatalf("Failed to declare timeline topology: %v", err)
	}
	// outbox_worker
	// 此worker不是消费者 而是向消息队列推送的任务
	outboxWorker := worker.NewOutboxWorker(sqlDB, timelineMQ)

	// 创建仅在redis可用时的worker
	var popularityWorker *worker.PopularityWorker
	if cache != nil {
		popularityClient, err := newWorkerClient(rmq, "popularity_worker", true)
		if err != nil {
			log.Fatalf("Failed to create popularity worker channel: %v", err)
		}
		defer popularityClient.CloseChannel()
		if _, err := rabbitmq.NewPopularityMQ(popularityClient); err != nil {
			log.Fatalf("Failed to declare popularity topology: %v", err)
		}
		popularityWorker = worker.NewPopularityWorker(popularityClient.Ch, cache, rabbitmq.PopularityQueue)
	}
	var timelineWorker *worker.TimelineWorker
	if cache != nil {
		timelineClient, err := newWorkerClient(rmq, "timeline_worker", true)
		if err != nil {
			log.Fatalf("Failed to create timeline worker channel: %v", err)
		}
		defer timelineClient.CloseChannel()
		timelineWorker = worker.NewTimelineWorker(timelineClient.Ch, cache, rabbitmq.TimelineQueue)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 创建pprofServer
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

	errCh := make(chan workerError, 6)
	startWorker := func(name string, run func(context.Context) error) {
		go func() {
			errCh <- workerError{name: name, err: run(ctx)}
		}()
	}

	// 启动常驻worker
	log.Printf("Worker started, consuming queue=%s", rabbitmq.SocialQueue)
	startWorker("social_worker", socialWorker.Run)
	log.Printf("Worker started, consuming queue=%s", rabbitmq.LikeQueue)
	startWorker("like_worker", likeWorker.Run)
	log.Printf("Worker started, consuming queue=%s", rabbitmq.CommentQueue)
	startWorker("comment_worker", commentWorker.Run)
	log.Printf("Worker started, polling outbox")
	startWorker("outbox_worker", outboxWorker.Run)

	// 启动仅在redis可用时的worker
	if popularityWorker != nil {
		log.Printf("Worker started, consuming queue=%s", rabbitmq.PopularityQueue)
		startWorker("popularity_worker", popularityWorker.Run)
	} else {
		log.Printf("Popularity worker disabled: redis is not initialized")
	}
	if timelineWorker != nil {
		log.Printf("Worker started, consuming queue=%s", rabbitmq.TimelineQueue)
		startWorker("timeline_worker", timelineWorker.Run)
	} else {
		log.Printf("Timeline worker disabled: redis is not initialized")
	}

	workerErr := <-errCh
	if workerErr.err != nil && !errors.Is(workerErr.err, context.Canceled) {
		// 非ctx取消的err 为异常退出
		log.Fatalf("%s stopped error: %v", workerErr.name, workerErr.err)
	}
	// ctx取消的err 为正常退出
	log.Printf("%s stopped", workerErr.name)
}
