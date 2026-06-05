//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/kiritosuki/GoVideo/internal/middleware/rabbitmq"
	"github.com/kiritosuki/GoVideo/internal/worker"
)

func TestRabbitMQRetryAndDLX(t *testing.T) {
	env := setupIntegration(t)

	// 使用真实TimelineWorker做黑盒测试:
	// 关闭Redis后，worker写ZSET必然失败，从而触发consumeWithRetry里的重试和DLX逻辑。
	if err := env.cache.Close(); err != nil {
		t.Fatalf("关闭Redis client失败: %v", err)
	}
	timelineMQ, err := rabbitmq.NewTimelineMQ(env.rmq)
	if err != nil {
		t.Fatalf("创建TimelineMQ失败: %v", err)
	}
	if err := timelineMQ.Publish(context.Background(), 9001, time.Now()); err != nil {
		t.Fatalf("发布timeline消息失败: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := env.newChannel(t)
	w := worker.NewTimelineWorker(ch, env.cache, rabbitmq.TimelineQueue)
	go func() {
		_ = w.Run(ctx)
	}()

	dlxDelivery := consumeOne(t, env.newChannel(t), rabbitmq.TimelineQueue+".dlx", 8*time.Second)
	if len(dlxDelivery.Body) == 0 {
		t.Fatalf("死信队列中应保留原始消息")
	}
}
