package redis

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// 测试PublishJSON发布的消息可以被Subscribe订阅收到
// 这里使用miniredis模拟Redis Pub/Sub 不依赖真实Redis服务
func TestPublishJSONAndSubscribe(t *testing.T) {
	client, _, cleanup := newMiniRedisClient(t)
	defer cleanup()

	ctx := context.Background()
	channel := client.Key("pubsub:test")
	pubsub, err := client.Subscribe(ctx, channel)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer pubsub.Close()

	// 先等待订阅建立 否则发布太快时消息可能早于订阅生效
	if _, err := pubsub.ReceiveTimeout(ctx, time.Second); err != nil {
		t.Fatalf("wait subscribe ready: %v", err)
	}

	payload := map[string]any{
		"id":   float64(1),
		"name": "alice",
	}
	if err := client.PublishJSON(ctx, channel, payload); err != nil {
		t.Fatalf("publish json: %v", err)
	}

	select {
	case msg := <-pubsub.Channel():
		var got map[string]any
		if err := json.Unmarshal([]byte(msg.Payload), &got); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		if got["id"] != payload["id"] || got["name"] != payload["name"] {
			t.Fatalf("unexpected payload: %#v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting pubsub message")
	}
}

// 测试Pub/Sub参数校验
// 这些分支用于保证Redis未初始化或频道为空时会返回明确错误
func TestPubSubValidation(t *testing.T) {
	var client *Client
	ctx := context.Background()

	if err := client.PublishJSON(ctx, "channel", map[string]string{"k": "v"}); err == nil {
		t.Fatal("expected nil client PublishJSON to return error")
	}
	if _, err := client.Subscribe(ctx, "channel"); err == nil {
		t.Fatal("expected nil client Subscribe to return error")
	}

	client, _, cleanup := newMiniRedisClient(t)
	defer cleanup()
	if err := client.PublishJSON(ctx, "", map[string]string{"k": "v"}); err == nil {
		t.Fatal("expected empty channel PublishJSON to return error")
	}
	if _, err := client.Subscribe(ctx); err == nil {
		t.Fatal("expected empty channel Subscribe to return error")
	}
}
