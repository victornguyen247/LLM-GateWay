package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisLimiter implements Limiter using a sliding window counter backed by Redis.
//
// Algorithm choice: sliding window counter, not token bucket. Token bucket
// requires atomic read-modify-write with floating-point refill math, which
// needs a Lua script to be safe across concurrent replicas. Sliding window
// reduces to INCR (atomic) + EXPIRE (only on creation), which maps directly
// onto Redis's atomic primitives.
//
// Failure mode: if Redis is unreachable, Allow returns (false, err).
// Callers (middleware) should fail OPEN on err != nil — a rate limiter
// outage shouldn't block all traffic; log it instead.
type RedisLimiter struct {
	client *redis.Client
	window time.Duration
	limit  int
	script *redis.Script
}

var slidingWindowScript = redis.NewScript(`
	-- KEYS[1] = current window key
	-- KEYS[2] = previous window key
	-- ARGV[1] = window size in seconds
	-- ARGV[2] = limit
	-- ARGV[3] = elapsed in current window
	local windowSize = tonumber(ARGV[1])
	local limit = tonumber(ARGV[2])
	local elapsed = tonumber(ARGV[3])

	local currentCount = redis.call("INCR", KEYS[1])
	if currentCount == 1 then
		-- 2x window size: key must survive as both "current" and, one bucket
		-- later, "previous" window, regardless of when within its own bucket it was created.
		redis.call("EXPIRE", KEYS[1], windowSize * 2)
	end

	local previousCount = tonumber(redis.call("GET", KEYS[2]) or "0")
	local weight = 1 - (elapsed / windowSize)
	local effective = currentCount + previousCount * weight
	if effective > limit then
		return 0
	end
	return 1
`)

func NewRedisLimiter(client *redis.Client, window time.Duration, limit int) *RedisLimiter {
	return &RedisLimiter{
		client: client,
		window: window,
		limit:  limit,
		script: slidingWindowScript,
	}
}

func (l *RedisLimiter) Allow(ctx context.Context, key string) (bool, error) {
	now := time.Now()
	windowSize := int64(l.window.Seconds())
	nowUnix := now.Unix()
	currentWindowStart := (nowUnix / windowSize) * windowSize
	previousWindowStart := currentWindowStart - windowSize
	elapsed := nowUnix - currentWindowStart

	return l.script.Run(ctx, l.client,
		[]string{
			fmt.Sprintf("ratelimit:%s:%d", key, currentWindowStart),
			fmt.Sprintf("ratelimit:%s:%d", key, previousWindowStart),
		},
		windowSize,
		l.limit,
		elapsed,
	).Bool()
}
