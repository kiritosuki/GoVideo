package worker

import (
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"gorm.io/gorm"
)

type Notification struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	RecipientID uint      `gorm:"index;not null" json:"recipient_id"`
	SenderID    uint      `gorm:"not null" json:"sender_id"`
	Type        string    `gorm:"type:varchar(50);not null" json:"type"`
	TargetID    uint      `json:"target_id"`
	Content     string    `gorm:"type:varchar(255)" json:"content"`
	IsRead      bool      `gorm:"default:false" json:"is_read"`
	CreatedAt   time.Time `gorm:"autoCreateTime" json:"created_at"`
}

type NotificationHub interface {
	Push(userID uint, n *Notification)
}

type NotificationWorker struct {
	ch    *amqp.Channel
	db    *gorm.DB
	queue string
	hub   NotificationHub
}

func NewNotificationWorker(ch *amqp.Channel, db *gorm.DB, queue string, hub NotificationHub) *NotificationWorker {
	return &NotificationWorker{
		ch:    ch,
		db:    db,
		queue: queue,
		hub:   hub,
	}
}

// TODO 这个最后再写 好像是旁路消费？
