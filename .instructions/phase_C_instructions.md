# Phase C — Deploy to kind & Prove Shared State

## 🎯 Missions in Phase C (at a glance)

- [ ] **C1** — Fix current bugs in `k8s/deployment.yaml`; add pod identity via the downward API
- [ ] **C2** — Log the pod name (so the C5/C6 demos are actually readable)
- [ ] **C3** — Deploy Redis to kind (StatefulSet + PVC + Service)
- [ ] **C4** — Wire the gateway to Redis in K8s; scale to 2 replicas
- [ ] **C5** — Prove the shared cache: a request cached on pod A serves pod B
- [ ] **C6** — Prove the shared rate limit: budget is respected globally, not per-pod
- [ ] **C7** — Document the milestone: README v2 section + progress tracker

**End state**: two gateway pods behind one Service, one Redis pod, and reproducible experiments demonstrating that cache hits and rate-limit budgets are shared. This is the moment v2 justifies its existence — everything you built in Phases A and B pays off here.

---

## Big picture: why Phase C matters

Phases A and B built the machinery. Phase C proves it works.

Until this phase, "shared state across pods" is a claim in a README. After this phase, you have two pods running, you send a request to one, you get a cache hit from the other, and you can watch it in the logs. That's the artifact you can screenshot for the portfolio and the story you can tell in an interview: *"v1's biggest limitation was per-replica state — here's the design that fixed it, and here's the before/after."*

Three things to internalize before starting:

1. **The demonstration is the deliverable**, not just "it deploys." Anyone can write YAML that boots pods. What separates a portfolio piece from a homework assignment is showing the *before/after*. Your experiments in C5 and C6 are that evidence — treat them as first-class deliverables, not smoke tests.

2. **kind is the right target throughout Phase C.** DigitalOcean (Phase I) adds real DNS, Ingress, TLS, and a monthly bill — none of which is the point right now. Distributed state is proven identically on local kind.

3. **Two replicas is enough.** Three doesn't teach anything two doesn't. Two is the smallest number that makes "shared" a meaningful word.

---

## Subtask C1 — Fix deployment.yaml + add pod identity

### What + why
The current `k8s/deployment.yaml` has three real issues that will bite you in Phase C:

- **Two separate `env:` blocks.** YAML allows this, but the second silently overrides the first. Your `OPENAI_API_KEY` entry vanishes at deploy time and you get a confusing runtime "why is my key empty" hunt.
- **The manual `OPENAI_API_KEY` entry has no `value` or `valueFrom`.** Even if the override didn't nuke it, it would deploy as an empty string. The `envFrom: secretRef` block above should be doing this work anyway; the manual entry is redundant.
- **`GATEWAY_LISTEN: "8080"` is wrong.** Your code expects `:8080` (with a leading colon — that's how Go's `net/http` reads bind addresses). Right now the env var is being ignored because the code's default kicks in — but a future config change would silently break it.

While you're in there, also add pod identity via the downward API so the C5/C6 experiments have readable logs.

### How-to
1. Delete both `env:` blocks. Keep the `envFrom: secretRef` block that pulls from `gateway-secret`.
2. Add one `env:` block with:
   - `POD_NAME` from `fieldRef: fieldPath: metadata.name` (downward API — K8s injects the pod's own name at container start)
   - `GATEWAY_LISTEN` as `":8080"` (with colon) if you want to keep it overridable; otherwise remove it entirely and let the code's default apply
3. While you're touching the file, tighten the probe timings. Current `initialDelaySeconds: 30` on both probes is extremely conservative for a Go service that boots in ~200ms. Set liveness to `initialDelaySeconds: 5, periodSeconds: 10` and readiness to `initialDelaySeconds: 2, periodSeconds: 5`. This makes rolling updates in C4 feel snappy instead of glacial.
4. `kubectl apply -f k8s/deployment.yaml` — should redeploy cleanly.
5. Verify the env vars actually made it in: `kubectl exec deploy/llm-gateway -- env | grep -E 'POD_NAME|OPENAI|GATEWAY'`.

### 🔍 Check-in question
The downward API can expose pod metadata (`metadata.name`, `metadata.namespace`, `status.podIP`, and more) as env vars or mounted files. Why is `metadata.name` more useful for logging than `status.podIP` when your goal is to demonstrate that requests hit different pods? What would go wrong if you used the pod's IP instead?

---

## Subtask C2 — Log the pod name

### What + why
For C5/C6 to be readable, every log line needs to say which pod produced it. Otherwise you're staring at logs from two pods interleaved through `kubectl logs -l app=llm-gateway`, with no way to tell them apart. One env var read + one slog attribute is all it takes.

### How-to
1. In `cmd/gateway/main.go`, right after you construct the logger, before you call `slog.SetDefault(logger)`:
   ```go
   if podName := os.Getenv("POD_NAME"); podName != "" {
       logger = logger.With("pod", podName)
   }
   slog.SetDefault(logger)
   ```
2. Rebuild the image with a fresh tag (you'll need this for C4's `kubectl apply` to actually pick up the new binary):
   ```bash
   docker build -t victornguyen247/llm-gateway:v0.2 .
   kind load docker-image victornguyen247/llm-gateway:v0.2 --name gateway-dev
   ```
3. Verify locally before deploying: `POD_NAME=test-local go run ./cmd/gateway` — every log line should include `"pod":"test-local"` in the JSON output.

### 🔍 Check-in question
You added `pod` to `logger` via `logger.With(...)` and then called `slog.SetDefault(logger)`. Elsewhere in the code, does the `OpenAIProxy`'s injected logger (constructed before this change and passed into `NewOpenAIProxy`) inherit the `pod` attribute? What about the cache/rate-limit middleware's `slog.Default()` calls? Where does the attribute propagate, and where doesn't it?

---

## Subtask C3 — Deploy Redis to kind

### What + why
Now the actual dependency: Redis in the cluster. There's a real decision point here — **Deployment vs StatefulSet.**

- **Deployment + `emptyDir`** — 20 lines shorter, works fine for dev. Pod restart wipes state. For cache and rate-limit state that's acceptable; users just get a cold cache after a Redis pod restart.
- **StatefulSet + `PersistentVolumeClaim`** — stable network identity (`redis-0.redis.<namespace>.svc.cluster.local`) and durable storage. This is what production Redis looks like. kind provisions PVCs automatically via the default `standard` StorageClass, so it works out of the box.

For portfolio value, use `StatefulSet`. The extra ~30 lines teach real K8s primitives (headless services, volumeClaimTemplates) you'll need again. Single replica is fine — HA Redis (Sentinel or Cluster) is a Phase I concern.

Also: set Redis to `allkeys-lru` eviction. This is the operator-side setting your Phase B mental model assumed ("Redis handles its own eviction"). It's a good moment to make that concrete.

### How-to
1. Create `k8s/redis.yaml` with two resources:
   - A **headless `Service`** (`clusterIP: None`) named `redis`, port `6379`. Headless is the StatefulSet convention — it gives you per-pod DNS records like `redis-0.redis`, though for a single-replica setup you'll mostly use the service name `redis`.
   - A **StatefulSet** named `redis`:
     - `serviceName: redis` (matches the Service — required linkage)
     - `replicas: 1`
     - Container `redis:7-alpine` with args to set `maxmemory 128mb`, `maxmemory-policy allkeys-lru`, and `save 60 1` for periodic RDB snapshots
     - `containerPort: 6379`
     - `volumeMounts` at `/data` from a claim named `data`
     - `volumeClaimTemplates` with one entry: `data`, `accessModes: [ReadWriteOnce]`, 1Gi
     - Liveness + readiness probes running `redis-cli ping`
2. `kubectl apply -f k8s/redis.yaml`
3. Watch it come up: `kubectl get pods -w`. First deploy takes ~30s because the PVC has to bind and Redis has to boot.
4. Verify DNS works from within the cluster before you even touch the gateway:
   ```bash
   kubectl run -it --rm ping-test --image=redis:7-alpine --restart=Never -- redis-cli -h redis ping
   ```
   Should print `PONG` and exit cleanly.
5. Check that eviction policy actually took effect:
   ```bash
   kubectl exec redis-0 -- redis-cli CONFIG GET maxmemory-policy
   ```
   Should return `allkeys-lru`.

### 🔍 Check-in question
You could put any of these three in the gateway's `REDIS_URL`: `redis://redis:6379`, `redis://redis.default:6379`, or `redis://redis.default.svc.cluster.local:6379`. All three resolve. Which one would you actually use, and how does the choice affect (or not affect) portability if you deployed this to a different namespace, a different cluster, or a non-kind environment?

---

## Subtask C4 — Wire gateway to Redis; scale to 2 replicas

### What + why
Flip the gateway to Redis backends and add a second replica. This is the config change that makes shared state actually happen — everything before was setup.

### How-to
1. Edit `k8s/deployment.yaml`:
   - `replicas: 2`
   - Bump `image: victornguyen247/llm-gateway:v0.2` (the tag you built in C2)
   - Add a `strategy` block for a proper rolling update:
     ```yaml
     strategy:
       type: RollingUpdate
       rollingUpdate:
         maxSurge: 1
         maxUnavailable: 0
     ```
     `maxUnavailable: 0` = never let the total ready count drop below `replicas` during rollout = zero-downtime deploys.
   - Add env vars into your existing `env:` block:
     ```yaml
     - name: REDIS_URL
       value: "redis://redis:6379"
     - name: RATE_LIMIT_BACKEND
       value: "redis"
     - name: CACHE_BACKEND
       value: "redis"
     - name: RATE_LIMIT_RPS
       value: "1"        # deliberately low for the C6 demo
     - name: RATE_LIMIT_BURST
       value: "1"
     - name: CACHE_TTL
       value: "5m"
     ```
2. `kubectl apply -f k8s/`
3. Watch both pods come up: `kubectl get pods -w`. Both should reach `Running` with `1/1` ready.
4. Read the boot logs and confirm each pod pinged Redis successfully:
   ```bash
   kubectl logs -l app=llm-gateway --tail=20
   ```
   You should see log lines from *two different pods* (each stamped with its own `pod` attribute from C2), no ping errors, and no crash loops.
5. If a pod is `CrashLoopBackOff`: `kubectl describe pod <name>` and `kubectl logs <name>`. Most common cause is a bad `REDIS_URL` — `redis.ParseURL` will surface that at boot with a clear error message (which was the whole point of the boot-time ping in `main.go`).

### 🔍 Check-in question
`kubectl apply` triggered a rolling update. During the ~10 seconds where the new pods came up and old pods terminated, what happened to in-flight requests being served by pods that were about to die? Two settings on the Deployment (`strategy` you just added is one — what's the other?) plus one server-side concern determine whether shutdown is actually graceful. Which of them does your current setup handle correctly?

---

## Subtask C5 — Prove the shared cache works

### What + why
The moment of truth for Phase B. Send a request to pod A; get a cache hit from pod B. Screenshot this — it's portfolio material.

The trick is bypassing the Service's load-balancing so you control which pod handles each request. `kubectl port-forward pod/<name>` does this cleanly — it forwards straight to a specific pod, ignoring the Service.

### How-to
1. Grab both pod names:
   ```bash
   PODS=($(kubectl get pods -l app=llm-gateway -o jsonpath='{.items[*].metadata.name}'))
   POD_A=${PODS[0]}
   POD_B=${PODS[1]}
   echo "Pod A: $POD_A"
   echo "Pod B: $POD_B"
   ```
2. In two separate terminals, port-forward each pod to a different local port:
   ```bash
   # Terminal 1
   kubectl port-forward pod/$POD_A 8080:8080
   # Terminal 2
   kubectl port-forward pod/$POD_B 8081:8080
   ```
3. In a third terminal, send the same request to pod A first:
   ```bash
   BODY='{"model":"gpt-4o-mini","messages":[{"role":"user","content":"phase C cache demo"}]}'
   curl -sD - -o /dev/null -X POST http://localhost:8080/v1/chat/completions \
     -H 'Content-Type: application/json' \
     -H 'Authorization: Bearer demo' \
     -d "$BODY" | grep -iE 'HTTP|X-Cache'
   ```
   Expected: `HTTP/1.1 200 OK` and `X-Cache: MISS`.
4. Then send the identical request to pod B:
   ```bash
   curl -sD - -o /dev/null -X POST http://localhost:8081/v1/chat/completions \
     -H 'Content-Type: application/json' \
     -H 'Authorization: Bearer demo' \
     -d "$BODY" | grep -iE 'HTTP|X-Cache'
   ```
   Expected: `HTTP/1.1 200 OK` and **`X-Cache: HIT`** — even though pod B never made an upstream call.
5. Watch the logs from both pods:
   ```bash
   kubectl logs -l app=llm-gateway --tail=30
   ```
   Pod A should log an upstream call (with its `pod` attribute). Pod B should log no upstream call for the second request — the cache short-circuits before the proxy handler runs. Save this output; it goes into the README in C7.

### 🔍 Check-in question
Your cache middleware buffers the response body into memory before writing it out. If a very large response (say 10MB) is cached, and then a second pod retrieves it from Redis, the 10MB travels: `client ← pod B ← Redis ← pod B ← client`. Compare that to just calling upstream: `client ← pod B ← OpenAI ← pod B ← client`. Is there a request/response size where the Redis roundtrip is actually *worse* than calling upstream again? What would you measure to find out?

---

## Subtask C6 — Prove the shared rate limit works

### What + why
The paired demonstration for Phase A. With `rps=1, burst=1` per the config you set in C4, the *total* budget across all pods should be 1 request per second — not 1 per pod per second. This is where memory-backend rate limiting failed silently in v1 and where Redis-backend fixes it correctly.

The comparison is the demo: run the same experiment with `CACHE_BACKEND=redis` and `RATE_LIMIT_BACKEND=memory`, and count the difference in successful requests. That before/after is the portfolio artifact.

### How-to
1. Keep the two port-forwards from C5 running.
2. Fire 10 requests round-robin between the pods, deliberately making each request unique so cache hits don't interfere with the rate-limit test:
   ```bash
   for i in {1..10}; do
     PORT=$( (( i % 2 == 0 )) && echo 8080 || echo 8081 )
     STATUS=$(curl -s -o /dev/null -w "%{http_code}" \
       -X POST http://localhost:$PORT/v1/chat/completions \
       -H 'Content-Type: application/json' \
       -H 'Authorization: Bearer ratelimit-demo' \
       -d "{\"model\":\"gpt-4o-mini\",\"messages\":[{\"role\":\"user\",\"content\":\"req $i\"}]}")
     echo "req $i on port $PORT -> $STATUS"
   done
   ```
3. **Redis backend results** (already configured in C4): mostly `429` after the first couple of requests, regardless of which pod they hit. The total `200` count should track elapsed seconds (with rps=1), not request count.
4. **Flip to memory backend for the comparison**:
   ```bash
   kubectl set env deployment/llm-gateway RATE_LIMIT_BACKEND=memory
   kubectl rollout status deployment/llm-gateway
   ```
   Wait for the rolling update to finish (~10s with the tightened probes from C1).
5. Rerun the same loop. Result: roughly **twice** as many `200`s — because each pod has its own bucket, so the effective global rate is `2 × rps`. That's exactly the v1 bug this whole rearchitecture was designed to fix.
6. Save both runs' output. The side-by-side goes into the README in C7 as a small table.
7. Flip back to `redis` when you're done demonstrating:
   ```bash
   kubectl set env deployment/llm-gateway RATE_LIMIT_BACKEND=redis
   kubectl rollout status deployment/llm-gateway
   ```

### 🔍 Check-in question
In the memory backend, scaling to 5 replicas gives you an effective rate of `5 × rps`. In the Redis backend, it stays at `rps` regardless of replica count. Which of the two is "right"? Put another way: when the *caller* configures "10 req/s per user", which behavior are they actually asking for? Is your v2 README explicit about this, or does it just say "Redis-backed rate limiting" and hope the reader figures out the difference?

---

## Subtask C7 — Document the milestone

### What + why
This is the phase that pays for the portfolio. Without documentation, no one reading your README knows that "v2" means anything specific. A well-written README section here is the difference between "he built a Go project" and "he understands distributed systems well enough to design around a real problem."

### How-to
1. Add a `## v2 milestone: shared state across replicas` section to the root `README.md`:
   - **The problem** (one paragraph): per-replica state in v1 — cache hit rate collapsed with scale, and rate limits were effectively `N × rps` at N replicas.
   - **The solution** (one paragraph): Redis-backed shared state via the `Limiter` and `Cache` interfaces, backend-selectable through `RATE_LIMIT_BACKEND` and `CACHE_BACKEND` env vars.
   - **Evidence**: paste your C5 output (MISS → HIT across pods) and the C6 comparison table:

     | Backend | 200s / 10 reqs | 429s / 10 reqs | Effective rate |
     |---|---|---|---|
     | memory | ~[your number] | ~[your number] | ~2 × rps (broken) |
     | redis | ~[your number] | ~[your number] | ~1 × rps (correct) |
2. Update the architecture diagram (or make a new one) to show 2 gateway pods + 1 Redis pod behind a Service. Excalidraw → PNG in `docs/`.
3. Update `gateway_progress_tracker.md`:
   - Mark Phases A, B, C as done with dates
   - Add a "v2 milestone hit: shared state across replicas" line
   - Log actual pace vs your budget for each phase
   - Include a lessons-learned bullet or two — what surprised you, what took longer than planned, what shortcut you regretted. Honest notes here make Phase D planning better.
4. Optional but strongly recommended: tag the release.
   ```bash
   git tag v2.0-shared-state
   git push --tags
   ```
   Tags make it easy to point a recruiter at "the state of the code at this milestone" without needing them to trawl commit history.

### 🔍 Check-in question
Your README v2 section describes what changed and shows it works. What's the *smallest* addition you could make that would demonstrate genuine systems thinking rather than just "it works"? (Hint: think failure modes. What happens to the gateway when Redis dies? Have you tested it, and have you documented it?)

---

## Wrapping up Phase C

Before you close:

1. Both gateway pods running, both connected to Redis, no crash loops
2. C5 cache demo shows `X-Cache: HIT` across pods, output captured
3. C6 rate-limit demo shows Redis budget respected globally, memory-mode comparison captured
4. README `v2 milestone` section committed
5. Progress tracker updated with real dates and honest pace notes
6. (Optional) `v2.0-shared-state` git tag pushed

**Phase D is next**: multi-provider routing + failover. You already have both `OpenAIProxy` and `GeminiProxy` — that was over-scope for v1, but it pays off now. Phase D turns them from "two independent handlers" into "a router with a fallback policy."

---

## 📖 Final reference: complete manifests and code

*(Attempt each subtask first. The check-in questions are the point — the code below is for review, not for lookup.)*

### `k8s/deployment.yaml` (fixed + Redis-wired)

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: llm-gateway
  labels:
    app: llm-gateway
spec:
  replicas: 2
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxSurge: 1
      maxUnavailable: 0
  selector:
    matchLabels:
      app: llm-gateway
  template:
    metadata:
      labels:
        app: llm-gateway
    spec:
      terminationGracePeriodSeconds: 30
      containers:
      - name: llm-gateway
        image: victornguyen247/llm-gateway:v0.2
        imagePullPolicy: IfNotPresent
        ports:
        - containerPort: 8080
        envFrom:
        - secretRef:
            name: gateway-secret
        env:
        - name: POD_NAME
          valueFrom:
            fieldRef:
              fieldPath: metadata.name
        - name: GATEWAY_LISTEN
          value: ":8080"
        - name: REDIS_URL
          value: "redis://redis:6379"
        - name: RATE_LIMIT_BACKEND
          value: "redis"
        - name: CACHE_BACKEND
          value: "redis"
        - name: RATE_LIMIT_RPS
          value: "1"
        - name: RATE_LIMIT_BURST
          value: "1"
        - name: CACHE_TTL
          value: "5m"
        resources:
          requests:
            cpu: 100m
            memory: 128Mi
          limits:
            cpu: 500m
            memory: 512Mi
        livenessProbe:
          httpGet:
            path: /health
            port: 8080
          initialDelaySeconds: 5
          periodSeconds: 10
        readinessProbe:
          httpGet:
            path: /health
            port: 8080
          initialDelaySeconds: 2
          periodSeconds: 5
```

### `k8s/redis.yaml` (new)

```yaml
apiVersion: v1
kind: Service
metadata:
  name: redis
  labels:
    app: redis
spec:
  clusterIP: None  # headless — StatefulSet convention
  ports:
  - port: 6379
    targetPort: 6379
    name: redis
  selector:
    app: redis
---
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: redis
  labels:
    app: redis
spec:
  serviceName: redis
  replicas: 1
  selector:
    matchLabels:
      app: redis
  template:
    metadata:
      labels:
        app: redis
    spec:
      containers:
      - name: redis
        image: redis:7-alpine
        args:
        - "redis-server"
        - "--maxmemory"
        - "128mb"
        - "--maxmemory-policy"
        - "allkeys-lru"
        - "--save"
        - "60"
        - "1"
        ports:
        - containerPort: 6379
          name: redis
        volumeMounts:
        - name: data
          mountPath: /data
        livenessProbe:
          exec:
            command: ["redis-cli", "ping"]
          initialDelaySeconds: 5
          periodSeconds: 10
        readinessProbe:
          exec:
            command: ["redis-cli", "ping"]
          initialDelaySeconds: 2
          periodSeconds: 5
        resources:
          requests:
            cpu: 100m
            memory: 128Mi
          limits:
            cpu: 500m
            memory: 256Mi
  volumeClaimTemplates:
  - metadata:
      name: data
    spec:
      accessModes: ["ReadWriteOnce"]
      resources:
        requests:
          storage: 1Gi
```

### `cmd/gateway/main.go` — pod name injection (snippet)

```go
// Right after building the logger, before slog.SetDefault:
logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
if podName := os.Getenv("POD_NAME"); podName != "" {
    logger = logger.With("pod", podName)
}
slog.SetDefault(logger)
```

### C5 experiment — full copy-paste block

```bash
# Get both pod names
PODS=($(kubectl get pods -l app=llm-gateway -o jsonpath='{.items[*].metadata.name}'))
POD_A=${PODS[0]}
POD_B=${PODS[1]}
echo "Pod A: $POD_A"
echo "Pod B: $POD_B"

# In separate terminals:
# Terminal 1:  kubectl port-forward pod/$POD_A 8080:8080
# Terminal 2:  kubectl port-forward pod/$POD_B 8081:8080

# In a third terminal:
BODY='{"model":"gpt-4o-mini","messages":[{"role":"user","content":"phase C cache demo"}]}'

echo "--- Request 1: pod A (expect MISS) ---"
curl -sD - -o /dev/null -X POST http://localhost:8080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer demo' \
  -d "$BODY" | grep -iE 'HTTP|X-Cache'

echo "--- Request 2: pod B, same body (expect HIT) ---"
curl -sD - -o /dev/null -X POST http://localhost:8081/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer demo' \
  -d "$BODY" | grep -iE 'HTTP|X-Cache'
```

### C6 experiment — full copy-paste block

```bash
run_ratelimit_demo() {
  echo "--- 10 requests round-robin ---"
  for i in {1..10}; do
    PORT=$( (( i % 2 == 0 )) && echo 8080 || echo 8081 )
    STATUS=$(curl -s -o /dev/null -w "%{http_code}" \
      -X POST http://localhost:$PORT/v1/chat/completions \
      -H 'Content-Type: application/json' \
      -H 'Authorization: Bearer ratelimit-demo' \
      -d "{\"model\":\"gpt-4o-mini\",\"messages\":[{\"role\":\"user\",\"content\":\"req $i\"}]}")
    echo "req $i on port $PORT -> $STATUS"
  done
}

echo "=== Redis backend ==="
run_ratelimit_demo

echo ""
echo "=== Flipping to memory backend ==="
kubectl set env deployment/llm-gateway RATE_LIMIT_BACKEND=memory
kubectl rollout status deployment/llm-gateway

# Re-fetch pod names — the rollout changed them
PODS=($(kubectl get pods -l app=llm-gateway -o jsonpath='{.items[*].metadata.name}'))
# Restart port-forwards to the new pods before continuing

echo "=== Memory backend ==="
run_ratelimit_demo

echo ""
echo "=== Flipping back to redis ==="
kubectl set env deployment/llm-gateway RATE_LIMIT_BACKEND=redis
kubectl rollout status deployment/llm-gateway
```
