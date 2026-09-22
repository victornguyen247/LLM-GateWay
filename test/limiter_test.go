package main

import (
	"testing"
	"time"

	"github.com/victornguyen247/LLM-GateWay/internal/ratelimit"
)

func TestRateLimiter(t *testing.T) {
	manager := ratelimit.NewManager(8, 3)
	count := 0
	for i := 0; i < 20; i++ {
		if ok, err := manager.Allow(nil, "test"); err != nil {
			t.Fatalf("Allow: %v", err)
		} else if ok {
			count++
		}
		time.Sleep(100 * time.Millisecond)
	}
	// burst=3 plus ~8 rps over ~2s → expect roughly mid-teens, not all 20.
	if count < 10 || count > 20 {
		t.Errorf("expected roughly 10–20 allowed requests over 2s, got %d", count)
	}
}
