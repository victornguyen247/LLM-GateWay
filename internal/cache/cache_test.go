package cache

import (
	"testing"
	"time"
)

func TestCache_SetAndGet(t *testing.T) {
	c, err := NewCache(10, time.Minute)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	want := entry{
		body:        []byte(`{"hello":"world"}`),
		contentType: "application/json",
	}
	c.Set("k1", want)

	got, ok := c.Get("k1")
	if !ok {
		t.Fatal("expected hit, got miss")
	}
	if string(got.body) != string(want.body) {
		t.Errorf("body mismatch: got %q, want %q", got.body, want.body)
	}
	if got.contentType != want.contentType {
		t.Errorf("contentType mismatch: got %q, want %q", got.contentType, want.contentType)
	}
}

func TestCache_Miss(t *testing.T) {
	c, _ := NewCache(10, time.Minute)
	if _, ok := c.Get("nope"); ok {
		t.Error("expected miss for unknown key")
	}
}

func TestCache_Expiration(t *testing.T) {
	c, err := NewCache(10, 1*time.Nanosecond)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	c.Set("k1", entry{body: []byte("data"), contentType: "text/plain"})
	time.Sleep(5 * time.Millisecond) // well past the 1ns TTL

	if _, ok := c.Get("k1"); ok {
		t.Error("expected miss after expiration")
	}
}

func TestHashRequest(t *testing.T) {
	body := []byte(`{"prompt":"hi"}`)

	h1 := hashRequest(body)
	h2 := hashRequest(body)
	if h1 != h2 {
		t.Errorf("hash not deterministic: %s vs %s", h1, h2)
	}
	if len(h1) != 64 {
		t.Errorf("expected 64-char sha256 hex, got %d", len(h1))
	}
	if hashRequest([]byte("different")) == h1 {
		t.Error("expected different hash for different input")
	}
}