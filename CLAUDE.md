# LLM Gateway — Project Context for Claude

## What this is

A Go API gateway sitting between client applications and LLM providers (OpenAI, Gemini). Production-grade portfolio build targeting AI infrastructure roles. Provides rate limiting, response caching, multi-provider routing/failover, streaming passthrough, cost tracking, and observability in front of upstream LLM APIs.

Same category as LiteLLM, OpenRouter, and Helicone.

## Current state

**v1 complete and committed.**
- HTTP server with `/health`, `/v1/chat/completions`, graceful shutdown
- OpenAI + Gemini proxy handlers (both, more than v1 scoped for)
- In-memory token-bucket rate limiting via `golang.org/x/time/rate`
- LRU response cache with `bufWriter` response-capture middleware
- Multi-stage distroless Dockerfile (~15–20MB)
- kind cluster manifests (K8s deployment + service, kind config)

**working on Phase B**

## Phase roadmap

- **A** — Redis rate limiting
- **B** — Redis-backed shared cache with interface extraction; fix the two middleware bugs while there
- **C** — kind deployment with 2 replicas, prove shared state across pods
- **D** — Multi-provider routing with failover
- **E** — SSE streaming passthrough
- **F** — Cost / token accounting per key
- **G** — Prometheus + Grafana observability
- **H** — Production hardening: Ingress + TLS on cloud cluster (this is when DigitalOcean provisioning returns), load testing, README/portfolio polish

## Architecture

Request pipeline order: `logging middleware → rate limit middleware → cache middleware → proxy handler → upstream`

Package-level interfaces (both use the same pattern deliberately — the interface-first discipline is architectural, not incidental):

- `proxy.Proxy`: `Handle(w, r)` — `OpenAIProxy`, `GeminiProxy`
- `ratelimit.Limiter`: `Allow(ctx, key) (bool, error)` — `Manager` (memory), `RedisLimiter`
- `cache.Cache`: `Get(ctx, key) (Entry, bool, error)` + `Set(ctx, key, entry) error` — `MemoryCache`, `RedisCache` *(interface not yet extracted; Phase B work)*

Backend selection via env vars:

- `RATE_LIMIT_BACKEND=memory|redis`
- `CACHE_BACKEND=memory|redis`

**One shared Redis client** if either backend is `redis`. Do not open two connections. Ping on startup and exit if it fails — bad `REDIS_URL` should fail loudly at boot, not on the first request.

## Design principles (non-negotiable — enforce in reviews)

- **Fail-open at the middleware layer, never inside the backend.** Redis errors must surface so operators see outages. The middleware decides whether to swallow them. A rate limiter that hides its own outage is worse than no rate limiter.
- **Interfaces widen at package boundaries, from the start.** `NewServer` takes `Limiter` and `Cache`, never `*Manager` and `*MemoryCache`. Retrofitting this later was a real Phase A blocker; don't repeat it in new packages.
- **Namespace all Redis keys.** `ratelimit:<key>:<window>`, `cache:<sha256>`. Never assume you own the whole Redis instance.
- **Config from env, defaults sensible, fail loud at boot on bad config.** Don't let a typo in `REDIS_URL` surface on the first user request at 2am.
- **Caching layers are complementary, not competing.** Exact-match LRU (L1) first; semantic cache (L2, Phase H) only after L1 is solid.

## Common commands

```bash
# Build & test
go build ./...
go test ./...

# Run locally (memory backends)
export OPENAI_API_KEY=sk-...
go run ./cmd/gateway

# Run locally with Redis backends
docker compose up -d redis
export RATE_LIMIT_BACKEND=redis CACHE_BACKEND=redis REDIS_URL=redis://localhost:6379
go run ./cmd/gateway

# Docker image
docker build -t victornguyen247/llm-gateway:v0.1 .
docker run --rm -p 8080:8080 -e OPENAI_API_KEY=$OPENAI_API_KEY victornguyen247/llm-gateway:v0.1

# kind local deploy
kind create cluster --config k8s/kind-cluster.yaml
kind load docker-image victornguyen247/llm-gateway:v0.1 --name gateway-dev
kubectl create secret generic gateway-secret --from-literal=OPENAI_API_KEY=$OPENAI_API_KEY
kubectl apply -f k8s/
kubectl port-forward svc/llm-gateway 8080:80

# Smoke test through the running gateway
curl -X POST http://localhost:8080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer test-key' \
  -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"say hi"}]}'
```

## Directory map

```
cmd/gateway/          # main entrypoint: config, DI, lifecycle, signal handling
cmd/redistest/        # throwaway Redis connectivity smoke test (kept for reference)
internal/server/      # HTTP server, route registration, middleware (rate limit + cache)
internal/proxy/       # upstream proxy handlers + Proxy interface (OpenAI, Gemini)
internal/ratelimit/   # Limiter interface + Manager (memory) + RedisLimiter
internal/cache/       # Cache impl + Entry type (interface extraction is Phase B)
k8s/                  # Deployment, Service, kind cluster config
Dockerfile            # multi-stage: golang builder → distroless static runtime
docker-compose.yaml   # local stack: gateway + redis
```

## Tech stack

- **Language**: Go (stdlib-first — `net/http`, `log/slog`, `context`)
- **Libraries**: `golang.org/x/time/rate` (token bucket), `hashicorp/golang-lru/v2`, `redis/go-redis/v9`, `google/uuid`
- **Testing**: `alicebob/miniredis/v2` for Redis-dependent tests
- **Container**: Docker multi-stage → `gcr.io/distroless/static-debian12:nonroot`
- **Orchestration**: kind (local dev throughout v1–v2); DigitalOcean managed K8s deferred until Phase I
- **Planned**: Prometheus, Grafana, Qdrant (semantic cache)

## What NOT to do

- **Don't suggest gin/echo/chi/fiber.** stdlib `net/http` is deliberate — learning fundamentals matters more than convenience.
- **Don't suggest zap/zerolog/logrus.** `slog` covers everything needed.
- **Don't provision DigitalOcean before Phase I.** kind is the correct dev target throughout. DigitalOcean's distinct learning value (Ingress + TLS + real DNS) doesn't kick in until Phase I.
- **Don't recommend Helm charts before Phase I.** Plain manifests are better for learning K8s primitives.
- **Don't put fail-open policy inside a backend.** It belongs in the middleware. See design principles.
- **Don't reach for `context.Background()` inside a request-scoped code path.** Thread `r.Context()` through so cancellation and deadlines propagate.
- **Don't reproduce copyrighted material** in any research/reference work — paraphrase per Claude's standard rules.

## When helping with code review

Review against Go idioms and production-readiness, not just "does it compile":

- Locking discipline around shared state (`sync.Mutex`, map access)
- Response body handling (`defer resp.Body.Close()`, `io.Copy` errors checked)
- Header ordering (`Set` BEFORE `WriteHeader` — the current cache middleware bug)
- Context propagation (`http.NewRequestWithContext(r.Context(), ...)`)
- Redis errors surfaced (via `errors.Is(err, redis.Nil)` distinction), not swallowed
- Interface satisfaction at boundaries, not concrete-type coupling
- Test coverage on failure paths, not just happy paths
- Env config parsed with sensible defaults, invalid values failing at boot

See `internal/CLAUDE.md` for cross-package code conventions.
