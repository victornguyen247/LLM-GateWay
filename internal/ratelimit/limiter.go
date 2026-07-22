package ratelimit

import (
	"golang.org/x/time/rate"
	"sync"
	"context"
	"time"
)

// Manager is a rate limiter for the gateway
type Manager struct {
	mu sync.Mutex // protects the limiters map
	limiters map[string]*rate.Limiter // map of key to rate limiter
	rps float64 // requests per second
	burst int // burst limit
}

type Limiter interface {
	Allow(ctx context.Context, key string) (allowed bool, err error)
}

// Get returns a rate limiter for the given key or creates a new one if it doesn't exist
func (m *Manager) Get(key string) *rate.Limiter {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exist := m.limiters[key]; !exist {
		m.limiters[key] = rate.NewLimiter(rate.Limit(m.rps), m.burst)
		return m.limiters[key]
	}
	return m.limiters[key]
}

func (m *Manager) Allow(ctx context.Context, key string) (allowed bool, err error) {
	return m.Get(key).Allow(ctx), nil
}

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
	redis *redis.Client
	window time.Duration
	limit int
}

func newRedisLimiter(redis *redis.Client, window time.Duration, limit int) *RedisLimiter {
	return &RedisLimiter{
		redis: redis,
		window: window,
		limit: limit,
	}
}
