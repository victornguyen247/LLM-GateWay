# internal/ — Code Conventions

Conventions that apply across every package under `internal/`. Root project context lives in the top-level `CLAUDE.md`; this file focuses on how code inside `internal/` is structured.

## Interface extraction pattern

Every package with (or planning to have) multiple backends follows the same three-part shape:

1. **Interface declared in the package** — `Limiter`, `Cache`, `Proxy`
2. **Concrete implementations as separate types** — `Manager` + `RedisLimiter`, `MemoryCache` + `RedisCache`, `OpenAIProxy` + `GeminiProxy`
3. **Consumers accept the interface, not the concrete** — `NewServer` takes `ratelimit.Limiter` and `cache.Cache`, not `*ratelimit.Manager` and `*cache.MemoryCache`

Widen signatures early. Retrofitting an interface after `NewServer` takes a concrete type was a real Phase A blocker (the `Limiter` interface existed but `NewServer` was still typed against `*Manager`). Every new package here follows this from the first commit.

## Context on every backend method

Even backends that can't use `ctx` (the in-memory rate limiter, the in-memory cache) keep it in the signature. Redis-backed implementations need it for cancellation and deadlines. Removing `ctx` "because this backend doesn't need it" means every call site changes when the next backend does.

```go
// Right — signature accommodates all backends
func (c *MemoryCache) Get(_ context.Context, key string) (Entry, bool, error) { ... }
func (c *RedisCache)  Get(ctx context.Context, key string) (Entry, bool, error) { ... }

// Wrong — forces interface change and cascading edits later
func (c *MemoryCache) Get(key string) (Entry, bool, error) { ... }
```

## Error handling philosophy

**Cache-like `Get` returns `(Value, bool, error)`:**
- `(v, true, nil)` — hit
- `(_, false, nil)` — clean miss (key absent, TTL expired)
- `(_, false, err)` — backend failure; middleware logs and treats as miss

**Rate-limit `Allow` returns `(bool, error)`:**
- `(true, nil)` — allowed
- `(false, nil)` — rate limited (normal operation, not an error)
- `(_, err)` — backend failure; middleware fail-opens and logs

**Rule**: backends never make policy decisions about their own failures. Backends surface errors; middleware decides what to do.

Wrap errors with `fmt.Errorf("context: %w", err)`. Check with `errors.Is` / `errors.As`. Never `panic` in library code.

## Redis conventions

- **Namespace all keys** — `<package>:<key>` at minimum. Currently `ratelimit:<key>:<window>` and `cache:<sha256>`.
- **Distinguish miss from error** with `errors.Is(err, redis.Nil)`. Failure to do this hides Redis outages behind fake "cache miss" behavior.
- **Atomic operations** — `INCR` is atomic; use it. For read-modify-write, use a Lua script via `redis.NewScript`.
- **Script declaration** — `var script = redis.NewScript(\`...`)` at package level, OR assign to a struct field in the constructor. **Never as a struct field TYPE** (`script *redis.NewScript(...)` — a function call as a type).
- **Lua ARGV are strings** — Redis passes ARGV values to Lua as strings. Use explicit `tonumber()` in the script if you need arithmetic. Don't rely on implicit coercion.
- **Variadic Lua args** — build `[]interface{}{...}` and spread with `...` when calling `script.Run`.
- **`go-redis` idioms** — pass raw `int64` values directly without `strconv`. `.Bytes()` for binary values, `.Int()` for counters, `.Bool()` for scripts that return 0/1.
- **For rate limiting specifically**: sliding window counter, not token bucket. Token bucket needs floating-point refill math inside a Lua script; sliding window reduces to `INCR + EXPIRE` primitives that map directly onto Redis atomics.

## Middleware patterns

- Middleware wraps handlers using the classic `func(http.Handler) http.Handler` shape. Chain order (outermost to innermost): logging → rate limit → cache → proxy.
- To capture the upstream response for caching, wrap `http.ResponseWriter` with a `bufWriter` that buffers `Write` and remembers the status. Order matters: `WriteHeader` freezes response headers, so any `w.Header().Set(...)` must happen before it.
- Middleware may use `slog.Default()` for logging (set in `main` via `slog.SetDefault(logger)`). Injecting a logger through the middleware factory is cleaner.

## Testing conventions

- **Redis-dependent tests use `miniredis.RunT(t)`** — automatic cleanup, in-process, no Docker.
- **Compile-time interface satisfaction checks** in test files:
  ```go
  var (
      _ Cache = (*MemoryCache)(nil)
      _ Cache = (*RedisCache)(nil)
  )
  ```
  If either type drifts from the interface, the build breaks. Free protection, zero runtime cost.
- **Test failure paths explicitly** — `mr.Close()` to simulate a Redis outage; assert the cache returns miss + error and does not panic. Happy paths alone don't prove production readiness.
- **Table-driven tests** for algorithms with multiple cases (rate-limit boundary crossings, cache eviction scenarios, header roundtrip fidelity). Single-purpose tests where setup varies significantly.
- **Time-dependent tests use `mr.FastForward(...)`** for TTL expiry rather than `time.Sleep`. Deterministic, fast, no flakes.

## Package-specific notes

### `internal/ratelimit/`
- `Manager` (in-memory) uses `golang.org/x/time/rate` token buckets per key, guarded by a mutex-protected map.
- `RedisLimiter` uses sliding-window counter algorithm (see Redis conventions above).
- Both satisfy `Limiter`; both are constructed in `main` based on `RATE_LIMIT_BACKEND`.

### `internal/cache/`
- `Entry` fields are exported (JSON-marshalable) because `RedisCache` roundtrips through `encoding/json`.
- `http.Header` marshals natively as `map[string][]string`. `[]byte` becomes base64. `time.Time` roundtrips with nanosecond precision.
- `MemoryCache` sets `ExpiresAt` on `Set`. `RedisCache` ignores `ExpiresAt` — Redis handles TTL via `SET ... EX`.
- Interface extraction and `RedisCache` implementation is Phase B work.

### `internal/proxy/`
- `OpenAIProxy` and `GeminiProxy` are near-identical shapes, differing only in auth header (`Authorization: Bearer` vs `x-goog-api-key`) and upstream URL.
- Both preserve the request path so `/v1/chat/completions` and other endpoints route correctly. The `upstreamRequestURL` helper handles trimming and query string forwarding.
- When adding a third provider (Anthropic, in Phase D), factor common shape into a shared helper — don't triplicate the code.

### `internal/server/`
- `Server` struct holds `httpServer`, `logger`, `mux`, `proxy`, `mgr` (rate limiter), `cache`. Constructor takes interfaces where possible.
- Routes registered in `registerRoutes()`. `/health` unwrapped; `/v1/chat/completions` wrapped in cache + rate-limit middleware.
- Server timeouts are set: `ReadTimeout: 5s`, `WriteTimeout: 35s` (upstream can take up to 30s), `IdleTimeout: 60s`.
