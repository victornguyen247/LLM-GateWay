# LLM Gateway — progress tracker

## Milestones

| Milestone | Status | Date |
|-----------|--------|------|
| v1 — single-process gateway (memory backends) | Done | (pre–Phase A) |
| **v2 — shared state across replicas** | **Done** | 2026-09-27 |

**v2 milestone hit:** shared state across replicas — Redis-backed rate limiting and cache proven on a 2-replica kind deployment with one Redis pod.

## Phase roadmap

| Phase | Goal | Status | Completed | Budget (planned) | Actual pace |
|-------|------|--------|-----------|------------------|-------------|
| **A** | Redis rate limiting + `Limiter` interface | Done | 2026-08-25 | ~1 week | ~3 weeks (Redis script + wiring spread across July–August) |
| **B** | Redis cache + `Cache` interface | Done | 2026-09-22 | ~1 week | ~4 weeks (interface extraction, middleware fixes, PR merge) |
| **C** | kind deploy, prove shared cache + rate limit | Done | 2026-09-27 | ~3–5 days | ~5 days (Sep 23–27; k8s YAML + demos) |
| D | Multi-provider routing + failover | Planned | — | TBD | — |
| E | SSE streaming passthrough | Planned | — | TBD | — |
| F | Cost / token accounting | Planned | — | TBD | — |
| G | Prometheus + Grafana | Planned | — | TBD | — |
| H | Production hardening (Ingress, TLS, load test) | Planned | — | TBD | — |

## Lessons learned (Phases A–C)

- **Interface-first at `NewServer` pays off immediately.** Phase C only needed env vars and manifests; no surgery on middleware signatures.
- **Rolling updates invalidate port-forwards.** After `kubectl set env` or image changes, restart per-pod `kubectl port-forward` or curls return `000` until forwards point at live pods.
- **Shell matters for demo scripts.** `PODS[0]` is wrong in zsh (arrays are 1-based); use bash for the C5/C6 pod pickers or index with `${PODS[1]}` / `${PODS[2]}`.
- **The demo is the deliverable.** C5/C6 output belongs in the README; without MISS→HIT and the memory vs Redis 429 table, v2 reads like “added Redis” instead of “fixed a real scaling bug.”

## Next

Phase D — multi-provider routing with failover.
