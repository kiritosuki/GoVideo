package redis

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// ZIncrBy 增加zset中某member的score zset不存在时自动创建
func (c *Client) ZIncrBy(ctx context.Context, key string, member string, score float64) error {
	if c == nil || c.rdb == nil {
		return nil
	}
	return c.rdb.ZIncrBy(ctx, key, score, member).Err()
}

// ZAdd 向zset中批量添加member
func (c *Client) ZAdd(ctx context.Context, key string, members ...redis.Z) error {
	if c == nil || c.rdb == nil {
		return nil
	}
	return c.rdb.ZAdd(ctx, key, members...).Err()
}

// ZRemRangeByRank 删除zset中 [start, stop] 闭区间范围内的member
// 支持负数 例如 [-3, -1] 为删除最后三个
func (c *Client) ZRemRangeByRank(ctx context.Context, key string, start int64, stop int64) error {
	if c == nil || c.rdb == nil {
		return nil
	}
	return c.rdb.ZRemRangeByRank(ctx, key, start, stop).Err()
}

// ZRangeWithScores 按照score升序排序 查询zset中 [start, stop] 闭区间范围内的member
// member与score一并封装返回
func (c *Client) ZRangeWithScores(ctx context.Context, key string, start int64, stop int64) ([]redis.Z, error) {
	if c == nil || c.rdb == nil {
		return nil, errors.New("redis client not initialized")
	}
	return c.rdb.ZRangeWithScores(ctx, key, start, stop).Result()
}

// Expire 设置过期时长
// 针对key设置 zset类型中即为整个zset的过期时长 而不是某个member
func (c *Client) Expire(ctx context.Context, key string, ttl time.Duration) error {
	if c == nil || c.rdb == nil {
		return nil
	}
	return c.rdb.Expire(ctx, key, ttl).Err()
}

// ZUnionStore 取并集 合并多个zset 结果存入一个新的zset dst
// aggregate 可取 SUM, MIN or MAX 决策不同zset中如果有相同的member时的合并方案
func (c *Client) ZUnionStore(ctx context.Context, dst string, keys []string, aggregate string) error {
	if c == nil || c.rdb == nil {
		return nil
	}
	return c.rdb.ZUnionStore(ctx, dst, &redis.ZStore{
		Keys:      keys,
		Aggregate: aggregate,
	}).Err()
}

// Exists 判断key是否存在
func (c *Client) Exists(ctx context.Context, key string) (bool, error) {
	if c == nil || c.rdb == nil {
		return false, nil
	}
	n, err := c.rdb.Exists(ctx, key).Result()
	return n > 0, err
}

// ZRevRange 按照score降序排序 查询zset中 [start, stop] 区间内的member
func (c *Client) ZRevRange(ctx context.Context, key string, start int64, stop int64) ([]string, error) {
	if c == nil || c.rdb == nil {
		return nil, nil
	}
	return c.rdb.ZRevRange(ctx, key, start, stop).Result()
}

// ZRevRangeByScore 按照score降序排序 在zset中查询score在 [min, max] 区间内的member
// offset和count用于对查询结果二次筛选
// offset表示跳过前几个 从查询后结果的offset索引处开始返回
// count表示限制最大返回数量为count
func (c *Client) ZRevRangeByScore(ctx context.Context, key string, max string, min string, offset int64, count int64) ([]string, error) {
	if c == nil || c.rdb == nil {
		return nil, nil
	}
	return c.rdb.ZRevRangeByScore(ctx, key, &redis.ZRangeBy{
		Max:    max,
		Min:    min,
		Offset: offset,
		Count:  count,
	}).Result()
}
