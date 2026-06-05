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

	// 使用miniredis启动内存Redis 避免单元测试依赖真实Redis服务
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	// 直接构造Client 让测试可以使用内部redis client和时间推进能力
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
// 这个测试覆盖旧固定窗口限流脚本 主要确认首次INCR设置TTL 后续INCR不会重置窗口时间
func TestIncrementWithExprSetTTLWithoutExtendingWindow(t *testing.T) {
	client, mr, cleanup := newMiniRedisClient(t)
	defer cleanup()

	ctx := context.Background()
	key := "feedsystem:ratelimit:test"
	expire := 30 * time.Second

	// 第一次请求会创建计数器 并设置固定窗口TTL
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

	// 第二次请求只增加计数 不应该把TTL重置回完整窗口
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

// 测试滑动窗口限流的基础行为
// 窗口内未超过上限放行 达到上限拒绝 时间滑出窗口后重新放行
func TestSlidingWindowAllow(t *testing.T) {
	client, _, cleanup := newMiniRedisClient(t)
	defer cleanup()

	ctx := context.Background()
	key := "feedsystem:ratelimit:sliding"
	window := time.Second
	base := time.UnixMilli(100000)

	// 窗口内第一次请求没有超过上限 应当放行
	allowed, count, err := client.slidingWindowAllowAt(ctx, key, 2, window, base)
	if err != nil {
		t.Fatalf("first request: %v", err)
	}
	if !allowed || count != 1 {
		t.Fatalf("expected first request allowed with count 1, got allowed=%v count=%d", allowed, count)
	}

	// 窗口内第二次请求仍未超过上限 应当继续放行
	allowed, count, err = client.slidingWindowAllowAt(ctx, key, 2, window, base.Add(100*time.Millisecond))
	if err != nil {
		t.Fatalf("second request: %v", err)
	}
	if !allowed || count != 2 {
		t.Fatalf("expected second request allowed with count 2, got allowed=%v count=%d", allowed, count)
	}

	// 第三次请求仍在同一个1s窗口内 达到上限后应当拒绝
	allowed, count, err = client.slidingWindowAllowAt(ctx, key, 2, window, base.Add(500*time.Millisecond))
	if err != nil {
		t.Fatalf("third request: %v", err)
	}
	if allowed || count != 2 {
		t.Fatalf("expected third request rejected with count 2, got allowed=%v count=%d", allowed, count)
	}

	// 时间滑过窗口后 最早的一次请求会被清理 新请求应当重新放行
	allowed, count, err = client.slidingWindowAllowAt(ctx, key, 2, window, base.Add(1001*time.Millisecond))
	if err != nil {
		t.Fatalf("request after window: %v", err)
	}
	if !allowed || count != 2 {
		t.Fatalf("expected request after sliding window allowed with count 2, got allowed=%v count=%d", allowed, count)
	}
}

// 测试同一毫秒内的多次请求不会被ZSET覆盖
// 滑动窗口使用 now-seq 作为member 保证同一毫秒内每次请求都有独立member
func TestSlidingWindowAllowSameMillisecondRequests(t *testing.T) {
	client, _, cleanup := newMiniRedisClient(t)
	defer cleanup()

	ctx := context.Background()
	key := "feedsystem:ratelimit:same-ms"
	now := time.UnixMilli(200000)

	// 前三次请求落在同一毫秒内 但member不同 因此计数会从1递增到3
	for i := 1; i <= 3; i++ {
		allowed, count, err := client.slidingWindowAllowAt(ctx, key, 3, time.Second, now)
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		if !allowed || count != int64(i) {
			t.Fatalf("expected request %d allowed with count %d, got allowed=%v count=%d", i, i, allowed, count)
		}
	}

	// 第四次请求仍在同一毫秒内 但窗口内计数已经达到3 因此会被拒绝
	allowed, count, err := client.slidingWindowAllowAt(ctx, key, 3, time.Second, now)
	if err != nil {
		t.Fatalf("request over limit: %v", err)
	}
	if allowed || count != 3 {
		t.Fatalf("expected request over limit rejected with count 3, got allowed=%v count=%d", allowed, count)
	}
}

// 测试不同限流key之间相互隔离
// 不同IP或不同账号会生成不同key 一个用户被限流不应影响另一个用户
func TestSlidingWindowAllowIndependentKeys(t *testing.T) {
	client, _, cleanup := newMiniRedisClient(t)
	defer cleanup()

	ctx := context.Background()
	window := time.Second
	now := time.UnixMilli(300000)

	// user-a第一次请求放行
	allowed, count, err := client.slidingWindowAllowAt(ctx, "feedsystem:ratelimit:user-a", 1, window, now)
	if err != nil {
		t.Fatalf("user-a first request: %v", err)
	}
	if !allowed || count != 1 {
		t.Fatalf("expected user-a first request allowed with count 1, got allowed=%v count=%d", allowed, count)
	}

	// user-a第二次请求超过自身上限 因此被拒绝
	allowed, count, err = client.slidingWindowAllowAt(ctx, "feedsystem:ratelimit:user-a", 1, window, now.Add(100*time.Millisecond))
	if err != nil {
		t.Fatalf("user-a second request: %v", err)
	}
	if allowed || count != 1 {
		t.Fatalf("expected user-a second request rejected with count 1, got allowed=%v count=%d", allowed, count)
	}

	// user-b使用独立key 不受user-a限流状态影响 因此仍然放行
	allowed, count, err = client.slidingWindowAllowAt(ctx, "feedsystem:ratelimit:user-b", 1, window, now.Add(100*time.Millisecond))
	if err != nil {
		t.Fatalf("user-b first request: %v", err)
	}
	if !allowed || count != 1 {
		t.Fatalf("expected user-b first request allowed with count 1, got allowed=%v count=%d", allowed, count)
	}
}

// 测试滑动窗口key会设置TTL
// TTL用于清理长时间不用的限流key 防止Redis中限流key无限增长
func TestSlidingWindowAllowKeepsTTL(t *testing.T) {
	client, _, cleanup := newMiniRedisClient(t)
	defer cleanup()

	ctx := context.Background()
	key := "feedsystem:ratelimit:ttl"
	window := time.Second
	now := time.UnixMilli(400000)

	// 第一次请求后 应当给限流zset设置 window+1s 左右的TTL
	if allowed, _, err := client.slidingWindowAllowAt(ctx, key, 2, window, now); err != nil || !allowed {
		t.Fatalf("first request expected allowed, allowed=%v err=%v", allowed, err)
	}
	ttl := client.rdb.TTL(ctx, key).Val()
	if ttl <= window || ttl > window+time.Second {
		t.Fatalf("expected ttl in (%s, %s], got %s", window, window+time.Second, ttl)
	}

	// 第二次请求后 TTL仍然应当存在 用于保证限流key最终会过期
	if allowed, _, err := client.slidingWindowAllowAt(ctx, key, 2, window, now.Add(100*time.Millisecond)); err != nil || !allowed {
		t.Fatalf("second request expected allowed, allowed=%v err=%v", allowed, err)
	}
	refreshedTTL := client.rdb.TTL(ctx, key).Val()
	if refreshedTTL <= 0 {
		t.Fatalf("expected refreshed ttl > 0, got %s", refreshedTTL)
	}
}

// 测试Redis客户端为空时限流默认放行
// 这是项目的降级策略: Redis故障不能导致接口全部不可用
func TestSlidingWindowAllowNilClient(t *testing.T) {
	var client *Client

	// Redis不可用时返回允许 并且count返回0 表示没有实际读取窗口计数
	allowed, count, err := client.SlidingWindowAllow(context.Background(), "feedsystem:ratelimit:nil", 1, time.Second)
	if err != nil {
		t.Fatalf("nil client should not return error: %v", err)
	}
	if !allowed || count != 0 {
		t.Fatalf("expected nil client to allow with count 0, got allowed=%v count=%d", allowed, count)
	}
}
