package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
)

// Cache is the interface all cache backends satisfy.
//
// Get returns (entry, true, nil) on hit, (Entry{}, false, nil) on miss,
// and (Entry{}, false, err) on backend error. Middleware treats err as a
// miss but should log it.
type Cache interface {
	Get(ctx context.Context, key string) (Entry, bool, error)
	Set(ctx context.Context, key string, entry Entry) error
}

// Entry is a cached response.
type Entry struct {
	Body        []byte
	ContentType string
	ExpiresAt   time.Time // Used by MemoryCache; ignored by RedisCache (Redis handles TTL).
	Status      int
	Headers     http.Header
}

// MemoryCache is an in-process LRU cache with per-entry TTL.
type MemoryCache struct {
	lru *lru.Cache[string, Entry]
	ttl time.Duration
}

func NewMemoryCache(size int, ttl time.Duration) (*MemoryCache, error) {
	if ttl <= 0 {
		return nil, errors.New("ttl must be positive")
	}
	l, err := lru.New[string, Entry](size)
	if err != nil {
		return nil, err
	}
	return &MemoryCache{lru: l, ttl: ttl}, nil
}

func (c *MemoryCache) Get(_ context.Context, key string) (Entry, bool, error) {
	val, exists := c.lru.Get(key)
	if !exists {
		return Entry{}, false, nil
	}
	if time.Now().After(val.ExpiresAt) {
		c.lru.Remove(key)
		return Entry{}, false, nil
	}
	return val, true, nil
}

func (c *MemoryCache) Set(_ context.Context, key string, entry Entry) error {
	entry.ExpiresAt = time.Now().Add(c.ttl)
	c.lru.Add(key, entry)
	return nil
}

// HashRequest returns the hex-encoded SHA-256 of body.
func HashRequest(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
