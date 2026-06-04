package worker

import (
	"context"
	"encoding/json"
	"log"

	"github.com/kiritosuki/GoVideo/internal/api/notification"
	rediscache "github.com/kiritosuki/GoVideo/internal/middleware/redis"
)

// StartNotificationSubscriber 订阅Redis频道 并开启协程接收信息 把接收到的信息推送到本节点的SSEHub
func StartNotificationSubscriber(ctx context.Context, cache *rediscache.Client, hub notification.NotificationHub) {
	if cache == nil {
		log.Printf("Notification subscriber disabled: redis is not initialized")
		return
	}
	if hub == nil {
		log.Printf("Notification subscriber disabled: notification hub is not initialized")
		return
	}
	pubsub, err := cache.Subscribe(ctx, notification.PushChannel)
	if err != nil {
		log.Printf("Notification subscriber disabled: %v", err)
		return
	}
	go func() {
		defer pubsub.Close()
		ch := pubsub.Channel()
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-ch:
				if !ok {
					return
				}
				var pushMsg notification.PushMessage
				if err := json.Unmarshal([]byte(msg.Payload), &pushMsg); err != nil {
					log.Printf("notification subscriber unmarshal failed: %v", err)
					continue
				}
				if pushMsg.RecipientID == 0 {
					continue
				}
				hub.Push(pushMsg.RecipientID, &pushMsg.Notification)
			}
		}
	}()
}
