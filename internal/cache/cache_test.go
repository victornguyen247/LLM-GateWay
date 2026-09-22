package cache

import (
	"context"
	"testing"
	"time"
)

func TestMemoryCache_SetAndGet(t *testing.T) {
	c, err := NewMemoryCache(10, time.Minute)
	if err != nil {
		t.Fatalf("NewMemoryCache: %v", err)
	}
	ctx := context.Background()

	want := Entry{
		Body:        []byte(`{"hello":"world"}`),
		ContentType: "application/json",
		Status:      200,
	}
	if err := c.Set(ctx, "k1", want); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, found, err := c.Get(ctx, "k1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !found {
		t.Fatal("expected hit, got miss")
	}
	if string(got.Body) != string(want.Body) {
		t.Errorf("body mismatch: got %q, want %q", got.Body, want.Body)
	}
	if got.ContentType != want.ContentType {
		t.Errorf("contentType mismatch: got %q, want %q", got.ContentType, want.ContentType)
	}
}

func TestMemoryCache_Miss(t *testing.T) {
	c, _ := NewMemoryCache(10, time.Minute)
	_, found, err := c.Get(context.Background(), "nope")
	if err != nil {
		t.Fatalf("expected clean miss, got err=%v", err)
	}
	if found {
		t.Error("expected miss for unknown key")
	}
}

func TestMemoryCache_Expiration(t *testing.T) {
	c, err := NewMemoryCache(10, time.Millisecond)
	if err != nil {
		t.Fatalf("NewMemoryCache: %v", err)
	}
	ctx := context.Background()

	if err := c.Set(ctx, "k1", Entry{Body: []byte("data"), ContentType: "text/plain", Status: 200}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	time.Sleep(5 * time.Millisecond)

	_, found, err := c.Get(ctx, "k1")
	if err != nil {
		t.Fatalf("expected clean miss after expiry, got err=%v", err)
	}
	if found {
		t.Error("expected miss after expiration")
	}
}

func TestHashRequest(t *testing.T) {
	body := []byte(`{"prompt":"hi"}`)

	h1 := HashRequest(body)
	h2 := HashRequest(body)
	if h1 != h2 {
		t.Errorf("hash not deterministic: %s vs %s", h1, h2)
	}
	if len(h1) != 64 {
		t.Errorf("expected 64-char sha256 hex, got %d", len(h1))
	}
	if HashRequest([]byte("different")) == h1 {
		t.Error("expected different hash for different input")
	}
}
