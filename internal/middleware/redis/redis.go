package redis

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/kiritosuki/GoVideo/internal/config"
	redis "github.com/redis/go-redis/v9"
)

const DefaultKeyPrefix = "v1:"

// Client 封装redis客户端对象 外部使用该对象的方法操作redis
type Client struct {
	rdb       *redis.Client
	keyPrefix string
}

// NewClient 创建redis客户端对象 用于redis相关操作
func NewClient(redisConfig *config.RedisConfig) (*Client, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:     redisConfig.Host + ":" + strconv.Itoa(redisConfig.Port),
		Password: redisConfig.Password,
		DB:       redisConfig.DB,
	})
	return &Client{rdb: rdb, keyPrefix: DefaultKeyPrefix}, nil
}

// Close 关闭redis客户端
func (c *Client) Close() error {
	if c == nil || c.rdb == nil {
		return nil
	}
	return c.rdb.Close()
}

// Ping 在应用层向redis服务发送信号 测试redis服务是否正常运行
func (c *Client) Ping(ctx context.Context) error {
	if c == nil || c.rdb == nil {
		return nil
	}
	return c.rdb.Ping(ctx).Err()
}

// IsMiss 判断返回的错误类型是不是 redis.Nil 表示key不存在
func IsMiss(err error) bool {
	return errors.Is(err, redis.Nil)
}

// Key 生成存入redis的key
func (c *Client) Key(format string, args ...any) string {
	prefix := ""
	if c != nil {
		prefix = c.keyPrefix
	}
	return prefix + fmt.Sprintf(format, args...)
}

// randToken 生成指定字节数的随机安全字符串
func randToken(n int) (string, error) {
	b := make([]byte, n)
	// 生成加密安全的随机数填入字节数组
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	// 二进制字节数组转十六进制字符串
	return hex.EncodeToString(b), nil
}

// Lock 获取用redis实现的分布式锁
func (c *Client) Lock(ctx context.Context, key string, ttl time.Duration) (string, bool, error) {
	if c == nil || c.rdb == nil {
		return "", false, nil
	}
	token, err := randToken(16)
	if err != nil {
		return "", false, err
	}
	ok, err := c.rdb.SetNX(ctx, key, token, ttl).Result()
	return token, ok, err
}

// lua脚本命令 原子操作
// 删除锁之前判断这个锁是不是自己的 根据value中存的token判断 如果是再执行删除
var unlockScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
	return redis.call("DEL", KEYS[1])
else
	return 0
end
`)

// 固定窗口 用于限流
// 在首次 INCR KEY 时重置过期时间 后续请求仅增加计数器
// 可以在该时间段内限制请求次数 进行限流
// TODO 滑动窗口替代 已经不再使用 后续可以选择移除
var incrementWithExprScript = redis.NewScript(`
local count = redis.call("INCR", KEYS[1])
if count == 1 then
	redis.call("PEXPIRE", KEYS[1], ARGV[1])
end
return count
`)

// 滑动窗口限流脚本
// KEYS[1]: 限流zset key
// KEYS[2]: 当前key对应的请求序列号key，用于保证同一毫秒内member唯一
// ARGV[1]: 当前时间戳，毫秒
// ARGV[2]: 窗口大小，毫秒
// ARGV[3]: 最大请求数
// ARGV[4]: key过期时间，毫秒
var slidingWindowScript = redis.NewScript(`
local key = KEYS[1]
local seqKey = KEYS[2]
local now = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local limit = tonumber(ARGV[3])
local ttl = tonumber(ARGV[4])
local minScore = now - window

redis.call("ZREMRANGEBYSCORE", key, 0, minScore)
local count = redis.call("ZCARD", key)
if count >= limit then
	redis.call("PEXPIRE", key, ttl)
	redis.call("PEXPIRE", seqKey, ttl)
	return {0, count}
end

local seq = redis.call("INCR", seqKey)
local member = tostring(now) .. "-" .. tostring(seq)
redis.call("ZADD", key, now, member)
redis.call("PEXPIRE", key, ttl)
redis.call("PEXPIRE", seqKey, ttl)
return {1, count + 1}
`)

// Unlock 释放分布式锁
func (c *Client) Unlock(ctx context.Context, key string, token string) error {
	if c == nil || c.rdb == nil {
		return nil
	}
	_, err := unlockScript.Run(ctx, c.rdb, []string{key}, token).Result()
	return err
}

// IncrementWithExpr 用于对key做给定expr时间内的自增计数
// 初次调用会开启过期时间 计时器为1 之后每次调用会让计数器原子性+1
// TODO 滑动窗口替代 已经不再使用 后续可以选择移除
func (c *Client) IncrementWithExpr(ctx context.Context, key string, expr time.Duration) (int64, error) {
	if c == nil || c.rdb == nil {
		return 0, nil
	}
	// 返回计数器次数
	return incrementWithExprScript.Run(ctx, c.rdb, []string{key}, expr.Milliseconds()).Int64()
}

// SlidingWindowAllow 使用滑动窗口算法判断本次请求是否允许通过
// 返回 bool 窗口内请求数量 error
func (c *Client) SlidingWindowAllow(ctx context.Context, key string, maxRequests int64, window time.Duration) (bool, int64, error) {
	return c.slidingWindowAllowAt(ctx, key, maxRequests, window, time.Now())
}

func (c *Client) slidingWindowAllowAt(ctx context.Context, key string, maxRequests int64, window time.Duration, now time.Time) (bool, int64, error) {
	if c == nil || c.rdb == nil {
		return true, 0, nil
	}
	if key == "" || maxRequests <= 0 || window <= 0 {
		return true, 0, nil
	}
	ttl := window + time.Second
	res, err := slidingWindowScript.Run(
		ctx,
		c.rdb,
		[]string{key, key + ":seq"},
		now.UnixMilli(),
		window.Milliseconds(),
		maxRequests,
		ttl.Milliseconds(),
	).Result()
	if err != nil {
		return true, 0, err
	}
	values, ok := res.([]interface{})
	if !ok || len(values) != 2 {
		return true, 0, errors.New("unexpected sliding window result")
	}
	allowed, ok := values[0].(int64)
	if !ok {
		return true, 0, errors.New("unexpected sliding window allowed value")
	}
	count, ok := values[1].(int64)
	if !ok {
		return true, 0, errors.New("unexpected sliding window count value")
	}
	return allowed == 1, count, nil
}
