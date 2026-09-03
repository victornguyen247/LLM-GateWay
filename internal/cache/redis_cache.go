package cache

import (
	"context"
	"github.com/redis/go-redis/v9"
)

const RedisCachePrefix = "cache:"

type RedisCache struct {
	client *redis.Client
	ttl time.Duration
}

func NewRedisCache(client *redis.Client, ttl time.Duration) *RedisCache {
	return &RedisCache{client: client, ttl: ttl}
}

func (c *RedisCache) Get(ctx context.Context, key string) (Entry, bool, error) {
	redisKey := RedisCachePrefix + key
	val, err := c.client.Get(ctx, redisKey).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return Entry{}, false, nil
		}
		return Entry{}, false, err
	}
	entry := Entry{}
	err = json.Unmarshal([]byte(val), &entry)
	if err != nil {
		return Entry{}, false, err
	}
	return entry, true, nil
}

func (c *RedisCache) Set(ctx context.Context, key string, entry Entry) error {
	if c.ttl <= 0 {
		return errors.New("ttl must be greater than 0")
	}
	redisKey := RedisCachePrefix + key
	val, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	return c.client.Set(ctx, redisKey, val, c.ttl).Err()
}