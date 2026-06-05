package redis

import (
	"context"
	"testing"
	"time"
)

// 测试SetBytes和GetBytes可以正常写入和读取缓存
// 这里使用miniredis作为内存Redis 不依赖真实Redis服务
func TestCacheSetAndGetBytes(t *testing.T) {
	client, _, cleanup := newMiniRedisClient(t)
	defer cleanup()

	ctx := context.Background()
	key := client.Key("cache:test")
	value := []byte("hello")

	if err := client.SetBytes(ctx, key, value, time.Minute); err != nil {
		t.Fatalf("set bytes: %v", err)
	}
	got, err := client.GetBytes(ctx, key)
	if err != nil {
		t.Fatalf("get bytes: %v", err)
	}
	if string(got) != string(value) {
		t.Fatalf("expected %q, got %q", value, got)
	}
}

// 测试Delete会删除指定缓存
// 删除后再次读取应当返回redis miss错误
func TestCacheDelete(t *testing.T) {
	client, _, cleanup := newMiniRedisClient(t)
	defer cleanup()

	ctx := context.Background()
	key := client.Key("cache:delete")
	if err := client.SetBytes(ctx, key, []byte("value"), time.Minute); err != nil {
		t.Fatalf("set bytes: %v", err)
	}
	if err := client.Delete(ctx, key); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := client.GetBytes(ctx, key); !IsMiss(err) {
		t.Fatalf("expected redis miss after delete, got %v", err)
	}
}

// 测试MGet可以批量读取多个key
// 对不存在的key Redis会返回nil 这符合go-redis的行为
func TestCacheMGet(t *testing.T) {
	client, _, cleanup := newMiniRedisClient(t)
	defer cleanup()

	ctx := context.Background()
	key1 := client.Key("cache:mget:1")
	key2 := client.Key("cache:mget:2")
	key3 := client.Key("cache:mget:missing")
	if err := client.SetBytes(ctx, key1, []byte("one"), time.Minute); err != nil {
		t.Fatalf("set key1: %v", err)
	}
	if err := client.SetBytes(ctx, key2, []byte("two"), time.Minute); err != nil {
		t.Fatalf("set key2: %v", err)
	}

	values, err := client.MGet(ctx, key1, key2, key3)
	if err != nil {
		t.Fatalf("mget: %v", err)
	}
	if len(values) != 3 {
		t.Fatalf("expected 3 values, got %d", len(values))
	}
	if values[0].(string) != "one" || values[1].(string) != "two" || values[2] != nil {
		t.Fatalf("unexpected mget values: %#v", values)
	}
}

// 测试缓存TTL到期后会自动过期
// miniredis支持FastForward 可以在测试中快速推进时间
func TestCacheTTLExpires(t *testing.T) {
	client, mr, cleanup := newMiniRedisClient(t)
	defer cleanup()

	ctx := context.Background()
	key := client.Key("cache:ttl")
	if err := client.SetBytes(ctx, key, []byte("value"), time.Second); err != nil {
		t.Fatalf("set bytes: %v", err)
	}
	mr.FastForward(2 * time.Second)

	if _, err := client.GetBytes(ctx, key); !IsMiss(err) {
		t.Fatalf("expected redis miss after ttl expired, got %v", err)
	}
}
