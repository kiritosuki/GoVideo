package rabbitmq

import "errors"

const (
	NotificationLikeQueue    = "notification.like"
	NotificationCommentQueue = "notification.comment"
	NotificationSocialQueue  = "notification.social"
)

type NotificationMQ struct {
	*RabbitMQ
}

// NewNotificationMQ 声明通知队列 并绑定到 点赞,评论,关注 事件交换机
// 本质上是给 RabbitMQ 创建三个新的queue 绑定到已有的三个交换机
func NewNotificationMQ(base *RabbitMQ) (*NotificationMQ, error) {
	if base == nil {
		return nil, errors.New("rabbitmq base is nil")
	}
	if err := base.DeclareTopic(LikeExchange, NotificationLikeQueue, LikeRoutingKeyLike); err != nil {
		return nil, err
	}
	if err := base.DeclareTopic(CommentExchange, NotificationCommentQueue, CommentRoutingKeyPublish); err != nil {
		return nil, err
	}
	if err := base.DeclareTopic(SocialExchange, NotificationSocialQueue, SocialRoutingKeyFollow); err != nil {
		return nil, err
	}
	return &NotificationMQ{
		RabbitMQ: base,
	}, nil
}
