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

// 时间窗口 用于限流
// 在首次 INCR KEY 时重置过期时间 后续请求仅增加计数器
// 可以在该时间段内限制请求次数 进行限流
var incrementWithExprScript = redis.NewScript(`
local count = redis.call("INCR", KEYS[1])
if count == 1 then
	redis.call("PEXPIRE", KEYS[1], ARGV[1])
end
return count
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
// 初次调用会开启过期时间 包括初次和之后每次调用会让计数器原子性+1
func (c *Client) IncrementWithExpr(ctx context.Context, key string, expr time.Duration) (int64, error) {
	if c == nil || c.rdb == nil {
		return 0, nil
	}
	return incrementWithExprScript.Run(ctx, c.rdb, []string{key}, expr.Milliseconds()).Int64()
}
