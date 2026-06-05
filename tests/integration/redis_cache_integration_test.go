//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/kiritosuki/GoVideo/internal/api/notification"
	rediscache "github.com/kiritosuki/GoVideo/internal/middleware/redis"
	goredis "github.com/redis/go-redis/v9"
)

func TestRedisCacheBytesAndMGet(t *testing.T) {
	env := setupIntegration(t)
	ctx := context.Background()

	key1 := env.cache.Key("it:cache:1")
	key2 := env.cache.Key("it:cache:2")
	missing := env.cache.Key("it:cache:missing")

	// 基础字节缓存用于验证业务中 JSON 缓存的底层读写能力。
	if err := env.cache.SetBytes(ctx, key1, []byte("one"), time.Minute); err != nil {
		t.Fatalf("写入key1失败: %v", err)
	}
	if err := env.cache.SetBytes(ctx, key2, []byte("two"), time.Minute); err != nil {
		t.Fatalf("写入key2失败: %v", err)
	}
	got, err := env.cache.GetBytes(ctx, key1)
	if err != nil || string(got) != "one" {
		t.Fatalf("读取key1异常, got=%q err=%v", string(got), err)
	}

	// MGet 的返回顺序必须与请求 key 顺序一致，feed 多级缓存依赖这个性质。
	values, err := env.cache.MGet(ctx, key2, missing, key1)
	if err != nil {
		t.Fatalf("MGet失败: %v", err)
	}
	if values[0] != "two" || values[1] != nil || values[2] != "one" {
		t.Fatalf("MGet顺序或miss结果异常: %#v", values)
	}

	if _, err := env.cache.GetBytes(ctx, missing); !rediscache.IsMiss(err) {
		t.Fatalf("不存在的key应返回redis miss, got: %v", err)
	}
}

func TestRedisZSetAndUnion(t *testing.T) {
	env := setupIntegration(t)
	ctx := context.Background()

	k1 := env.cache.Key("it:zset:1")
	k2 := env.cache.Key("it:zset:2")
	dest := env.cache.Key("it:zset:merge")

	// 热榜和时间线都依赖 ZSET，这里验证分值排序、并集合并和范围删除。
	if err := env.cache.ZAdd(ctx, k1, goredis.Z{Score: 10, Member: "1"}, goredis.Z{Score: 20, Member: "2"}); err != nil {
		t.Fatalf("ZAdd k1失败: %v", err)
	}
	if err := env.cache.ZAdd(ctx, k2, goredis.Z{Score: 5, Member: "1"}, goredis.Z{Score: 30, Member: "3"}); err != nil {
		t.Fatalf("ZAdd k2失败: %v", err)
	}
	if err := env.cache.ZUnionStore(ctx, dest, []string{k1, k2}, "SUM"); err != nil {
		t.Fatalf("ZUnionStore失败: %v", err)
	}
	members, err := env.cache.ZRevRange(ctx, dest, 0, 2)
	if err != nil {
		t.Fatalf("ZRevRange失败: %v", err)
	}
	if len(members) != 3 || members[0] != "3" || members[1] != "2" || members[2] != "1" {
		t.Fatalf("合并后热榜排序异常: %#v", members)
	}

	if err := env.cache.ZRemRangeByRank(ctx, dest, 0, 0); err != nil {
		t.Fatalf("ZRemRangeByRank失败: %v", err)
	}
	members, err = env.cache.ZRevRange(ctx, dest, 0, -1)
	if err != nil {
		t.Fatalf("删除后查询ZSET失败: %v", err)
	}
	if len(members) != 2 || members[0] != "2" || members[1] != "1" {
		t.Fatalf("ZSET范围删除结果异常: %#v", members)
	}
}

func TestRedisPubSub(t *testing.T) {
	env := setupIntegration(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	pubsub, err := env.cache.Subscribe(ctx, notification.PushChannel)
	if err != nil {
		t.Fatalf("订阅Redis频道失败: %v", err)
	}
	defer pubsub.Close()

	msg := notification.PushMessage{
		RecipientID: 7,
		Notification: notification.Notification{
			EventID:     "pubsub-event",
			RecipientID: 7,
			SenderID:    8,
			Type:        "like",
			TargetID:    9,
		},
	}
	if _, err := pubsub.Receive(ctx); err != nil {
		t.Fatalf("等待订阅建立失败: %v", err)
	}
	if err := env.cache.PublishJSON(ctx, notification.PushChannel, msg); err != nil {
		t.Fatalf("发布Redis消息失败: %v", err)
	}

	select {
	case got := <-pubsub.Channel():
		var decoded notification.PushMessage
		if err := json.Unmarshal([]byte(got.Payload), &decoded); err != nil {
			t.Fatalf("反序列化PubSub消息失败: %v", err)
		}
		if decoded.RecipientID != msg.RecipientID || decoded.Notification.EventID != msg.Notification.EventID {
			t.Fatalf("PubSub消息内容异常: %+v", decoded)
		}
	case <-ctx.Done():
		t.Fatalf("超时未收到PubSub消息")
	}
}
