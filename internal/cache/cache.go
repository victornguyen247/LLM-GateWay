package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"
	"github.com/hashicorp/golang-lru/v2"
	"net/http"
)

// Entry is a struct that represents an entry in the cache
type Entry struct {
	Body []byte // the body of the entry
	ContentType string // the content type of the entry
	ExpiresAt time.Time // the expiration time of the entry
	Status int // the status code of the entry
	Headers http.Header // the headers of the entry
}

// Cache is a thread-safe in-memory cache
type Cache struct {
	lru *lru.Cache[string,Entry] // lru cache to store the entries
	ttl time.Duration // TTL for the entries
}

// NewCache creates a new cache
func NewCache(size int, ttl time.Duration) (*Cache, error) {
	// create a new lru cache
	lru, err := lru.New[string,Entry](size)
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
func (c *Cache) Get(key string) (Entry, bool, error) {
	if val, exists := c.lru.Get(key); exists {
		// check if the entry is expired
		if time.Now().After(val.ExpiresAt) {
			c.lru.Remove(key)
			return Entry{}, false, errors.New("entry expired")
		}
		return val, true, nil
	}
	return Entry{}, false, errors.New("entry not found")
}

// Set adds an entry to the cache
func (c *Cache) Set(key string, entry Entry) error {
	// check if the TTL is valid
	if c.ttl > 0 {
		// add the entry to the cache
		entry.ExpiresAt = time.Now().Add(c.ttl)
		if ok := c.lru.Add(key, entry); !ok {
			return errors.New("failed to add to cache")
		}
		return nil
	}
	// return an error if the TTL is not valid
    return errors.New("ttl is not valid")
}

// HashRequest hashes the request body using SHA-256 and returns the hex encoded string
func HashRequest(body []byte) string {
	// create a new SHA-256 hash
	hash := sha256.New()
	// write the body to the hash
	hash.Write(body)
	// encode the hash to a hex string
	return hex.EncodeToString(hash.Sum(nil))
}