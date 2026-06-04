package worker

import (
	"context"
	"encoding/json"
	"errors"
	"log"

	"github.com/go-sql-driver/mysql"
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
	// 构造通知消息
	notif, err := w.buildNotification(ctx, d)
	if err != nil {
		return err
	}
	if notif == nil {
		return nil
	}
	// 根据event_id保证消费幂等 重复消息直接丢弃并ack
	if err := w.db.WithContext(ctx).Create(notif).Error; err != nil {
		var mysqlErr *mysql.MySQLError
		if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
			return nil
		}
		return err
	}
	if w.hub != nil {
		// 推送通知
		w.hub.Push(notif.RecipientID, notif)
	}
	return nil
}

// buildNotification 构造通知消息
func (w *NotificationWorker) buildNotification(ctx context.Context, d amqp.Delivery) (*notification.Notification, error) {
	if len(d.Body) == 0 {
		return nil, nil
	}
	switch d.RoutingKey {
	case rabbitmq.LikeRoutingKeyLike:
		return w.buildLikeNotification(ctx, d.Body)
	case rabbitmq.CommentRoutingKeyPublish:
		return w.buildCommentNotification(ctx, d.Body)
	case rabbitmq.SocialRoutingKeyFollow:
		return w.buildFollowNotification(d.Body)
	default:
		return nil, nil
	}
}

func (w *NotificationWorker) buildLikeNotification(ctx context.Context, body []byte) (*notification.Notification, error) {
	var evt rabbitmq.LikeEvent
	if err := json.Unmarshal(body, &evt); err != nil {
		return nil, nil
	}
	if evt.EventID == "" || evt.UserID == 0 || evt.VideoID == 0 {
		return nil, nil
	}
	// 获取作者id
	authorID, err := w.getVideoAuthorID(ctx, evt.VideoID)
	if err != nil {
		return nil, err
	}
	if authorID == 0 || authorID == evt.UserID {
		return nil, nil
	}
	return &notification.Notification{
		EventID:     evt.EventID,
		RecipientID: authorID,
		SenderID:    evt.UserID,
		Type:        "like",
		TargetID:    evt.VideoID,
		Content:     "点赞了你的视频",
	}, nil
}

func (w *NotificationWorker) buildCommentNotification(ctx context.Context, body []byte) (*notification.Notification, error) {
	var evt rabbitmq.CommentEvent
	if err := json.Unmarshal(body, &evt); err != nil {
		return nil, nil
	}
	if evt.EventID == "" || evt.AuthorID == 0 || evt.VideoID == 0 {
		return nil, nil
	}
	// 获取作者id
	authorID, err := w.getVideoAuthorID(ctx, evt.VideoID)
	if err != nil {
		return nil, err
	}
	if authorID == 0 || authorID == evt.AuthorID {
		return nil, nil
	}
	return &notification.Notification{
		EventID:     evt.EventID,
		RecipientID: authorID,
		SenderID:    evt.AuthorID,
		Type:        "comment",
		TargetID:    evt.VideoID,
		Content:     "评论了你的视频",
	}, nil
}

func (w *NotificationWorker) buildFollowNotification(body []byte) (*notification.Notification, error) {
	var evt rabbitmq.SocialEvent
	if err := json.Unmarshal(body, &evt); err != nil {
		return nil, nil
	}
	if evt.EventID == "" || evt.FollowerID == 0 || evt.VloggerID == 0 {
		return nil, nil
	}
	return &notification.Notification{
		EventID:     evt.EventID,
		RecipientID: evt.VloggerID,
		SenderID:    evt.FollowerID,
		Type:        "follow",
		TargetID:    evt.FollowerID,
		Content:     "关注了你",
	}, nil
}

func (w *NotificationWorker) getVideoAuthorID(ctx context.Context, videoID uint) (uint, error) {
	var authorID uint
	err := w.db.WithContext(ctx).Model(&struct {
		ID       uint
		AuthorID uint
	}{}).Table("videos").Where("id = ?", videoID).Select("author_id").Scan(&authorID).Error
	return authorID, err
}

// StartNotificationWorkers 启动通知相关的所有消费者
func StartNotificationWorkers(ctx context.Context, mq *rabbitmq.RabbitMQ, db *gorm.DB, hub notification.NotificationHub) {
	if mq == nil || mq.Ch == nil {
		log.Printf("Notification workers disabled: rabbitmq is not initialized")
		return
	}
	if db == nil {
		log.Printf("Notification workers disabled: db is not initialized")
		return
	}
	workers := []struct {
		name  string
		queue string
	}{
		{name: "notification-like", queue: rabbitmq.NotificationLikeQueue},
		{name: "notification-comment", queue: rabbitmq.NotificationCommentQueue},
		{name: "notification-social", queue: rabbitmq.NotificationSocialQueue},
	}
	for _, item := range workers {
		item := item
		go func() {
			w := NewNotificationWorker(mq.Ch, db, item.queue, hub)
			if err := w.Run(ctx); err != nil {
				log.Printf("%s worker: %v", item.name, err)
			}
		}()
	}
}
