package rabbitmq

import (
	"context"
	"errors"
	"time"

	"github.com/kiritosuki/GoVideo/internal/util"
)

const (
	SocialExchange           = "social.events"
	SocialQueue              = "social.events"
	SocialBindingKey         = "social.*"
	SocialRoutingKeyFollow   = "social.follow"
	SocialRoutingKeyUnFollow = "social.unfollow"
)

type SocialMQ struct {
	*RabbitMQ
}

type SocialEvent struct {
	EventID    string    `json:"event_id"`
	Action     string    `json:"action"`
	FollowerID uint      `json:"follower_id"`
	VloggerID  uint      `json:"vlogger_id"`
	OccurredAt time.Time `json:"occurred_at"`
}

// NewSocialMQ 创建 SocialMQ 本质上是给 RabbitMQ 创建新的exchange和queue
func NewSocialMQ(base *RabbitMQ) (*SocialMQ, error) {
	if base == nil {
		return nil, errors.New("rabbitmq base is nil")
	}
	if err := base.DeclareTopic(SocialExchange, SocialQueue, SocialBindingKey); err != nil {
		return nil, err
	}
	return &SocialMQ{
		RabbitMQ: base,
	}, nil
}

// Follow 向消息队列里发送关注消息
func (q *SocialMQ) Follow(ctx context.Context, followerID uint, vloggerID uint) error {
	return q.publish(ctx, "follow", SocialRoutingKeyFollow, followerID, vloggerID)
}

// Unfollow 向消息队列里发送取消关注消息
func (q *SocialMQ) Unfollow(ctx context.Context, followerID uint, vloggerID uint) error {
	return q.publish(ctx, "unfollow", SocialRoutingKeyUnFollow, followerID, vloggerID)
}

// publish 向消息队列里发送消息
func (q *SocialMQ) publish(ctx context.Context, action string, routingKey string, followerID uint, vloggerID uint) error {
	if q == nil || q.RabbitMQ == nil {
		return errors.New("social_mq is not initialized")
	}
	if followerID == 0 || vloggerID == 0 {
		return errors.New("followerID and vloggerID are required")
	}
	id, err := util.RandHex(16)
	if err != nil {
		return err
	}
	event := SocialEvent{
		EventID:    id,
		Action:     action,
		FollowerID: followerID,
		VloggerID:  vloggerID,
		OccurredAt: time.Now().UTC(),
	}
	return q.PublishJSON(ctx, SocialExchange, routingKey, event)
}
