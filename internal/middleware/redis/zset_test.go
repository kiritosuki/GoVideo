package redis

import (
	"context"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// 测试ZAdd和ZRangeWithScores可以按score升序返回member
func TestZSetAddAndRangeWithScores(t *testing.T) {
	client, _, cleanup := newMiniRedisClient(t)
	defer cleanup()

	ctx := context.Background()
	key := client.Key("zset:range")
	if err := client.ZAdd(ctx, key,
		goredis.Z{Score: 2, Member: "video-2"},
		goredis.Z{Score: 1, Member: "video-1"},
	); err != nil {
		t.Fatalf("zadd: %v", err)
	}

	values, err := client.ZRangeWithScores(ctx, key, 0, -1)
	if err != nil {
		t.Fatalf("zrange with scores: %v", err)
	}
	if len(values) != 2 || values[0].Member != "video-1" || values[1].Member != "video-2" {
		t.Fatalf("unexpected zset order: %#v", values)
	}
}

// 测试ZIncrBy会累加指定member的score
// 热度榜缓存会依赖这个行为累加视频热度
func TestZSetIncrBy(t *testing.T) {
	client, _, cleanup := newMiniRedisClient(t)
	defer cleanup()

	ctx := context.Background()
	key := client.Key("zset:incr")
	if err := client.ZIncrBy(ctx, key, "video-1", 3); err != nil {
		t.Fatalf("zincrby first: %v", err)
	}
	if err := client.ZIncrBy(ctx, key, "video-1", 2); err != nil {
		t.Fatalf("zincrby second: %v", err)
	}

	values, err := client.ZRangeWithScores(ctx, key, 0, -1)
	if err != nil {
		t.Fatalf("zrange with scores: %v", err)
	}
	if len(values) != 1 || values[0].Score != 5 {
		t.Fatalf("expected score 5, got %#v", values)
	}
}

// 测试ZRevRange会按score降序返回member
func TestZSetRevRange(t *testing.T) {
	client, _, cleanup := newMiniRedisClient(t)
	defer cleanup()

	ctx := context.Background()
	key := client.Key("zset:rev")
	if err := client.ZAdd(ctx, key,
		goredis.Z{Score: 1, Member: "video-1"},
		goredis.Z{Score: 3, Member: "video-3"},
		goredis.Z{Score: 2, Member: "video-2"},
	); err != nil {
		t.Fatalf("zadd: %v", err)
	}

	values, err := client.ZRevRange(ctx, key, 0, 1)
	if err != nil {
		t.Fatalf("zrevrange: %v", err)
	}
	if len(values) != 2 || values[0] != "video-3" || values[1] != "video-2" {
		t.Fatalf("unexpected rev range values: %#v", values)
	}
}

// 测试ZRevRangeByScore可以按score区间降序查询
func TestZSetRevRangeByScore(t *testing.T) {
	client, _, cleanup := newMiniRedisClient(t)
	defer cleanup()

	ctx := context.Background()
	key := client.Key("zset:score")
	if err := client.ZAdd(ctx, key,
		goredis.Z{Score: 100, Member: "video-100"},
		goredis.Z{Score: 200, Member: "video-200"},
		goredis.Z{Score: 300, Member: "video-300"},
	); err != nil {
		t.Fatalf("zadd: %v", err)
	}

	values, err := client.ZRevRangeByScore(ctx, key, "250", "100", 0, 10)
	if err != nil {
		t.Fatalf("zrevrange by score: %v", err)
	}
	if len(values) != 2 || values[0] != "video-200" || values[1] != "video-100" {
		t.Fatalf("unexpected score range values: %#v", values)
	}
}

// 测试ZRemRangeByRank可以按排名裁剪旧数据
// timeline worker 会使用这个方法只保留最新的一批视频
func TestZSetRemRangeByRank(t *testing.T) {
	client, _, cleanup := newMiniRedisClient(t)
	defer cleanup()

	ctx := context.Background()
	key := client.Key("zset:trim")
	if err := client.ZAdd(ctx, key,
		goredis.Z{Score: 1, Member: "old"},
		goredis.Z{Score: 2, Member: "middle"},
		goredis.Z{Score: 3, Member: "new"},
	); err != nil {
		t.Fatalf("zadd: %v", err)
	}
	if err := client.ZRemRangeByRank(ctx, key, 0, 0); err != nil {
		t.Fatalf("zremrangebyrank: %v", err)
	}

	values, err := client.ZRangeWithScores(ctx, key, 0, -1)
	if err != nil {
		t.Fatalf("zrange with scores: %v", err)
	}
	if len(values) != 2 || values[0].Member != "middle" || values[1].Member != "new" {
		t.Fatalf("unexpected values after trim: %#v", values)
	}
}

// 测试Exists和Expire能判断key存在并设置过期时间
func TestZSetExistsAndExpire(t *testing.T) {
	client, mr, cleanup := newMiniRedisClient(t)
	defer cleanup()

	ctx := context.Background()
	key := client.Key("zset:expire")
	if err := client.ZAdd(ctx, key, goredis.Z{Score: 1, Member: "video-1"}); err != nil {
		t.Fatalf("zadd: %v", err)
	}
	exists, err := client.Exists(ctx, key)
	if err != nil {
		t.Fatalf("exists: %v", err)
	}
	if !exists {
		t.Fatal("expected zset key to exist")
	}
	if err := client.Expire(ctx, key, time.Second); err != nil {
		t.Fatalf("expire: %v", err)
	}
	mr.FastForward(2 * time.Second)

	exists, err = client.Exists(ctx, key)
	if err != nil {
		t.Fatalf("exists after expire: %v", err)
	}
	if exists {
		t.Fatal("expected zset key to expire")
	}
}

// 测试nil client下的zset写操作会安全降级
// Redis不可用时 项目中部分派生缓存写入会直接跳过
func TestZSetNilClientWriteOperations(t *testing.T) {
	var client *Client
	ctx := context.Background()

	if err := client.ZAdd(ctx, "key", goredis.Z{Score: 1, Member: "m"}); err != nil {
		t.Fatalf("nil client zadd should not error: %v", err)
	}
	if err := client.ZIncrBy(ctx, "key", "m", 1); err != nil {
		t.Fatalf("nil client zincrby should not error: %v", err)
	}
	if err := client.ZRemRangeByRank(ctx, "key", 0, -1); err != nil {
		t.Fatalf("nil client zremrangebyrank should not error: %v", err)
	}
}
