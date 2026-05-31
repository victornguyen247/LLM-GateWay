package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"
	"github.com/hashicorp/golang-lru/v2"
)

// Entry is a struct that represents an entry in the cache
type entry struct {
	body []byte // the body of the entry
	contentType string // the content type of the entry
	expiresAt time.Time // the expiration time of the entry
}

// Cache is a thread-safe in-memory cache
type Cache struct {
	lru *lru.Cache[string,entry] // lru cache to store the entries
	ttl time.Duration // TTL for the entries
}

// NewCache creates a new cache
func NewCache(size int, ttl time.Duration) (*Cache, error) {
	// create a new lru cache
	lru, err := lru.New[string,entry](size)
	if err != nil {
		return nil, err
	}
	// return a new cache
	return &Cache{
		lru: lru,
		ttl: ttl,
	}, nil
}

// Get retrieves an entry from the cache
func (c *Cache) Get(key string) (entry, bool) {
	if val, exists := c.lru.Get(key); exists {
		// check if the entry is expired
		if time.Now().After(val.expiresAt) {
			c.lru.Remove(key)
			return entry{}, false
		}
		return val, true
	}
	return entry{}, false
}

// Set adds an entry to the cache
func (c *Cache) Set(key string, entry entry) error {
	// check if the TTL is valid
	if c.ttl > 0 {
		// add the entry to the cache
		entry.expiresAt = time.Now().Add(c.ttl)
		if ok := c.lru.Add(key, entry); !ok {
			return errors.New("failed to add to cache")
		}
		return nil
	}
	// return an error if the TTL is not valid
    return errors.New("ttl is not valid")
}

// HashRequest hashes the request body using SHA-256 and returns the hex encoded string
func hashRequest(body []byte) string {
	// create a new SHA-256 hash
	hash := sha256.New()
	// write the body to the hash
	hash.Write(body)
	// encode the hash to a hex string
	return hex.EncodeToString(hash.Sum(nil))
}