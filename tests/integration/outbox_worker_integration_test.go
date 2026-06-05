//go:build integration

package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kiritosuki/GoVideo/internal/api/video"
	"github.com/kiritosuki/GoVideo/internal/middleware/rabbitmq"
	"github.com/kiritosuki/GoVideo/internal/worker"
)

func TestOutboxWorkerPublishesPendingMessageAndMarksPublished(t *testing.T) {
	env := setupIntegration(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	author := env.createAccount(t, "outbox-worker-author")
	v := env.createVideo(t, author, "outbox-worker-video", time.Now())
	mqs := env.mqs(t)

	msg := &video.OutboxMsg{
		VideoID:    v.ID,
		EventType:  "video_published",
		CreateTime: v.CreateTime,
		Status:     video.OutboxStatusPending,
	}
	if err := env.db.WithContext(context.Background()).Create(msg).Error; err != nil {
		t.Fatalf("创建outbox消息失败: %v", err)
	}

	w := worker.NewOutboxWorker(env.db, mqs.timeline)
	go func() {
		_ = w.Run(ctx)
	}()

	waitUntil(t, 5*time.Second, func() bool {
		var got video.OutboxMsg
		env.db.First(&got, msg.ID)
		return got.Status == video.OutboxStatusPublished && got.PublishedAt != nil && got.LastError == ""
	})
	_ = consumeOne(t, env.newChannel(t), rabbitmq.TimelineQueue, 3*time.Second)
}

func TestOutboxWorkerRetriesAndMarksFailedWhenPublishFails(t *testing.T) {
	env := setupIntegration(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	author := env.createAccount(t, "outbox-fail-author")
	v := env.createVideo(t, author, "outbox-fail-video", time.Now())

	client, err := env.rmq.NewChannelClient()
	if err != nil {
		t.Fatalf("创建失败测试channel失败: %v", err)
	}
	timelineMQ, err := rabbitmq.NewTimelineMQ(client)
	if err != nil {
		t.Fatalf("创建TimelineMQ失败: %v", err)
	}
	// 关闭channel制造真实publish失败，验证outbox状态机会重试并最终failed。
	if err := client.CloseChannel(); err != nil {
		t.Fatalf("关闭失败测试channel失败: %v", err)
	}

	msg := &video.OutboxMsg{
		VideoID:    v.ID,
		EventType:  "video_published",
		CreateTime: v.CreateTime,
		Status:     video.OutboxStatusPending,
	}
	if err := env.db.WithContext(context.Background()).Create(msg).Error; err != nil {
		t.Fatalf("创建outbox失败消息失败: %v", err)
	}

	w := worker.NewOutboxWorker(env.db, timelineMQ)
	go func() {
		_ = w.Run(ctx)
	}()

	waitUntil(t, 8*time.Second, func() bool {
		var got video.OutboxMsg
		env.db.First(&got, msg.ID)
		return got.Status == video.OutboxStatusFailed && got.RetryCount == 4 && strings.TrimSpace(got.LastError) != ""
	})
}
