package rabbitmq

import (
	"context"
	"errors"
	"time"

	"github.com/kiritosuki/GoVideo/internal/util"
)

const (
	PopularityExchange         = "video.popularity.events"
	PopularityQueue            = "video.popularity.events"
	PopularityBindingKey       = "video.popularity.*"
	PopularityRoutingKeyUpdate = "video.popularity.update"
)

type PopularityMQ struct {
	*RabbitMQ
}

type PopularityEvent struct {
	EventID    string    `json:"event_id"`
	Action     string    `json:"action"`
	VideoID    uint      `json:"video_id"`
	Change     int64     `json:"change"`
	OccurredAt time.Time `json:"occurred_at"`
}

// NewPopularityMQ 创建 PopularityMQ 本质上是给 RabbitMQ 创建新的exchange和queue
func NewPopularityMQ(base *RabbitMQ) (*PopularityMQ, error) {
	if base == nil {
		return nil, errors.New("rabbitmq base is nil")
	}
	if err := base.DeclareTopic(PopularityExchange, PopularityQueue, PopularityBindingKey); err != nil {
		return nil, err
	}
	return &PopularityMQ{
		RabbitMQ: base,
	}, nil
}

// Update 向消息队列里发送更新视频热度缓存消息
func (q *PopularityMQ) Update(ctx context.Context, videoID uint, change int64) error {
	return q.publish(ctx, "update", PopularityRoutingKeyUpdate, videoID, change)
}

// publish 向消息队列里发送消息
func (q *PopularityMQ) publish(ctx context.Context, action string, routingKey string, videoID uint, change int64) error {
	if q == nil || q.RabbitMQ == nil {
		return errors.New("popularity_mq is not initialized")
	}
	if videoID == 0 || change == 0 {
		return errors.New("videoID and change are required")
	}
	id, err := util.RandHex(16)
	if err != nil {
		return err
	}
	event := PopularityEvent{
		EventID:    id,
		Action:     action,
		VideoID:    videoID,
		Change:     change,
		OccurredAt: time.Now().UTC(),
	}
	return q.PublishJSON(ctx, PopularityExchange, routingKey, event)
}
