//go:build integration

package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/kiritosuki/GoVideo/internal/middleware/rabbitmq"
	"github.com/kiritosuki/GoVideo/internal/worker"
)

func TestTimelineWorkerWritesGlobalTimelineZSetIdempotently(t *testing.T) {
	env := setupIntegration(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mqs := env.mqs(t)

	w := worker.NewTimelineWorker(env.newChannel(t), env.cache, rabbitmq.TimelineQueue)
	go func() {
		_ = w.Run(ctx)
	}()

	createTime := time.Now()
	if err := mqs.timeline.Publish(context.Background(), 7001, createTime); err != nil {
		t.Fatalf("发布timeline消息失败: %v", err)
	}
	if err := mqs.timeline.Publish(context.Background(), 7001, createTime.Add(time.Second)); err != nil {
		t.Fatalf("发布重复timeline消息失败: %v", err)
	}

	key := env.cache.Key("feed:global_timeline")
	waitUntil(t, 5*time.Second, func() bool {
		members, err := env.cache.ZRevRange(context.Background(), key, 0, -1)
		return err == nil && len(members) == 1 && members[0] == "7001"
	})

	score, err := env.redisRaw.ZScore(context.Background(), key, "7001").Result()
	if err != nil {
		t.Fatalf("查询timeline score失败: %v", err)
	}
	if score <= 0 {
		t.Fatalf("timeline score应为事件时间戳, got=%f", score)
	}
}

func TestTimelineWorkerKeepsLatest1000(t *testing.T) {
	env := setupIntegration(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mqs := env.mqs(t)

	w := worker.NewTimelineWorker(env.newChannel(t), env.cache, rabbitmq.TimelineQueue)
	go func() {
		_ = w.Run(ctx)
	}()

	base := time.Now().Add(-2 * time.Hour)
	for i := 1; i <= 1005; i++ {
		if err := mqs.timeline.Publish(context.Background(), uint(i), base.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatalf("发布第%d条timeline消息失败: %v", i, err)
		}
	}
	key := env.cache.Key("feed:global_timeline")
	waitUntil(t, 10*time.Second, func() bool {
		n, err := env.redisRaw.ZCard(context.Background(), key).Result()
		return err == nil && n == 1000
	})
	if _, err := env.redisRaw.ZScore(context.Background(), key, "1").Result(); err == nil {
		t.Fatalf("最旧的视频应该被裁剪出timeline")
	}
	if _, err := env.redisRaw.ZScore(context.Background(), key, fmt.Sprintf("%d", 1005)).Result(); err != nil {
		t.Fatalf("最新的视频应该保留在timeline: %v", err)
	}
}
