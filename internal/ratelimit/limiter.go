package ratelimit

import (
	"golang.org/x/time/rate"
	"sync"
)

// Manager is a rate limiter for the gateway
type Manager struct {
	mu sync.Mutex // protects the limiters map
	limiters map[string]*rate.Limiter // map of key to rate limiter
	rps float64 // requests per second
	burst int // burst limit
}

// NewManager creates a new rate limiter manager
func NewManager(rps float64, burst int) *Manager {
	return &Manager{
		mu: sync.Mutex{},
		limiters: make(map[string]*rate.Limiter),
		rps: rps,
		burst: burst,
	}
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