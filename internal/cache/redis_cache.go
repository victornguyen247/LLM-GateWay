package cache

import (
	"context"
	"github.com/redis/go-redis/v9"
)

type RedisCache struct {
	client *redis.Client
	ttl time.Duration
}

func NewRedisCache(client *redis.Client, ttl time.Duration) *RedisCache {
	return &RedisCache{client: client, ttl: ttl}
}

func (c *RedisCache) Get(ctx context.Context, key string) (Entry, bool, error) {
	redisKey := "cache:" + key
	val, err := c.client.Get(ctx, redisKey).Bytes()
	if err != nil {
		return Entry{}, false, err
	}
	if errors.Is(err, redis.Nil) {
		return Entry{}, false, nil
	}
	entry := Entry{}
	err = json.Unmarshal([]byte(val), &entry)
	if err != nil {
		return Entry{}, false, err
	}
	return entry, true, nil
}

func (c *RedisCache) Set(ctx context.Context, key string, entry Entry) error {
	redisKey := "cache:" + key
	val, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	return c.client.Set(ctx, redisKey, val, c.ttl).Err()
}