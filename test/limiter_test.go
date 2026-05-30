// curl -X POST http://localhost:8080/v1/chat/completions \
// -H 'Content-Type: application/json' \
// -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"say this is a test"}]}'

package main

import (
	"time"
	"testing"
	"github.com/victornguyen247/LLM-GateWay/internal/ratelimit"
)

//test the rate limiter

func TestRateLimiter(t *testing.T) {
	manager := ratelimit.NewManager(8, 3)
	count := 0
	for i := 0; i < 20; i++ {
		if ok :=manager.Get("test").Allow(); ok {
			count++
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Errorf("Expected %d requests, got %d", 20, count)
}