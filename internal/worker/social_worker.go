package worker

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/go-sql-driver/mysql"
	"github.com/kiritosuki/GoVideo/internal/api/social"
	"github.com/kiritosuki/GoVideo/internal/middleware/rabbitmq"
	amqp "github.com/rabbitmq/amqp091-go"
)

type SocialWorker struct {
	ch         *amqp.Channel
	socialRepo *social.SocialRepo
	queue      string
}

func NewSocialWorker(ch *amqp.Channel, socialRepo *social.SocialRepo, queue string) *SocialWorker {
	return &SocialWorker{
		ch:         ch,
		socialRepo: socialRepo,
		queue:      queue,
	}
}

func (w *SocialWorker) Run(ctx context.Context) error {
	if w == nil || w.ch == nil || w.socialRepo == nil {
		return errors.New("social worker is not initialized")
	}
	if w.queue == "" {
		return errors.New("queue is required")
	}

	return consumeWithRetry(ctx, w.ch, w.queue, "social", func(ctx context.Context, d amqp.Delivery) error {
		return w.process(ctx, d.Body)
	})
}

// process 回调函数 用于真正消费消息
func (w *SocialWorker) process(ctx context.Context, body []byte) error {
	var evt rabbitmq.SocialEvent
	if err := json.Unmarshal(body, &evt); err != nil {
		// 解析事件失败，直接丢弃
		return nil
	}
	if evt.FollowerID == 0 || evt.VloggerID == 0 {
		return nil
	}
	switch evt.Action {
	case "follow":
		return w.applyFollow(ctx, &evt)
	case "unfollow":
		return w.applyUnfollow(ctx, &evt)
	default:
		return nil
	}
}

// applyFollow 消费关注消息
func (w *SocialWorker) applyFollow(ctx context.Context, evt *rabbitmq.SocialEvent) error {
	err := w.socialRepo.Follow(ctx, &social.Social{
		FollowerID: evt.FollowerID,
		VloggerID:  evt.VloggerID,
	})
	if err == nil {
		return nil
	}
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
		return nil
	}
	return err
}

// applyUnfollow 消费取消关注消息
func (w *SocialWorker) applyUnfollow(ctx context.Context, evt *rabbitmq.SocialEvent) error {
	return w.socialRepo.Unfollow(ctx, &social.Social{
		FollowerID: evt.FollowerID,
		VloggerID:  evt.VloggerID,
	})
}
