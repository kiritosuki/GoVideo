package redis

import (
	"context"
	"encoding/json"
	"errors"

	redis "github.com/redis/go-redis/v9"
)

// PublishJSON 向Redis Pub/Sub频道发布JSON消息。
func (c *Client) PublishJSON(ctx context.Context, ch string, payload any) error {
	if c == nil || c.rdb == nil {
		return errors.New("redis client is not initialized")
	}
	if ch == "" {
		return errors.New("redis pubsub channel is required")
	}
	bytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return c.rdb.Publish(ctx, ch, bytes).Err()
}

// Subscribe 订阅Redis Pub/Sub频道。
func (c *Client) Subscribe(ctx context.Context, chs ...string) (*redis.PubSub, error) {
	if c == nil || c.rdb == nil {
		return nil, errors.New("redis client is not initialized")
	}
	if len(chs) == 0 {
		return nil, errors.New("redis pubsub channel is required")
	}
	return c.rdb.Subscribe(ctx, chs...), nil
}
