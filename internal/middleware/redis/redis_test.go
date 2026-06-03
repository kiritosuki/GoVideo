package redis

import (
	"context"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
)

func newMiniRedisClient(t *testing.T) (*Client, *miniredis.Miniredis, func()) {
	t.Helper()

	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	client := &Client{
		rdb: goredis.NewClient(&goredis.Options{Addr: mr.Addr()}),
	}
	cleanup := func() {
		client.Close()
		mr.Close()
	}
	return client, mr, cleanup
}

// 测试在首次设置时间窗口TTL后 时间窗口会不会延长
func TestIncrementWithExprSetTTLWithoutExtendingWindow(t *testing.T) {
	client, mr, cleanup := newMiniRedisClient(t)
	defer cleanup()

	ctx := context.Background()
	key := "feedsystem:ratelimit:test"
	expire := 30 * time.Second

	count, err := client.IncrementWithExpr(ctx, key, expire)
	if err != nil {
		t.Fatalf("first increment: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected count 1, got %d", count)
	}

	firstTTL := mr.TTL(key)
	if firstTTL <= 0 || firstTTL > expire {
		t.Fatalf("expected ttl in (0, %s], got %s", expire, firstTTL)
	}
	// 让TTL前进5s
	mr.FastForward(5 * time.Second)
	ttlBeforeSecond := mr.TTL(key)

	count, err = client.IncrementWithExpr(ctx, key, expire)
	if err != nil {
		t.Fatalf("second increment: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected count 2, got %d", count)
	}

	ttlAfterSecond := mr.TTL(key)
	if ttlAfterSecond != ttlBeforeSecond {
		t.Fatalf("expected ttl to stay at %s, got %s", ttlBeforeSecond, ttlAfterSecond)
	}
}

func TestSlidingWindowAllow(t *testing.T) {
	client, _, cleanup := newMiniRedisClient(t)
	defer cleanup()

	ctx := context.Background()
	key := "feedsystem:ratelimit:sliding"
	window := time.Second
	base := time.UnixMilli(100000)

	// 窗口内前两次请求没有超过上限，应当放行。
	allowed, count, err := client.slidingWindowAllowAt(ctx, key, 2, window, base)
	if err != nil {
		t.Fatalf("first request: %v", err)
	}
	if !allowed || count != 1 {
		t.Fatalf("expected first request allowed with count 1, got allowed=%v count=%d", allowed, count)
	}

	allowed, count, err = client.slidingWindowAllowAt(ctx, key, 2, window, base.Add(100*time.Millisecond))
	if err != nil {
		t.Fatalf("second request: %v", err)
	}
	if !allowed || count != 2 {
		t.Fatalf("expected second request allowed with count 2, got allowed=%v count=%d", allowed, count)
	}

	// 第三次请求仍在同一个1s窗口内，达到上限后应当拒绝。
	allowed, count, err = client.slidingWindowAllowAt(ctx, key, 2, window, base.Add(500*time.Millisecond))
	if err != nil {
		t.Fatalf("third request: %v", err)
	}
	if allowed || count != 2 {
		t.Fatalf("expected third request rejected with count 2, got allowed=%v count=%d", allowed, count)
	}

	// 时间滑过窗口后，最早的一次请求被清理，新请求应当重新放行。
	allowed, count, err = client.slidingWindowAllowAt(ctx, key, 2, window, base.Add(1001*time.Millisecond))
	if err != nil {
		t.Fatalf("request after window: %v", err)
	}
	if !allowed || count != 2 {
		t.Fatalf("expected request after sliding window allowed with count 2, got allowed=%v count=%d", allowed, count)
	}
}

func TestSlidingWindowAllowSameMillisecondRequests(t *testing.T) {
	client, _, cleanup := newMiniRedisClient(t)
	defer cleanup()

	ctx := context.Background()
	key := "feedsystem:ratelimit:same-ms"
	now := time.UnixMilli(200000)

	// 多个请求可能落在同一毫秒内，seqKey用于生成唯一member，避免ZSET覆盖导致少计数。
	for i := 1; i <= 3; i++ {
		allowed, count, err := client.slidingWindowAllowAt(ctx, key, 3, time.Second, now)
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		if !allowed || count != int64(i) {
			t.Fatalf("expected request %d allowed with count %d, got allowed=%v count=%d", i, i, allowed, count)
		}
	}

	allowed, count, err := client.slidingWindowAllowAt(ctx, key, 3, time.Second, now)
	if err != nil {
		t.Fatalf("request over limit: %v", err)
	}
	if allowed || count != 3 {
		t.Fatalf("expected request over limit rejected with count 3, got allowed=%v count=%d", allowed, count)
	}
}

func TestSlidingWindowAllowIndependentKeys(t *testing.T) {
	client, _, cleanup := newMiniRedisClient(t)
	defer cleanup()

	ctx := context.Background()
	window := time.Second
	now := time.UnixMilli(300000)

	// 不同限流key代表不同主体，例如不同IP或不同账号；它们的计数不能互相影响。
	allowed, count, err := client.slidingWindowAllowAt(ctx, "feedsystem:ratelimit:user-a", 1, window, now)
	if err != nil {
		t.Fatalf("user-a first request: %v", err)
	}
	if !allowed || count != 1 {
		t.Fatalf("expected user-a first request allowed with count 1, got allowed=%v count=%d", allowed, count)
	}

	allowed, count, err = client.slidingWindowAllowAt(ctx, "feedsystem:ratelimit:user-a", 1, window, now.Add(100*time.Millisecond))
	if err != nil {
		t.Fatalf("user-a second request: %v", err)
	}
	if allowed || count != 1 {
		t.Fatalf("expected user-a second request rejected with count 1, got allowed=%v count=%d", allowed, count)
	}

	allowed, count, err = client.slidingWindowAllowAt(ctx, "feedsystem:ratelimit:user-b", 1, window, now.Add(100*time.Millisecond))
	if err != nil {
		t.Fatalf("user-b first request: %v", err)
	}
	if !allowed || count != 1 {
		t.Fatalf("expected user-b first request allowed with count 1, got allowed=%v count=%d", allowed, count)
	}
}

func TestSlidingWindowAllowKeepsTTL(t *testing.T) {
	client, _, cleanup := newMiniRedisClient(t)
	defer cleanup()

	ctx := context.Background()
	key := "feedsystem:ratelimit:ttl"
	window := time.Second
	now := time.UnixMilli(400000)

	// 每次访问都会刷新TTL，避免长时间不用的限流key残留。
	if allowed, _, err := client.slidingWindowAllowAt(ctx, key, 2, window, now); err != nil || !allowed {
		t.Fatalf("first request expected allowed, allowed=%v err=%v", allowed, err)
	}
	ttl := client.rdb.TTL(ctx, key).Val()
	if ttl <= window || ttl > window+time.Second {
		t.Fatalf("expected ttl in (%s, %s], got %s", window, window+time.Second, ttl)
	}

	if allowed, _, err := client.slidingWindowAllowAt(ctx, key, 2, window, now.Add(100*time.Millisecond)); err != nil || !allowed {
		t.Fatalf("second request expected allowed, allowed=%v err=%v", allowed, err)
	}
	refreshedTTL := client.rdb.TTL(ctx, key).Val()
	if refreshedTTL <= 0 {
		t.Fatalf("expected refreshed ttl > 0, got %s", refreshedTTL)
	}
}

func TestSlidingWindowAllowNilClient(t *testing.T) {
	var client *Client

	// Redis不可用时返回允许，保持限流故障不影响接口可用性的策略。
	allowed, count, err := client.SlidingWindowAllow(context.Background(), "feedsystem:ratelimit:nil", 1, time.Second)
	if err != nil {
		t.Fatalf("nil client should not return error: %v", err)
	}
	if !allowed || count != 0 {
		t.Fatalf("expected nil client to allow with count 0, got allowed=%v count=%d", allowed, count)
	}
}
