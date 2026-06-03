package worker

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/kiritosuki/GoVideo/internal/api/notification"
	"github.com/kiritosuki/GoVideo/internal/middleware/rabbitmq"
	amqp "github.com/rabbitmq/amqp091-go"
	"gorm.io/gorm"
)

type NotificationWorker struct {
	ch    *amqp.Channel
	db    *gorm.DB
	queue string
	hub   notification.NotificationHub
}

func NewNotificationWorker(ch *amqp.Channel, db *gorm.DB, queue string, hub notification.NotificationHub) *NotificationWorker {
	return &NotificationWorker{
		ch:    ch,
		db:    db,
		queue: queue,
		hub:   hub,
	}
}

func (w *NotificationWorker) Run(ctx context.Context) error {
	if w == nil || w.ch == nil || w.db == nil {
		return errors.New("notification worker is not initialized")
	}
	return consumeWithRetry(ctx, w.ch, w.queue, "notification_worker", func(ctx context.Context, d amqp.Delivery) error {
		return w.process(ctx, d)
	})
}

// process 回调函数 用于真正消费消息
func (w *NotificationWorker) process(ctx context.Context, d amqp.Delivery) error {
	body := d.Body
	if len(body) == 0 {
		return nil
	}
	routingKey := d.RoutingKey

	var notif *notification.Notification

	switch {
	case routingKey == "like.like":
		var evt rabbitmq.LikeEvent
		if err := json.Unmarshal(body, &evt); err != nil {
			return nil
		}
		if evt.UserID == 0 || evt.VideoID == 0 {
			return nil
		}
		var authorID uint
		w.db.WithContext(ctx).Model(&struct {
			ID       uint
			AuthorID uint
		}{}).Table("videos").Where("id = ?", evt.VideoID).Select("author_id").Scan(&authorID)
		if authorID == 0 || authorID == evt.UserID {
			return nil
		}
		notif = &notification.Notification{
			RecipientID: authorID,
			SenderID:    evt.UserID,
			Type:        "like",
			TargetID:    evt.VideoID,
			Content:     "点赞了你的视频",
		}

	case routingKey == "comment.publish":
		var evt rabbitmq.CommentEvent
		if err := json.Unmarshal(body, &evt); err != nil {
			return nil
		}
		if evt.AuthorID == 0 || evt.VideoID == 0 {
			return nil
		}
		var authorID uint
		w.db.WithContext(ctx).Model(&struct {
			ID       uint
			AuthorID uint
		}{}).Table("videos").Where("id = ?", evt.VideoID).Select("author_id").Scan(&authorID)
		if authorID == 0 || authorID == evt.AuthorID {
			return nil
		}
		notif = &notification.Notification{
			RecipientID: authorID,
			SenderID:    evt.AuthorID,
			Type:        "comment",
			TargetID:    evt.VideoID,
			Content:     "评论了你的视频",
		}

	case routingKey == "social.follow":
		var evt rabbitmq.SocialEvent
		if err := json.Unmarshal(body, &evt); err != nil {
			return nil
		}
		if evt.FollowerID == 0 || evt.VloggerID == 0 {
			return nil
		}
		notif = &notification.Notification{RecipientID: evt.VloggerID,
			SenderID: evt.FollowerID,
			Type:     "follow",
			TargetID: evt.FollowerID,
			Content:  "关注了你",
		}
	}

	if notif == nil {
		return nil
	}
	if err := w.db.WithContext(ctx).Create(notif).Error; err != nil {
		return err
	}
	if w.hub != nil {
		w.hub.Push(notif.RecipientID, notif)
	}
	return nil
}
