package bootstrap

import (
	"context"
	"log"

	"github.com/kiritosuki/GoVideo/internal/api/notification"
	"github.com/kiritosuki/GoVideo/internal/middleware/rabbitmq"
	"github.com/kiritosuki/GoVideo/internal/worker"
	"gorm.io/gorm"
)

// StartNotification 启动通知相关的MQ拓扑声明和后台消费者
func StartNotification(ctx context.Context, rmq *rabbitmq.RabbitMQ, db *gorm.DB, hub notification.NotificationHub) {
	// notification_channel
	notificationClient, err := rmq.NewChannelClient()
	if err != nil {
		log.Printf("NotificationMQ channel init failed (notification_mq disabled): %v\n", err)
		return
	}
	// notification_mq
	if _, err := rabbitmq.NewNotificationMQ(notificationClient); err != nil {
		log.Printf("NotificationMQ init failed (notification_mq disabled): %v\n", err)
		return
	}
	// 启动notification的所有worker
	worker.StartNotificationWorkers(ctx, notificationClient, db, hub)
}
