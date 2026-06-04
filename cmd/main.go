package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/kiritosuki/GoVideo/internal/config"
	"github.com/kiritosuki/GoVideo/internal/db"
	apphttp "github.com/kiritosuki/GoVideo/internal/http"
	"github.com/kiritosuki/GoVideo/internal/middleware/rabbitmq"
	rediscache "github.com/kiritosuki/GoVideo/internal/middleware/redis"
	"github.com/kiritosuki/GoVideo/internal/observability"
	"github.com/kiritosuki/GoVideo/internal/worker"
)

func main() {
	// 加载config文件配置
	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		// 默认配置路径
		configPath = "configs/config-dev.yaml"
	}
	log.Printf("loading config from %s", configPath)
	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalln("load config failed! please set correct CONFIG_PATH")
	}

	// 连接数据库
	gormDB, err := db.NewDB(cfg.Database)
	if err != nil {
		log.Fatalf("failed to connect database: %v\n", err)
	}
	// 自动建表
	if err = db.AutoMigrate(gormDB); err != nil {
		log.Fatalf("failed to auto migrate database: %v\n", err)
	}
	defer db.CloseDB(gormDB)

	// 连接redis (可选 用于缓存)
	cache, err := rediscache.NewClient(&cfg.Redis)
	if err != nil {
		log.Printf("redis config error (cache disabled): %v\n", err)
		cache = nil
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		if err := cache.Ping(ctx); err != nil {
			log.Printf("redis not available (cache disabled): %v\n", err)
			cache.Close()
			cache = nil
		} else {
			defer cache.Close()
			log.Printf("redis connected (cache enabled)\n")
		}
	}

	// 连接rabbitmq (可选 用于消息队列)
	rmq, err := rabbitmq.NewRabbitMQ(&cfg.RabbitMQ)
	if err != nil {
		log.Printf("RabbitMQ config error (disabled): %v\n", err)
		rmq = nil
	} else {
		defer rmq.Close()
		log.Printf("RabbitMQ connected\n")
	}

	// 启动Pprof服务
	pprofServer, err := observability.NewPprofServer(
		"API",
		cfg.Observability.Pprof.Enabled,
		cfg.Observability.Pprof.ApiAddr,
	)
	if err != nil {
		log.Printf("failed to start API pprof server: %v", err)
	}
	if pprofServer != nil {
		defer pprofServer.Close()
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 设置路由并启动服务
	r, notificationHub := apphttp.SetRouter(gormDB, cache, rmq)
	// 启动消息通知推送服务
	worker.StartNotification(ctx, rmq, gormDB, cache, notificationHub)
	log.Printf("server is running on port %d\n", cfg.Server.Port)

	if err = r.Run(":" + strconv.Itoa(cfg.Server.Port)); err != nil {
		log.Fatalf("failed to run server: %v", err)
	}
}
