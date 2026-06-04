package worker

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/kiritosuki/GoVideo/internal/api/video"
	"github.com/kiritosuki/GoVideo/internal/middleware/rabbitmq"
	rediscache "github.com/kiritosuki/GoVideo/internal/middleware/redis"
	amqp "github.com/rabbitmq/amqp091-go"
)

type PopularityWorker struct {
	ch    *amqp.Channel
	cache *rediscache.Client
	queue string
}

func NewPopularityWorker(ch *amqp.Channel, cache *rediscache.Client, queue string) *PopularityWorker {
	return &PopularityWorker{
		ch:    ch,
		cache: cache,
		queue: queue,
	}
}

func (w *PopularityWorker) Run(ctx context.Context) error {
	if w == nil || w.ch == nil || w.cache == nil {
		return errors.New("popularity worker is not initialized")
	}
	if w.queue == "" {
		return errors.New("queue is required")
	}

	return consumeWithRetry(ctx, w.ch, w.queue, "popularity", func(ctx context.Context, d amqp.Delivery) error {
		return w.process(ctx, d.Body)
	})
}

// process 回调函数 用于真正消费消息
// TODO 可选优化: 当前未实现消费幂等性保证
// TODO 热度榜不要求严格幂等 消息被重复消费的概率较低 热度变化较小 可以容忍
func (w *PopularityWorker) process(ctx context.Context, body []byte) error {
	var evt rabbitmq.PopularityEvent
	if err := json.Unmarshal(body, &evt); err != nil {
		return nil
	}
	if evt.VideoID == 0 || evt.Change == 0 {
		return nil
	}
	video.UpdatePopularityCache(ctx, w.cache, evt.VideoID, evt.Change)
	return nil
}
