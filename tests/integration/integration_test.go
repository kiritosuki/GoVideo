//go:build integration

package integration

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/kiritosuki/GoVideo/internal/auth"
	"github.com/kiritosuki/GoVideo/internal/config"
	appdb "github.com/kiritosuki/GoVideo/internal/db"
	"github.com/kiritosuki/GoVideo/internal/middleware/rabbitmq"
	rediscache "github.com/kiritosuki/GoVideo/internal/middleware/redis"
	amqp "github.com/rabbitmq/amqp091-go"
	goredis "github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

type integrationEnv struct {
	cfg      config.Config
	db       *gorm.DB
	cache    *rediscache.Client
	redisRaw *goredis.Client
	rmq      *rabbitmq.RabbitMQ
}

// setupIntegration 初始化真实 MySQL、Redis、RabbitMQ。
// 集成测试依赖外部中间件，因此必须通过 CONFIG_PATH 指向测试配置。
func setupIntegration(t *testing.T) *integrationEnv {
	t.Helper()

	path := os.Getenv("CONFIG_PATH")
	if path == "" {
		path = "configs/config-integration.yaml"
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("加载集成测试配置失败: %v", err)
	}
	if err := auth.SetJWTSecret(cfg.JWT.JWTSecret); err != nil {
		t.Fatalf("初始化JWT密钥失败: %v", err)
	}

	db, err := appdb.NewDB(cfg.Database)
	if err != nil {
		t.Fatalf("连接MySQL失败: %v", err)
	}
	if err := appdb.AutoMigrate(db); err != nil {
		t.Fatalf("自动建表失败: %v", err)
	}

	cache, err := rediscache.NewClient(&cfg.Redis)
	if err != nil {
		t.Fatalf("创建Redis客户端失败: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := cache.Ping(ctx); err != nil {
		t.Fatalf("连接Redis失败: %v", err)
	}
	redisRaw := goredis.NewClient(&goredis.Options{
		Addr:     fmt.Sprintf("%s:%d", cfg.Redis.Host, cfg.Redis.Port),
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})

	rmq, err := rabbitmq.NewRabbitMQ(&cfg.RabbitMQ)
	if err != nil {
		t.Fatalf("连接RabbitMQ失败: %v", err)
	}

	env := &integrationEnv{
		cfg:      cfg,
		db:       db,
		cache:    cache,
		redisRaw: redisRaw,
		rmq:      rmq,
	}
	env.prepareRabbitMQ(t)
	env.cleanAll(t)

	t.Cleanup(func() {
		env.cleanAll(t)
		_ = env.redisRaw.Close()
		_ = env.cache.Close()
		_ = env.rmq.Close()
		_ = appdb.CloseDB(env.db)
	})
	return env
}

// cleanAll 清空每个测试产生的数据，避免测试之间互相污染。
func (e *integrationEnv) cleanAll(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := e.redisRaw.FlushDB(ctx).Err(); err != nil {
		t.Fatalf("清空Redis失败: %v", err)
	}
	e.purgeKnownQueues(t)

	tables := []string{
		"notifications",
		"messages",
		"socials",
		"comments",
		"likes",
		"video_tags",
		"tags",
		"outbox_msgs",
		"videos",
		"accounts",
	}
	for _, table := range tables {
		if err := e.db.Exec("DELETE FROM " + table).Error; err != nil {
			t.Fatalf("清空表 %s 失败: %v", table, err)
		}
	}
}

// prepareRabbitMQ 声明业务MQ拓扑。队列不存在时直接 purge 会失败，因此先统一声明。
func (e *integrationEnv) prepareRabbitMQ(t *testing.T) {
	t.Helper()
	if _, err := rabbitmq.NewLikeMQ(e.rmq); err != nil {
		t.Fatalf("声明LikeMQ失败: %v", err)
	}
	if _, err := rabbitmq.NewCommentMQ(e.rmq); err != nil {
		t.Fatalf("声明CommentMQ失败: %v", err)
	}
	if _, err := rabbitmq.NewSocialMQ(e.rmq); err != nil {
		t.Fatalf("声明SocialMQ失败: %v", err)
	}
	if _, err := rabbitmq.NewPopularityMQ(e.rmq); err != nil {
		t.Fatalf("声明PopularityMQ失败: %v", err)
	}
	if _, err := rabbitmq.NewTimelineMQ(e.rmq); err != nil {
		t.Fatalf("声明TimelineMQ失败: %v", err)
	}
	if _, err := rabbitmq.NewNotificationMQ(e.rmq); err != nil {
		t.Fatalf("声明NotificationMQ失败: %v", err)
	}
}

func (e *integrationEnv) purgeKnownQueues(t *testing.T) {
	t.Helper()
	queues := []string{
		rabbitmq.LikeQueue, rabbitmq.LikeQueue + ".dlx",
		rabbitmq.CommentQueue, rabbitmq.CommentQueue + ".dlx",
		rabbitmq.SocialQueue, rabbitmq.SocialQueue + ".dlx",
		rabbitmq.PopularityQueue, rabbitmq.PopularityQueue + ".dlx",
		rabbitmq.TimelineQueue, rabbitmq.TimelineQueue + ".dlx",
		rabbitmq.NotificationLikeQueue, rabbitmq.NotificationLikeQueue + ".dlx",
		rabbitmq.NotificationCommentQueue, rabbitmq.NotificationCommentQueue + ".dlx",
		rabbitmq.NotificationSocialQueue, rabbitmq.NotificationSocialQueue + ".dlx",
	}
	for _, queue := range queues {
		_, _ = e.rmq.Ch.QueuePurge(queue, false)
	}
}

func (e *integrationEnv) newChannel(t *testing.T) *amqp.Channel {
	t.Helper()
	client, err := e.rmq.NewChannelClient()
	if err != nil {
		t.Fatalf("创建独立RabbitMQ channel失败: %v", err)
	}
	t.Cleanup(func() {
		_ = client.CloseChannel()
	})
	return client.Ch
}

// waitUntil 等待异步worker完成状态变更。
func waitUntil(t *testing.T, timeout time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("等待条件在 %s 内未满足", timeout)
}
