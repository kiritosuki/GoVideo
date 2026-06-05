//go:build integration

package integration

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestSlidingWindowRateLimit(t *testing.T) {
	env := setupIntegration(t)
	ctx := context.Background()
	key := env.cache.Key("it:ratelimit:basic")

	// 窗口内前三次请求允许通过。
	for i := 1; i <= 3; i++ {
		allowed, count, err := env.cache.SlidingWindowAllow(ctx, key, 3, 200*time.Millisecond)
		if err != nil {
			t.Fatalf("滑动窗口限流执行失败: %v", err)
		}
		if !allowed || count != int64(i) {
			t.Fatalf("第%d次请求应允许通过, allowed=%v count=%d", i, allowed, count)
		}
	}

	// 同一窗口内第四次请求超过阈值，应该被拒绝。
	allowed, count, err := env.cache.SlidingWindowAllow(ctx, key, 3, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("滑动窗口限流执行失败: %v", err)
	}
	if allowed || count != 3 {
		t.Fatalf("超过阈值后应拒绝, allowed=%v count=%d", allowed, count)
	}

	// 窗口滑动后，旧请求被清理，新请求恢复允许。
	time.Sleep(260 * time.Millisecond)
	allowed, _, err = env.cache.SlidingWindowAllow(ctx, key, 3, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("窗口滑动后限流执行失败: %v", err)
	}
	if !allowed {
		t.Fatalf("窗口滑动后应该允许新请求")
	}
}

func TestSlidingWindowRateLimitIndependentKeysAndConcurrency(t *testing.T) {
	env := setupIntegration(t)
	ctx := context.Background()

	keyA := env.cache.Key("it:ratelimit:user:a")
	keyB := env.cache.Key("it:ratelimit:user:b")
	if allowed, _, _ := env.cache.SlidingWindowAllow(ctx, keyA, 1, time.Second); !allowed {
		t.Fatalf("keyA第一次请求应该允许")
	}
	if allowed, _, _ := env.cache.SlidingWindowAllow(ctx, keyA, 1, time.Second); allowed {
		t.Fatalf("keyA第二次请求应该被拒绝")
	}
	if allowed, _, _ := env.cache.SlidingWindowAllow(ctx, keyB, 1, time.Second); !allowed {
		t.Fatalf("不同key之间不应该互相影响")
	}

	keyC := env.cache.Key("it:ratelimit:concurrent")
	var wg sync.WaitGroup
	var mu sync.Mutex
	allowedCount := 0
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			allowed, _, err := env.cache.SlidingWindowAllow(ctx, keyC, 5, time.Second)
			if err != nil {
				t.Errorf("并发限流执行失败: %v", err)
				return
			}
			if allowed {
				mu.Lock()
				allowedCount++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if allowedCount != 5 {
		t.Fatalf("并发限流应该只允许5个请求, got=%d", allowedCount)
	}
}
