package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/kiritosuki/GoVideo/internal/middleware/rabbitmq"
	rediscache "github.com/kiritosuki/GoVideo/internal/middleware/redis"
	amqp "github.com/rabbitmq/amqp091-go"
	redis "github.com/redis/go-redis/v9"
)

type TimelineWorker struct {
	ch    *amqp.Channel
	queue string
	cache *rediscache.Client
}

func NewTimelineWorker(ch *amqp.Channel, cache *rediscache.Client, queue string) *TimelineWorker {
	return &TimelineWorker{
		ch:    ch,
		cache: cache,
		queue: queue,
	}
}

func (w *TimelineWorker) Run(ctx context.Context) error {
	if w == nil || w.ch == nil || w.cache == nil {
		return errors.New("timelineMQ worker is not initialized")
	}
	if w.queue == "" {
		return errors.New("queue is required")
	}
	return consumeWithRetry(ctx, w.ch, w.queue, "timeline_worker", func(ctx context.Context, d amqp.Delivery) error {
		return w.process(ctx, d.Body)
	})
}

// process 回调函数 用于真正消费消息
func (w *TimelineWorker) process(ctx context.Context, body []byte) error {
	var evt rabbitmq.TimelineEvent
	if err := json.Unmarshal(body, &evt); err != nil {
		log.Printf("反序列化失败\n")
		// 直接丢弃消息
		return nil
	}
	if evt.VideoID == 0 || evt.CreateTime == 0 {
		return nil
	}

	opCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()

	timelineKey := w.cache.Key("feed:global_timeline")
	// 把视频id加入时间榜zset
	if err := w.cache.ZAdd(opCtx, timelineKey, redis.Z{
		Score:  float64(evt.CreateTime),
		Member: fmt.Sprintf("%d", evt.VideoID),
	}); err != nil {
		log.Printf("写入Zset失败")
		return err
	}
	// 删除旧视频 只保留最新的1000条
	if err := w.cache.ZRemRangeByRank(opCtx, timelineKey, 0, -1001); err != nil {
		log.Printf("ZRem失败\n")
	}
	return nil
}
