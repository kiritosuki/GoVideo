//go:build integration

package integration

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestRedisDistributedLock(t *testing.T) {
	env := setupIntegration(t)
	ctx := context.Background()
	key := env.cache.Key("it:lock:basic")

	// 第一次获取锁应该成功。
	token, ok, err := env.cache.Lock(ctx, key, 300*time.Millisecond)
	if err != nil {
		t.Fatalf("获取锁失败: %v", err)
	}
	if !ok || token == "" {
		t.Fatalf("第一次获取锁应该成功, ok=%v token=%q", ok, token)
	}

	// 同一个key已经被占用时，第二次获取锁应该失败。
	_, ok, err = env.cache.Lock(ctx, key, 300*time.Millisecond)
	if err != nil {
		t.Fatalf("重复获取锁不应报错: %v", err)
	}
	if ok {
		t.Fatalf("锁未释放前不应该再次获取成功")
	}

	// token不匹配时不能释放别人的锁。
	if err := env.cache.Unlock(ctx, key, "wrong-token"); err != nil {
		t.Fatalf("错误token释放锁不应报错: %v", err)
	}
	_, ok, err = env.cache.Lock(ctx, key, 300*time.Millisecond)
	if err != nil {
		t.Fatalf("错误token释放后重新获取锁失败: %v", err)
	}
	if ok {
		t.Fatalf("错误token不应该释放锁")
	}

	// 正确token释放后，锁可以再次被获取。
	if err := env.cache.Unlock(ctx, key, token); err != nil {
		t.Fatalf("正确token释放锁失败: %v", err)
	}
	_, ok, err = env.cache.Lock(ctx, key, 300*time.Millisecond)
	if err != nil {
		t.Fatalf("释放后重新获取锁失败: %v", err)
	}
	if !ok {
		t.Fatalf("释放后应该可以重新获取锁")
	}
}

func TestRedisDistributedLockExpires(t *testing.T) {
	env := setupIntegration(t)
	ctx := context.Background()
	key := env.cache.Key("it:lock:expire")

	// 锁过期后应该允许其他节点重新获取。
	if _, ok, err := env.cache.Lock(ctx, key, 80*time.Millisecond); err != nil || !ok {
		t.Fatalf("获取短TTL锁失败, ok=%v err=%v", ok, err)
	}
	time.Sleep(150 * time.Millisecond)
	if _, ok, err := env.cache.Lock(ctx, key, 300*time.Millisecond); err != nil || !ok {
		t.Fatalf("锁TTL过期后应该可以重新获取, ok=%v err=%v", ok, err)
	}
}

func TestRedisDistributedLockConcurrentCompetition(t *testing.T) {
	env := setupIntegration(t)
	ctx := context.Background()
	key := env.cache.Key("it:lock:concurrent")

	var wg sync.WaitGroup
	var mu sync.Mutex
	success := 0
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, ok, err := env.cache.Lock(ctx, key, time.Second)
			if err != nil {
				t.Errorf("并发获取锁报错: %v", err)
				return
			}
			if ok {
				mu.Lock()
				success++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	// Redis SET NX 保证同一时刻只有一个竞争者成功。
	if success != 1 {
		t.Fatalf("并发抢锁应该只有一个成功, got=%d", success)
	}
}
