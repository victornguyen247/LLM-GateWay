# Phase B — Redis-Backed Shared Cache

## 🎯 Missions in Phase B (at a glance)

By the end of this phase, you will have:

- [ ] **B1** — Extracted a `Cache` interface (mirrors the `Limiter` interface pattern from Phase A)
- [ ] **B2** — Fixed two latent bugs in the existing cache middleware
- [ ] **B3** — Implemented `RedisCache`: a Redis-backed cache that satisfies the `Cache` interface, keyed by SHA-256, with TTL via `SET ... EX`
- [ ] **B4** — Wired `CACHE_BACKEND=memory|redis` selection in `main.go`
- [ ] **B5** — Added tests against `miniredis` covering hit / miss / expiry / serialization / Redis-down

**End state:** the gateway can run with either in-memory OR Redis cache, chosen by env var. When two pods run against the same Redis, a cache hit on pod A serves a request that arrived at pod B. This is the direct analog of what Phase A did for rate limiting.

---

## Big picture: why Phase B matters

Your v1 cache is `hashicorp/golang-lru` — an in-memory LRU. It works, but it has one fatal flaw for a K8s deployment: **each pod has its own cache**. Pod A caches a response; pod B sees the identical request and hits the upstream anyway. At 3 replicas, your effective cache hit rate roughly drops to 1/3 of what it should be.

Redis fixes this the same way it fixed rate limiting in Phase A: **shared state across pods**. Every pod queries the same Redis; a hit anywhere is a hit everywhere.

Phase B also cements the architectural discipline you established in Phase A:

- **Interface extraction** (`Cache` interface, not concrete `*cache.Cache` everywhere)
- **Backend selection via env var** (`CACHE_BACKEND=memory|redis`)
- **Failure policy at the middleware layer**, not swallowed inside the cache

You already lived through this pattern for rate limiting. Phase B is that muscle memory getting a second rep — the design decisions from A carry over almost verbatim.

**Two important design notes before you start:**

1. **Fail policy for cache differs from rate limiting.** For rate limiting, "fail-open" means "let the request through." For cache, the natural failure is *cache miss* — if Redis is down, treat every request as a miss and go to upstream. This is effectively fail-open, but you get it for free from the semantics; you don't need a separate policy. You do still want the underlying error *visible* (logged), same principle as Phase A: never silently swallow Redis errors.

2. **Serialization matters now.** The in-memory cache stores `Entry` structs directly. Redis stores strings/bytes. You'll need to marshal `Entry → bytes` on `Set`, and unmarshal `bytes → Entry` on `Get`. JSON is fine here; the `http.Header` type is `map[string][]string`, which JSON handles natively, and `[]byte` roundtrips as base64 automatically.

---

## Subtask B1 — Extract the Cache interface

### What + why
Right now, `Cache` is a concrete struct in `internal/cache/cache.go`, and `CacheMiddleware` accepts `*cache.Cache`. That's the exact blocker you hit in Phase A with `NewServer` taking a concrete `*ratelimit.Manager`. Fix it the same way: extract an interface, rename the concrete type, update the middleware.

### How-to
1. In `internal/cache/cache.go`, define an interface near the top of the file:
   ```go
   type Cache interface {
       Get(ctx context.Context, key string) (Entry, bool, error)
       Set(ctx context.Context, key string, entry Entry) error
   }
   ```
   The `error` return on `Get` is deliberate — we want to distinguish a normal miss (`nil` error, `false` found) from an underlying failure like a Redis outage. The middleware can then log the error and still treat it as a miss.
2. Rename the existing `Cache` struct → `MemoryCache`. Rename `NewCache` → `NewMemoryCache` returning `*MemoryCache`.
3. Update `MemoryCache.Get` and `MemoryCache.Set` to match the new interface signature (add `ctx context.Context` — for the memory backend you'll ignore it, but the signature must match).
4. In `internal/server/middleware.go`, change `func CacheMiddleware(c *cache.Cache)` → `func CacheMiddleware(c cache.Cache)`.
5. In `internal/server/server.go`, change the `cache` field type from `*cache.Cache` → `cache.Cache`.
6. `go build ./...` — should compile cleanly.

### 🔍 Check-in question
Look at your `Limiter` interface (`Allow(ctx, key) (bool, error)`) vs the new `Cache` interface (`Get(ctx, key) (Entry, bool, error)` and `Set(ctx, key, entry) error`). Both take `context.Context`. If you were writing the memory backend and you knew you'd *never* need cancellation, would you still put `ctx` in the signature? Why does keeping it there anyway pay off later? Give it 60 seconds before moving on.

---

## Subtask B2 — Fix two latent bugs in cache middleware

### What + why
While you're already in the middleware for B1, clear two real bugs. They don't crash the gateway but they quietly break cache correctness. Fixing them now is cheaper than debugging weird behavior three phases from now.

### The bugs

**Bug 1: `Content-Type` comes from the request, not the response.**

Current code:
```go
c.Set(key, cache.Entry{
    Body:        bufw.buf.Bytes(),
    ContentType: r.Header.Get("Content-Type"),  // ← WRONG: this is the request's CT
    ...
})
```
The request's `Content-Type` is what the *client* sent (usually `application/json` because they sent a JSON body). What you actually want to cache is what the *upstream* sent back — that's what you'll replay to the next client on a cache hit. Since you already have `bufw`, use `bufw.Header().Get("Content-Type")`.

**Bug 2: Header ordering on the cache-hit branch.**

Current code:
```go
if found {
    w.WriteHeader(entry.Status)                       // headers frozen here
    w.Header().Set("Content-Type", entry.ContentType) // silently ignored!
    ...
}
```
`WriteHeader` freezes the response headers. Any `Set` after it is silently dropped. Fix: set all headers *first*, then `WriteHeader`, then `Write` the body.

### How-to
1. In `internal/server/middleware.go`, in the cache-miss branch (`c.Set(...)`), change `r.Header.Get("Content-Type")` to `bufw.Header().Get("Content-Type")`.
2. In the cache-hit branch, reorder:
   - all `w.Header().Set(...)` / `w.Header().Add(...)` calls FIRST
   - then `w.WriteHeader(entry.Status)`
   - then `w.Write(entry.Body)`
3. While you're there: the `X-Cache: HIT` / `X-Cache: MISS` header you set on `w` in the miss path goes out before `next.ServeHTTP` writes its own headers — that's fine, but only because you're using `bufWriter`. Make sure you understand *why* it's safe (hint: `bufWriter.WriteHeader` calls the underlying `w.ResponseWriter.WriteHeader`, so by the time upstream headers land, yours are already staged).

### 🔍 Check-in question
In the cache-hit path after your reorder: if `entry.Headers` also contains a `Content-Type` value (it probably does, since you cloned the response headers on `Set`), and you *also* separately `Set("Content-Type", entry.ContentType)`, do you have a duplicate, an override, or a merge? Which behavior do you actually want, and which of `http.Header.Set` vs `http.Header.Add` gives you it?

---

## Subtask B3 — Implement RedisCache

### What + why
Now the real work: a `Cache` implementation backed by Redis. The interface is set from B1, so you just need a type that satisfies it. Serialization is the only genuinely new problem — Redis stores strings/bytes, so `Entry` needs to marshal in and out.

### How-to

**Step 3a — Set up the file and struct.**
1. Create `internal/cache/redis_cache.go`.
2. Define:
   ```go
   type RedisCache struct {
       client *redis.Client
       ttl    time.Duration
   }

   func NewRedisCache(client *redis.Client, ttl time.Duration) *RedisCache { ... }
   ```
3. Note that `RedisCache` doesn't take a `size` parameter — Redis handles its own eviction via `maxmemory-policy` (typically `allkeys-lru` in production). This is a real difference in mental model from the LRU: with Redis, you're a tenant of a memory budget the operator controls, not the sole owner of a Go map.

**Step 3b — Serialize `Entry`.**
1. `Entry` has fields `Body []byte`, `ContentType string`, `ExpiresAt time.Time`, `Status int`, `Headers http.Header`. All JSON-encodable.
2. Fields must be **exported** (capital first letter) for `encoding/json` to see them. Check `cache.go` — they already are.
3. You can either add a package-level `marshalEntry` / `unmarshalEntry` helper or just call `json.Marshal` / `json.Unmarshal` inline. The former is cleaner if you later add gob/msgpack; the latter is fine for now.

**Step 3c — `Get` method.**
1. Signature: `func (c *RedisCache) Get(ctx context.Context, key string) (Entry, bool, error)`.
2. Namespace the key: `redisKey := "cache:" + key`. (Consistent with your `ratelimit:` prefix from Phase A — critical so a shared Redis instance never sees the two keyspaces collide.)
3. Call `c.client.Get(ctx, redisKey).Bytes()`.
4. Three cases:
   - `errors.Is(err, redis.Nil)` → clean miss: `return Entry{}, false, nil`
   - Other `err != nil` → real error: `return Entry{}, false, err` (middleware decides how loudly to log)
   - Success → unmarshal, return `entry, true, nil`
5. This is the `redis.Nil` idiom your memory notes already flag — the whole reason you added `error` to the interface signature was to make this distinction possible.

**Step 3d — `Set` method.**
1. Signature: `func (c *RedisCache) Set(ctx context.Context, key string, entry Entry) error`.
2. Marshal the entry to bytes.
3. Call `c.client.Set(ctx, "cache:"+key, data, c.ttl).Err()`.
4. The `time.Duration` argument to `Set` maps directly to `SET key value EX seconds` — Redis-side TTL. You do **not** need to set `entry.ExpiresAt` for the Redis path; Redis handles expiration itself. Leave the field zero-valued, and let `MemoryCache` be the one that uses it.
5. Return the error — don't swallow it. Middleware decides what to do.

### 🔍 Check-in question
You're storing responses under `cache:<sha256>`. Your rate-limit keys look like `ratelimit:<key>:<window>`. If a customer accidentally deploys *two* gateway environments (staging and prod) sharing one Redis instance, is there any way keys could collide across environments? What's the smallest change that would make cross-environment collisions structurally impossible? (Answer isn't "add a prefix" — you already have one. Think broader — think about Redis features.)

---

## Subtask B4 — Wire CACHE_BACKEND selection in main.go

### What + why
The whole point of the interface was to swap backends via config. Now do it. This is the direct mirror of what Phase A did for `RATE_LIMIT_BACKEND`.

### How-to
1. In `main.go`, read config via env vars, with defaults:
   - `CACHE_BACKEND` (default `"memory"`)
   - `CACHE_SIZE` (default `1000`) — only used for memory backend
   - `CACHE_TTL` (default `1h`, parsed with `time.ParseDuration`)
   - `REDIS_URL` (default `"redis://localhost:6379"`) — only used if any backend is `redis`
2. Construct the Redis client **once** if either `RATE_LIMIT_BACKEND` or `CACHE_BACKEND` is `redis`. Don't open two connections. Ping it on startup and exit early if it fails — a misconfigured Redis should fail loudly at boot, not on the first request.
3. Switch on `CACHE_BACKEND`:
   ```go
   var c cache.Cache
   switch cacheBackend {
   case "memory", "":
       mc, err := cache.NewMemoryCache(cacheSize, cacheTTL)
       if err != nil { ... exit ... }
       c = mc
   case "redis":
       c = cache.NewRedisCache(redisClient, cacheTTL)
   default:
       logger.Error("unknown CACHE_BACKEND", "value", cacheBackend); os.Exit(1)
   }
   ```
4. Pass `c` (interface value, not concrete) into `NewServer`.

### 🔍 Check-in question
If Phase A's `RATE_LIMIT_BACKEND=redis` needs a Redis client and Phase B's `CACHE_BACKEND=redis` needs one, and both are configured independently, what happens if only ONE is set to `redis`? Should you construct the client conditionally, or unconditionally? Which is cleaner from an operator's perspective — the person who has to read a `REDIS_URL is required` error message at 2am?

---

## Subtask B5 — Tests with miniredis

### What + why
Same tool you used for Phase A. Fast, in-process Redis; no Docker required for tests. Table-driven where it helps, focused single-purpose tests where the setup varies.

### How-to
1. Create `internal/cache/redis_cache_test.go`.
2. Standard miniredis helper:
   ```go
   func newTestRedisCache(t *testing.T, ttl time.Duration) (*RedisCache, *miniredis.Miniredis) {
       t.Helper()
       mr := miniredis.RunT(t)
       client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
       return NewRedisCache(client, ttl), mr
   }
   ```
3. Cover these cases:
   - **Hit / miss** — Set then Get returns the entry; Get on unknown key returns `_, false, nil`
   - **Roundtrip fidelity** — body, status, content-type, and headers all survive Marshal→Redis→Unmarshal
   - **Multi-value header** — `Set-Cookie: a=1` + `Set-Cookie: b=2` both survive (this is where naive marshaling breaks — verify it doesn't for you)
   - **Expiry** — Set with TTL, `mr.FastForward(2 * ttl)`, Get returns miss
   - **Redis down** — `mr.Close()`, Get returns `_, false, err` with a non-nil error (does NOT panic)
4. Add compile-time interface satisfaction checks at package level in a test file:
   ```go
   var _ Cache = (*MemoryCache)(nil)
   var _ Cache = (*RedisCache)(nil)
   ```
   If either type ever drifts from the interface, the build breaks — free protection, no runtime cost.

### 🔍 Check-in question
`MemoryCache` stores `Entry` values directly; `RedisCache` marshals through JSON. Is there any data that would roundtrip fine through `MemoryCache` but get corrupted or lost through JSON? Think about: `time.Time` precision, `nil` vs empty maps/slices, header case sensitivity (does `http.Header` treat "Content-Type" and "content-type" as the same key after JSON roundtrip?), and what happens to `[]byte` fields containing invalid UTF-8. Which of these actually matter for your use case?

---

## Wrapping up Phase B

Before you close the phase:

1. Run the gateway locally with `CACHE_BACKEND=memory`, hit `/v1/chat/completions` twice with the same body, confirm the second returns `X-Cache: HIT`.
2. Run it with `CACHE_BACKEND=redis` against local Redis (`docker compose up redis`), repeat. Verify Redis actually has the key: `redis-cli KEYS 'cache:*'`.
3. Update `gateway_progress_tracker.md`: move Phase B items to done, note actual pace vs your budget, add lessons.

**Phase C is next**: deploy two replicas of the gateway to kind, prove cache state is *actually* shared across pods. That's where the payoff shows up — a request that lands on pod B returns a cached response created by pod A. That's the moment v2 justifies its existence.

---

## 📖 Final reference: complete code

> **Attempt each subtask first.** The whole point of the guided flow is that you write it, then compare. Reading the reference before trying makes the review-my-code work in Phase C much less useful — you'll pattern-match to what you saw here instead of grappling with the tradeoffs yourself.

### `internal/cache/cache.go`

```go
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

// Entry is a cached response.
type Entry struct {
	Body        []byte      `json:"body"`
	ContentType string      `json:"content_type"`
	ExpiresAt   time.Time   `json:"expires_at"` // Used by MemoryCache; ignored by RedisCache (Redis handles TTL).
	Status      int         `json:"status"`
	Headers     http.Header `json:"headers"`
}

// Cache is the interface all cache backends satisfy.
//
// Get returns (entry, true, nil) on hit, (Entry{}, false, nil) on miss,
// and (Entry{}, false, err) on backend error. Middleware treats err as a
// miss but should log it — a silent Redis outage is exactly the kind of
// invisibility that made "fail-open at the middleware layer" a rule.
type Cache interface {
	Get(ctx context.Context, key string) (Entry, bool, error)
	Set(ctx context.Context, key string, entry Entry) error
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
```

### `internal/cache/redis_cache.go` (new)

```go
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

const redisKeyPrefix = "cache:"

// RedisCache is a Redis-backed cache. TTL is enforced Redis-side via SET ... EX.
type RedisCache struct {
	client *redis.Client
	ttl    time.Duration
}

func NewRedisCache(client *redis.Client, ttl time.Duration) *RedisCache {
	return &RedisCache{client: client, ttl: ttl}
}

func (c *RedisCache) Get(ctx context.Context, key string) (Entry, bool, error) {
	data, err := c.client.Get(ctx, redisKeyPrefix+key).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return Entry{}, false, nil // clean miss
		}
		return Entry{}, false, err // real error — surface for logging
	}
	var entry Entry
	if err := json.Unmarshal(data, &entry); err != nil {
		return Entry{}, false, err
	}
	return entry, true, nil
}

func (c *RedisCache) Set(ctx context.Context, key string, entry Entry) error {
	if c.ttl <= 0 {
		return errors.New("ttl must be positive")
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	return c.client.Set(ctx, redisKeyPrefix+key, data, c.ttl).Err()
}
```

### `internal/server/middleware.go` (bugs fixed, accepts interface)

Only the cache middleware is shown; the rest of the file (bufWriter, RateLimitMiddleware) is unchanged.

```go
func CacheMiddleware(c cache.Cache) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "Failed to read request body", http.StatusInternalServerError)
				return
			}
			defer r.Body.Close()
			r.Body = io.NopCloser(bytes.NewBuffer(body))

			// Streaming responses aren't cacheable. Substring match is loose —
			// good enough for Phase B; tighten later if false positives show up.
			if bytes.Contains(body, []byte(`"stream":true`)) ||
				bytes.Contains(body, []byte(`"stream": true`)) {
				next.ServeHTTP(w, r)
				return
			}

			keyBytes := []byte(r.Method + ":" + r.URL.Path + ":" + string(body))
			key := cache.HashRequest(keyBytes)

			entry, found, err := c.Get(r.Context(), key)
			if err != nil {
				// Fail-open: treat as miss, but log so Redis outages don't hide.
				slog.Default().Warn("cache get failed, treating as miss", "error", err)
			}
			if found {
				// Set ALL headers first — WriteHeader freezes them.
				for k, values := range entry.Headers {
					for _, v := range values {
						w.Header().Add(k, v)
					}
				}
				// Overwrite Content-Type explicitly in case Headers didn't carry it.
				if entry.ContentType != "" {
					w.Header().Set("Content-Type", entry.ContentType)
				}
				w.Header().Set("X-Cache", "HIT")
				w.WriteHeader(entry.Status)
				_, _ = w.Write(entry.Body)
				return
			}

			// Cache miss: capture upstream response into bufWriter, then persist.
			w.Header().Set("X-Cache", "MISS")
			bufw := &bufWriter{ResponseWriter: w}
			next.ServeHTTP(bufw, r)

			if bufw.status >= 200 && bufw.status < 300 {
				setErr := c.Set(r.Context(), key, cache.Entry{
					Body:        bufw.buf.Bytes(),
					ContentType: bufw.Header().Get("Content-Type"), // FIX: was r.Header
					Status:      bufw.status,
					Headers:     bufw.Header().Clone(),
				})
				if setErr != nil {
					slog.Default().Warn("cache set failed", "error", setErr)
				}
			}
		})
	}
}
```

### `cmd/gateway/main.go` (backend selection)

Assumes Phase A completed: `ratelimit.Limiter` interface exists, `ratelimit.NewRedisLimiter` is real, `server.NewServer` accepts `ratelimit.Limiter` and `cache.Cache` interfaces.

```go
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/victornguyen247/LLM-GateWay/internal/cache"
	"github.com/victornguyen247/LLM-GateWay/internal/proxy"
	"github.com/victornguyen247/LLM-GateWay/internal/ratelimit"
	"github.com/victornguyen247/LLM-GateWay/internal/server"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger) // middleware's slog.Default() picks this up

	rps := envFloat("RATE_LIMIT_RPS", 2.0)
	burst := envInt("RATE_LIMIT_BURST", 2)
	rateLimitBackend := envString("RATE_LIMIT_BACKEND", "memory")

	cacheBackend := envString("CACHE_BACKEND", "memory")
	cacheSize := envInt("CACHE_SIZE", 1000)
	cacheTTL := envDuration("CACHE_TTL", 1*time.Hour)

	// One Redis client, shared by any backend that wants it. Fail loudly at boot
	// so misconfiguration doesn't surface on the first request at 2am.
	var redisClient *redis.Client
	if rateLimitBackend == "redis" || cacheBackend == "redis" {
		redisURL := envString("REDIS_URL", "redis://localhost:6379")
		opts, err := redis.ParseURL(redisURL)
		if err != nil {
			logger.Error("invalid REDIS_URL", "error", err)
			os.Exit(1)
		}
		redisClient = redis.NewClient(opts)
		if err := redisClient.Ping(context.Background()).Err(); err != nil {
			logger.Error("redis ping failed", "error", err)
			os.Exit(1)
		}
	}

	var c cache.Cache
	switch cacheBackend {
	case "memory", "":
		mc, err := cache.NewMemoryCache(cacheSize, cacheTTL)
		if err != nil {
			logger.Error("failed to create memory cache", "error", err)
			os.Exit(1)
		}
		c = mc
	case "redis":
		c = cache.NewRedisCache(redisClient, cacheTTL)
	default:
		logger.Error("unknown CACHE_BACKEND", "value", cacheBackend)
		os.Exit(1)
	}

	var lim ratelimit.Limiter
	switch rateLimitBackend {
	case "memory", "":
		lim = ratelimit.NewManager(rps, burst)
	case "redis":
		lim = ratelimit.NewRedisLimiter(redisClient, 1*time.Second, int(rps))
	default:
		logger.Error("unknown RATE_LIMIT_BACKEND", "value", rateLimitBackend)
		os.Exit(1)
	}

	upstreamURL := envString("OPENAI_UPSTREAM_URL", "https://api.openai.com")
	listen := envString("GATEWAY_LISTEN", ":8080")

	s := server.NewServer(
		listen,
		logger,
		proxy.NewOpenAIProxy(upstreamURL, os.Getenv("OPENAI_API_KEY"), logger),
		lim,
		c,
	)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := s.Run(); err != nil {
			logger.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown error", "error", err)
	}
	logger.Info("gracefully shut down")
}

func envString(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
func envFloat(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil {
			return n
		}
	}
	return def
}
func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
```

### `internal/cache/redis_cache_test.go` (new)

```go
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
```
