package ratelimit

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTestLimiter(t *testing.T, window time.Duration, limit int) (*RedisLimiter, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}
	t.Cleanup(mr.Close)

	client := redis.NewClient(&redis.Options{
		Addr:       mr.Addr(),
		MaxRetries: -1,
	})
	t.Cleanup(func() { client.Close() })

	return NewRedisLimiter(client, window, limit), mr
}

// waitForFreshWindow blocks until the wall clock is close to the start of a
// new window, so a test can rely on elapsed-since-window-start being small.
func waitForFreshWindow(window time.Duration) {
	windowSize := int64(window.Seconds())
	for time.Now().Unix()%windowSize > windowSize/10 {
		time.Sleep(20 * time.Millisecond)
	}
}

func TestRedisLimiter_Allow(t *testing.T) {
	t.Run("allows requests under the limit and denies once exceeded", func(t *testing.T) {
		limiter, _ := newTestLimiter(t, 10*time.Second, 3)
		ctx := context.Background()

		for i := 0; i < 3; i++ {
			allowed, err := limiter.Allow(ctx, "key-a")
			if err != nil {
				t.Fatalf("request %d: unexpected error: %v", i, err)
			}
			if !allowed {
				t.Fatalf("request %d: expected allowed, got denied", i)
			}
		}

		allowed, err := limiter.Allow(ctx, "key-a")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if allowed {
			t.Fatal("expected 4th request to be denied")
		}
	})

	t.Run("different keys are tracked independently", func(t *testing.T) {
		limiter, _ := newTestLimiter(t, 10*time.Second, 1)
		ctx := context.Background()

		if allowed, err := limiter.Allow(ctx, "key-b"); err != nil || !allowed {
			t.Fatalf("key-b: allowed=%v err=%v, want allowed", allowed, err)
		}
		if allowed, err := limiter.Allow(ctx, "key-c"); err != nil || !allowed {
			t.Fatalf("key-c: allowed=%v err=%v, want allowed", allowed, err)
		}
	})

	t.Run("weight math: previous window contribution decays across the boundary", func(t *testing.T) {
		window := 2 * time.Second
		limit := 5
		limiter, _ := newTestLimiter(t, window, limit)
		ctx := context.Background()
		key := "key-weight"

		waitForFreshWindow(window)
		windowSize := int64(window.Seconds())
		currentStart := (time.Now().Unix() / windowSize) * windowSize
		previousKey := fmt.Sprintf("ratelimit:%s:%d", key, currentStart-windowSize)

		// Seed the previous window with a count that, weighted at near-full
		// strength (elapsed ~ 0), pushes the effective count over the limit.
		if err := limiter.client.Set(ctx, previousKey, 5, window*2).Err(); err != nil {
			t.Fatalf("failed to seed previous window: %v", err)
		}

		// Near the start of the window: effective = 1(current) + 5*(~1) > 5 -> denied.
		allowed, err := limiter.Allow(ctx, key)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if allowed {
			t.Fatal("expected request near window start to be denied by the near-full-weight previous count")
		}

		// Sleep most of the way through the window so the previous count's
		// weight decays toward zero: effective = 2(current) + 5*(~0) <= 5 -> allowed.
		time.Sleep(time.Duration(float64(window) * 0.8))
		allowed, err = limiter.Allow(ctx, key)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !allowed {
			t.Fatal("expected request near window end to be allowed once the previous window's weight decays")
		}
	})
}

func TestRedisLimiter_Allow_RedisDown(t *testing.T) {
	limiter, mr := newTestLimiter(t, 10*time.Second, 3)
	mr.Close()

	if _, err := limiter.Allow(context.Background(), "key-down"); err == nil {
		t.Fatal("expected an error when redis is unreachable, got nil")
	}
}
