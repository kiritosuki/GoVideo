package rabbitmq

import (
	"context"
	"errors"
	"time"

	"github.com/kiritosuki/GoVideo/internal/util"
)

const (
	CommentExchange          = "comment.events"
	CommentQueue             = "comment.events"
	CommentBindingKey        = "comment.*"
	CommentRoutingKeyPublish = "comment.publish"
	CommentRoutingKeyDelete  = "comment.delete"
)

type CommentMQ struct {
	*RabbitMQ
}

type CommentEvent struct {
	EventID    string    `json:"event_id"`
	Action     string    `json:"action"`
	CommentID  uint      `json:"comment_id,omitempty"`
	Username   string    `json:"username,omitempty"`
	VideoID    uint      `json:"video_id,omitempty"`
	AuthorID   uint      `json:"author_id,omitempty"`
	Content    string    `json:"content,omitempty"`
	OccurredAt time.Time `json:"occurred_at"`
}

// NewCommentMQ 创建 CommentMQ 本质上是给 RabbitMQ 创建新的exchange和queue
func NewCommentMQ(base *RabbitMQ) (*CommentMQ, error) {
	if base == nil {
		return nil, errors.New("rabbitmq base is nil")
	}
	if err := base.DeclareTopic(CommentExchange, CommentQueue, CommentBindingKey); err != nil {
		return nil, err
	}
	return &CommentMQ{
		RabbitMQ: base,
	}, nil
}

// Publish 向消息队列里发送发布评论消息
func (q *CommentMQ) Publish(ctx context.Context, username string, videoID uint, authorID uint, content string) error {
	return q.publish(ctx, "publish", CommentRoutingKeyPublish, CommentEvent{
		Username: username,
		VideoID:  videoID,
		AuthorID: authorID,
		Content:  content,
	})
}

// Delete 向消息队列里发送删除评论消息
func (q *CommentMQ) Delete(ctx context.Context, commentID uint) error {
	return q.publish(ctx, "delete", CommentRoutingKeyDelete, CommentEvent{
		CommentID: commentID,
	})
}

// publish 向消息队列里发送消息
func (q *CommentMQ) publish(ctx context.Context, action string, routingKey string, evt CommentEvent) error {
	if q == nil || q.RabbitMQ == nil {
		return errors.New("comment_mq is not initialized")
	}
	id, err := util.RandHex(16)
	if err != nil {
		return err
	}
	evt.EventID = id
	evt.Action = action
	evt.OccurredAt = time.Now().UTC()
	return q.PublishJSON(ctx, CommentExchange, routingKey, evt)
}
