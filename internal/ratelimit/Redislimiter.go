package ratelimit

import (
	"context"
	"time"
	"fmt"

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
// Design (pseudocode):
//   1. windowSize := int64(window.Seconds())
//   2. nowUnix := time.Now().Unix()
//   3. currentWindowStart := (nowUnix / windowSize) * windowSize
//      previousWindowStart := currentWindowStart - windowSize
//   4. currentKey  := "ratelimit:{key}:{currentWindowStart}"
//      previousKey := "ratelimit:{key}:{previousWindowStart}"
//   5. currentCount := INCR currentKey
//      if currentCount == 1 { EXPIRE currentKey, 2*windowSize }
//      (TTL = 2x window size: key must survive as both "current" and,
//      one bucket later, "previous" window, regardless of when within
//      its own bucket it was created.)
//   6. previousCount := GET previousKey (0 if missing/expired)
//   7. elapsed := nowUnix - currentWindowStart
//      weight := 1 - float64(elapsed)/float64(windowSize)
//   8. effectiveCount := float64(currentCount) + float64(previousCount)*weight
//   9. return effectiveCount <= float64(limit), nil
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
		local currentCount = redis.call("INCR", KEYS[1])
		if currentCount == 1 then
			redis.call("EXPIRE", KEYS[1], ARGV[1] * 2) -- 2x window size: key must survive as both "current" and, one bucket later, "previous" window, regardless of when within its own bucket it was created.
		end
		local previousCount = tonumber(redis.call("GET", KEYS[2]) or '0')
		local weight = currentCount + previousCount * (1 - ARGV[3] / ARGV[1])
		if weight > tonumber(ARGV[2]) then
			return 0
		else
			return 1
		end
	`)

func NewRedisLimiter(client *redis.Client, window time.Duration, limit int) *RedisLimiter {
	return &RedisLimiter{
		client: client,
		window: window,
		limit:  limit,
		script: slidingWindowScript}
}

func (l *RedisLimiter) Allow(ctx context.Context, key string) (bool, error) {
	now := time.Now()
	windowSize := int64(l.window.Seconds())
	nowUnix := now.Unix()
	currentWindowStart := int64(nowUnix / windowSize) * windowSize
	previousWindowStart := currentWindowStart - windowSize
	elapsed := nowUnix - currentWindowStart

	result, err := l.script.Run(ctx, l.client, 
		[]string{
		fmt.Sprintf("ratelimit:%s:%d", key, currentWindowStart),
		fmt.Sprintf("ratelimit:%s:%d", key, previousWindowStart)},
		[]interface{}{
			windowSize,
			l.limit,
			elapsed,
		}...,
	).Bool()
	return result, err
}


