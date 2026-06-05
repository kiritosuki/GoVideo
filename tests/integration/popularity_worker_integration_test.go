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

func TestPopularityWorkerUpdatesMinuteHotZSet(t *testing.T) {
	env := setupIntegration(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mqs := env.mqs(t)

	w := worker.NewPopularityWorker(env.newChannel(t), env.cache, rabbitmq.PopularityQueue)
	go func() {
		_ = w.Run(ctx)
	}()

	if err := mqs.popularity.Update(context.Background(), 8001, 3); err != nil {
		t.Fatalf("发布热度增加消息失败: %v", err)
	}
	if err := mqs.popularity.Update(context.Background(), 8001, -1); err != nil {
		t.Fatalf("发布热度减少消息失败: %v", err)
	}

	// 热度窗口key由worker按当前分钟生成，因此通过通配符查找测试产生的窗口。
	waitUntil(t, 5*time.Second, func() bool {
		keys, err := env.redisRaw.Keys(context.Background(), env.cache.Key("hot:video:1m:*")).Result()
		if err != nil || len(keys) == 0 {
			return false
		}
		for _, key := range keys {
			score, err := env.redisRaw.ZScore(context.Background(), key, fmt.Sprintf("%d", 8001)).Result()
			if err == nil && score == 2 {
				return true
			}
		}
		return false
	})
}
