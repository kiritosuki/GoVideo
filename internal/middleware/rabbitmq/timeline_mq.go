package rabbitmq

import (
	"context"
	"errors"
	"time"

	"github.com/kiritosuki/GoVideo/internal/util"
)

const (
	TimelineExchange          = "video.timeline.events"
	TimelineQueue             = "video.timeline.events"
	TimelineBindingKey        = "video.timeline.*"
	TimelineRoutingKeyPublish = "video.timeline.publish"
)

type TimelineMQ struct {
	*RabbitMQ
}

type TimelineEvent struct {
	EventID    string    `json:"event_id"`
	Action     string    `json:"action"`
	VideoID    uint      `json:"video_id"`
	CreateTime int64     `json:"create_time"`
	OccurredAt time.Time `json:"occurred_at"`
}

// NewTimelineMQ 创建 TimelineMQ 本质上是给 RabbitMQ 创建新的exchange和queue
func NewTimelineMQ(base *RabbitMQ) (*TimelineMQ, error) {
	if base == nil {
		return nil, errors.New("rabbitmq base is nil")
	}
	if err := base.DeclareTopic(TimelineExchange, TimelineQueue, TimelineBindingKey); err != nil {
		return nil, err
	}
	return &TimelineMQ{
		RabbitMQ: base,
	}, nil
}

// Publish 向消息队列里发送“有视频发布”的消息
func (q *TimelineMQ) Publish(ctx context.Context, videoID uint, createTime time.Time) error {
	return q.publish(ctx, "publish", TimelineRoutingKeyPublish, videoID, createTime.UnixMilli())
}

// publish 向消息队列里发送消息
func (q *TimelineMQ) publish(ctx context.Context, action string, routingKey string, videoID uint, createTime int64) error {
	if q == nil || q.RabbitMQ == nil {
		return errors.New("timeline_mq is not initialized")
	}
	if videoID == 0 {
		return errors.New("videoID is required")
	}
	id, err := util.RandHex(16)
	if err != nil {
		return err
	}
	event := TimelineEvent{
		EventID:    id,
		Action:     action,
		VideoID:    videoID,
		CreateTime: createTime,
		OccurredAt: time.Now(),
	}
	return q.PublishJSON(ctx, TimelineExchange, routingKey, event)
}
