package cache

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

const redisKeyPrefix = "cache:"

// RedisCache is a Redis-backed cache. TTL is enforced Redis-side via SET ... EX.
type RedisCache struct {
	client *redis.Client
	ttl    time.Duration
}

func NewRedisCache(client *redis.Client, ttl time.Duration) *RedisCache {
	return &RedisCache{client: client, ttl: ttl}
}

func (c *RedisCache) Get(ctx context.Context, key string) (Entry, bool, error) {
	data, err := c.client.Get(ctx, redisKeyPrefix+key).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return Entry{}, false, nil // clean miss
		}
		return Entry{}, false, err // real error — surface for logging
	}
	var entry Entry
	if err := json.Unmarshal(data, &entry); err != nil {
		return Entry{}, false, err
	}
	return entry, true, nil
}

func (c *RedisCache) Set(ctx context.Context, key string, entry Entry) error {
	if c.ttl <= 0 {
		return errors.New("ttl must be positive")
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	return c.client.Set(ctx, redisKeyPrefix+key, data, c.ttl).Err()
}
