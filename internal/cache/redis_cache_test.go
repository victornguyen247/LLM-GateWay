package cache

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// Compile-time interface satisfaction. If either type drifts, the build breaks.
var (
	_ Cache = (*MemoryCache)(nil)
	_ Cache = (*RedisCache)(nil)
)

func newTestRedisCache(t *testing.T, ttl time.Duration) (*RedisCache, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	return NewRedisCache(client, ttl), mr
}

func TestRedisCache_HitAndMiss(t *testing.T) {
	c, _ := newTestRedisCache(t, time.Minute)
	ctx := context.Background()

	if _, found, err := c.Get(ctx, "nope"); found || err != nil {
		t.Errorf("expected clean miss, got found=%v err=%v", found, err)
	}

	want := Entry{
		Body:        []byte(`{"ok":true}`),
		ContentType: "application/json",
		Status:      200,
		Headers:     http.Header{"X-Custom": []string{"v"}},
	}
	if err := c.Set(ctx, "k1", want); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, found, err := c.Get(ctx, "k1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !found {
		t.Fatal("expected hit")
	}
	if string(got.Body) != string(want.Body) {
		t.Errorf("body: got %q, want %q", got.Body, want.Body)
	}
	if got.Status != want.Status {
		t.Errorf("status: got %d, want %d", got.Status, want.Status)
	}
	if got.ContentType != want.ContentType {
		t.Errorf("content-type: got %q, want %q", got.ContentType, want.ContentType)
	}
	if got.Headers.Get("X-Custom") != "v" {
		t.Errorf("header lost in roundtrip: %v", got.Headers)
	}
}

func TestRedisCache_Expiry(t *testing.T) {
	c, mr := newTestRedisCache(t, 10*time.Second)
	ctx := context.Background()

	if err := c.Set(ctx, "k1", Entry{Body: []byte("x"), Status: 200}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	mr.FastForward(20 * time.Second)

	if _, found, _ := c.Get(ctx, "k1"); found {
		t.Error("expected miss after TTL")
	}
}

func TestRedisCache_MultiValueHeader(t *testing.T) {
	c, _ := newTestRedisCache(t, time.Minute)
	ctx := context.Background()

	entry := Entry{
		Body:   []byte("x"),
		Status: 200,
		Headers: http.Header{
			"Set-Cookie": []string{"a=1", "b=2"},
		},
	}
	if err := c.Set(ctx, "k1", entry); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, _, _ := c.Get(ctx, "k1")
	cookies := got.Headers.Values("Set-Cookie")
	if len(cookies) != 2 {
		t.Fatalf("expected 2 Set-Cookie values, got %d: %v", len(cookies), cookies)
	}
}

func TestRedisCache_RedisDownIsMissNotPanic(t *testing.T) {
	c, mr := newTestRedisCache(t, time.Minute)
	ctx := context.Background()

	mr.Close() // simulate Redis outage

	_, found, err := c.Get(ctx, "k1")
	if found {
		t.Error("expected miss when Redis is down")
	}
	if err == nil {
		t.Error("expected error surfaced so middleware can log it")
	}
}