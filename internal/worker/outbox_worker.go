package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/kiritosuki/GoVideo/internal/api/video"
	"github.com/kiritosuki/GoVideo/internal/middleware/rabbitmq"
	"github.com/kiritosuki/GoVideo/internal/middleware/redis"
	amqp "github.com/rabbitmq/amqp091-go"
	oredis "github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// StartOutboxPoller 开启常驻协程 定时扫描OutboxMsg的pending消息 投入消息队列
func StartOutboxPoller(db *gorm.DB, tmq *rabbitmq.TimelineMQ) {
	if db == nil || tmq == nil || tmq.RabbitMQ == nil || tmq.Ch == nil {
		log.Printf("Outbox poller disabled: timeline mq is not initialized")
		return
	}

	go func() {
		for {
			// 定时扫描数据库 找出处于"pending"状态的待办msg
			var messages []video.OutboxMsg
			err := db.Where("status = ?", "pending").Order("create_time ASC").Limit(100).Find(&messages).Error

			if err != nil || len(messages) == 0 {
				time.Sleep(1 * time.Second)
				continue
			}

			for _, msg := range messages {
				// 把创建视频的消息发送到消息队列
				err := tmq.Publish(context.Background(), msg.VideoID, msg.CreateTime)
				if err == nil {
					// 发送成功才会从数据库移除
					db.Delete(&msg)
				} else {
					log.Printf("投递MQ失败: VideoID: %d, err: %v", msg.VideoID, err)
				}
			}
		}
	}()
}

// StartConsumer 开启常驻协程 消费timeline队列中的消息
func StartConsumer(tmq *rabbitmq.TimelineMQ, queueName string, redisClient *redis.Client) {
	if tmq == nil || tmq.RabbitMQ == nil || tmq.Ch == nil {
		log.Printf("Timeline consumer disabled: timeline mq is not initialized")
		return
	}
	if redisClient == nil {
		log.Printf("Timeline consumer disabled: redis is not initialized")
		return
	}

	go func() {
		if err := consumeWithRetry(context.Background(), tmq.Ch, queueName, "timeline_worker", func(ctx context.Context, d amqp.Delivery) error {
			var event rabbitmq.TimelineEvent
			if err := json.Unmarshal(d.Body, &event); err != nil {
				log.Printf("反序列化失败\n")
				// 直接丢弃消息
				return nil
			}

			opCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
			defer cancel()

			timelineKey := redisClient.Key("feed:global_timeline")
			// 把视频id加入时间榜zset
			if err := redisClient.ZAdd(opCtx, timelineKey, oredis.Z{
				Score:  float64(event.CreateTime),
				Member: fmt.Sprintf("%d", event.VideoID),
			}); err != nil {
				log.Printf("写入Zset失败\n")
				return err
			}
			// 删除旧视频 只保留最新的1000条
			if err := redisClient.ZRemRangeByRank(opCtx, timelineKey, 0, -1001); err != nil {
				log.Printf("ZRem失败\n")
			}
			return nil
		}); err != nil {
			log.Printf("timeline consumer stopped: %v", err)
		}
	}()
}
