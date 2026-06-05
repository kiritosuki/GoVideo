//go:build integration

package integration

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/kiritosuki/GoVideo/internal/api/notification"
	"github.com/kiritosuki/GoVideo/internal/middleware/rabbitmq"
	"github.com/kiritosuki/GoVideo/internal/worker"
)

type recordingHub struct {
	mu       sync.Mutex
	messages map[uint][]*notification.Notification
}

func newRecordingHub() *recordingHub {
	return &recordingHub{messages: make(map[uint][]*notification.Notification)}
}

func (h *recordingHub) Push(userID uint, n *notification.Notification) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.messages[userID] = append(h.messages[userID], n)
}

func (h *recordingHub) count(userID uint) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.messages[userID])
}

func TestNotificationWorkerWritesDBPublishesRedisAndDeduplicates(t *testing.T) {
	env := setupIntegration(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	author := env.createAccount(t, "notification-author")
	user := env.createAccount(t, "notification-user")
	v := env.createVideo(t, author, "notification-video", time.Now())
	hub := newRecordingHub()

	pubsub, err := env.cache.Subscribe(context.Background(), notification.PushChannel)
	if err != nil {
		t.Fatalf("订阅通知频道失败: %v", err)
	}
	defer pubsub.Close()
	if _, err := pubsub.Receive(context.Background()); err != nil {
		t.Fatalf("等待通知订阅建立失败: %v", err)
	}

	w := worker.NewNotificationWorker(env.newChannel(t), env.db, rabbitmq.NotificationLikeQueue, env.cache, hub)
	go func() {
		_ = w.Run(ctx)
	}()

	evt := rabbitmq.LikeEvent{EventID: "notification-like-event", Action: "like", UserID: user.ID, VideoID: v.ID, OccurredAt: time.Now()}
	if err := env.rmq.PublishJSON(context.Background(), rabbitmq.LikeExchange, rabbitmq.LikeRoutingKeyLike, evt); err != nil {
		t.Fatalf("发布点赞通知事件失败: %v", err)
	}
	if err := env.rmq.PublishJSON(context.Background(), rabbitmq.LikeExchange, rabbitmq.LikeRoutingKeyLike, evt); err != nil {
		t.Fatalf("发布重复点赞通知事件失败: %v", err)
	}

	waitUntil(t, 5*time.Second, func() bool {
		var count int64
		env.db.Model(&notification.Notification{}).Where("event_id = ?", evt.EventID).Count(&count)
		return count == 1
	})

	select {
	case msg := <-pubsub.Channel():
		if msg.Payload == "" {
			t.Fatalf("Redis通知消息payload不应为空")
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("通知worker写库后应发布Redis Pub/Sub消息")
	}
	if hub.count(author.ID) != 0 {
		t.Fatalf("Redis发布成功时不应走本地hub fallback")
	}
}

func TestNotificationWorkerFallbacksToLocalHubWhenRedisUnavailable(t *testing.T) {
	env := setupIntegration(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	author := env.createAccount(t, "notification-fallback-author")
	user := env.createAccount(t, "notification-fallback-user")
	v := env.createVideo(t, author, "notification-fallback-video", time.Now())
	hub := newRecordingHub()

	w := worker.NewNotificationWorker(env.newChannel(t), env.db, rabbitmq.NotificationLikeQueue, nil, hub)
	go func() {
		_ = w.Run(ctx)
	}()

	evt := rabbitmq.LikeEvent{EventID: "notification-fallback-event", Action: "like", UserID: user.ID, VideoID: v.ID, OccurredAt: time.Now()}
	if err := env.rmq.PublishJSON(context.Background(), rabbitmq.LikeExchange, rabbitmq.LikeRoutingKeyLike, evt); err != nil {
		t.Fatalf("发布点赞通知事件失败: %v", err)
	}
	waitUntil(t, 5*time.Second, func() bool {
		var count int64
		env.db.Model(&notification.Notification{}).Where("event_id = ?", evt.EventID).Count(&count)
		return count == 1 && hub.count(author.ID) == 1
	})
}

func TestNotificationWorkerIgnoresSelfAction(t *testing.T) {
	env := setupIntegration(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	author := env.createAccount(t, "notification-self-author")
	v := env.createVideo(t, author, "notification-self-video", time.Now())

	w := worker.NewNotificationWorker(env.newChannel(t), env.db, rabbitmq.NotificationLikeQueue, env.cache, newRecordingHub())
	go func() {
		_ = w.Run(ctx)
	}()

	evt := rabbitmq.LikeEvent{EventID: "notification-self-event", Action: "like", UserID: author.ID, VideoID: v.ID, OccurredAt: time.Now()}
	if err := env.rmq.PublishJSON(context.Background(), rabbitmq.LikeExchange, rabbitmq.LikeRoutingKeyLike, evt); err != nil {
		t.Fatalf("发布自己点赞事件失败: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	var count int64
	env.db.Model(&notification.Notification{}).Where("event_id = ?", evt.EventID).Count(&count)
	if count != 0 {
		t.Fatalf("自己点赞自己的视频不应该生成通知")
	}
}
