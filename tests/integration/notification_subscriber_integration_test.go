//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/kiritosuki/GoVideo/internal/api/notification"
	"github.com/kiritosuki/GoVideo/internal/worker"
)

func TestNotificationSubscriberPushesToLocalHub(t *testing.T) {
	env := setupIntegration(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hub := newRecordingHub()

	worker.StartNotificationSubscriber(ctx, env.cache, hub)
	msg := notification.PushMessage{
		RecipientID: 42,
		Notification: notification.Notification{
			EventID:     "subscriber-event",
			RecipientID: 42,
			SenderID:    7,
			Type:        "follow",
			TargetID:    7,
		},
	}
	if err := env.cache.PublishJSON(context.Background(), notification.PushChannel, msg); err != nil {
		t.Fatalf("发布订阅测试消息失败: %v", err)
	}
	waitUntil(t, 3*time.Second, func() bool {
		return hub.count(42) == 1
	})
}
