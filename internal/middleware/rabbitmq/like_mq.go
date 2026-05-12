package rabbitmq

import (
	"context"
	"errors"
	"time"

	"github.com/kiritosuki/GoVideo/internal/util"
)

const (
	LikeExchange         = "like.events"
	LikeQueue            = "like.events"
	LikeBindingKey       = "like.*"
	LikeRoutingKeyLike   = "like.like"
	LikeRoutingKeyUnlike = "like.unlike"
)

type LikeMQ struct {
	*RabbitMQ
}

type LikeEvent struct {
	EventID    string    `json:"event_id"`
	Action     string    `json:"action"`
	UserID     uint      `json:"user_id"`
	VideoID    uint      `json:"video_id"`
	OccurredAt time.Time `json:"occurred_at"`
}

// NewLikeMQ 创建 LikeMQ 本质上是给 RabbitMQ 创建新的exchange和queue
func NewLikeMQ(base *RabbitMQ) (*LikeMQ, error) {
	if base == nil {
		return nil, errors.New("rabbitmq base is nil")
	}
	if err := base.DeclareTopic(LikeExchange, LikeQueue, LikeBindingKey); err != nil {
		return nil, err
	}
	return &LikeMQ{
		RabbitMQ: base,
	}, nil
}

// Like 向消息队列里发送点赞消息
func (q *LikeMQ) Like(ctx context.Context, userID uint, videoID uint) error {
	return q.publish(ctx, "like", LikeRoutingKeyLike, userID, videoID)
}

// Unlike 向消息队列里发送取消点赞消息
func (q *LikeMQ) Unlike(ctx context.Context, userID uint, videoID uint) error {
	return q.publish(ctx, "unlike", LikeRoutingKeyUnlike, userID, videoID)
}

// publish 向消息队列里发送消息
func (q *LikeMQ) publish(ctx context.Context, action string, routingKey string, userID uint, videoID uint) error {
	if q == nil || q.RabbitMQ == nil {
		return errors.New("like_mq is not initialized")
	}
	if userID == 0 || videoID == 0 {
		return errors.New("userID and videoID are required")
	}
	id, err := util.RandHex(16)
	if err != nil {
		return err
	}
	event := LikeEvent{
		EventID:    id,
		Action:     action,
		UserID:     userID,
		VideoID:    videoID,
		OccurredAt: time.Now(),
	}
	return q.PublishJSON(ctx, LikeExchange, routingKey, event)
}
