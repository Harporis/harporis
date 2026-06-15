# Harporis Roadmap — Editions & Phases

**Date:** 2026-06-14
**Status:** Approved (structure)
**Topic:** Decomposition of Harporis from the current state to the final
vision, expressed as parallel **open-core editions** built in
dependency-ordered phases.

---

## 1. Product model: parallel editions (open-core)

Harporis ships as **three coexisting editions**, not a single product that
mutates over time. The simple edition stays forever light.

| Edition | Audience | Adds over previous | Stores |
|---|---|---|---|
| **Community** (Lite) | CI/CD, self-host on a single machine | — (the core) | none (NATS only) |
| **Pro** (Scale + FP) | k8s multi-node, FP triage at volume | Helm/k8s deploy, persistent stores, false-positive lifecycle, analytics | Postgres + ClickHouse |
| **Enterprise** (Platform) | full service | web UI, dashboards, RBAC roles, AI FP-classifier | + API server |

### Architectural backbone

Today, sinks are already *consumers* of the NATS streams (`FINDINGS`,
`STATUS`). Generalize that into the governing principle:

> **Everything new is just another consumer of the same NATS streams.
> An edition is the set of consumers you deploy.**

- The core (getter → scanner → writer + NATS + CLI) stays unchanged and
  dependency-light across all editions.
- Postgres, ClickHouse, the FP service, the web UI, and the AI classifier
  all attach to `harporis.findings.>` / `harporis.status.>` — they never
  fork the core.
- The **stitch contract** is the proto schema + the NATS subject layout.
  Its stability is the foundation every edition rests on.

### Non-negotiable constraints (carried from existing design)

- Core build stays `CGO_ENABLED=0` (pure-Go); heavy deps live only in
  optional modules.
- One-command launch preserved: Community = `scripts/install.sh` +
  docker-compose (single machine); Pro = Helm chart (k8s).
- Each service scales independently (10 getters / 5 scanners / 5 writers).
- Optional modules are opt-in via flags/profiles; absent module = zero
  cost to the core.

---

## 2. Phases

Phase IDs are `P<edition>.<n>`. Order within an edition is the intended
build order; cross-edition dependencies are called out in §3.

### Edition: Community (Lite) — harden the core to production

Goal: forever-light, NATS-only, **single-machine** deployment via the
`scripts/install.sh` one-shot installer + docker-compose (which can scale
replicas locally with `--scale`). No Helm/Kubernetes — that is a Pro feature.

- **P0.1 — `harporis watch` fleet view + source attribution.**
  Live cross-scan dashboard reading the wildcard `STATUS` stream; add a
  `source` field to `StatusEvent` (getter + scanner stamp
  `<service>-<hostname>`). Fully designed in
  `2026-06-14-harporis-watch-design.md`. *First phase to implement.*
- **P0.2 — Harden the stitch contract.**
  Move `ValidateScanID`, `SanitizeConsumerName`, and `isTerminal` into
  `kit/` so every consumer agrees on them; freeze the findings/status
  proto as the public module API. Prerequisite for all DB/UI/AI modules
  so they don't bind to an unstable schema.
- **P0.3 — Regex pack consolidation.**
  Consolidate the ~28-rule pack to cut per-chunk CPU; measured against the
  benchmark baseline at commit `9785898`. Serves the "fast" goal.
(No Helm/k8s phase here — Community is single-machine Docker only. Helm
moves to Pro, see P1.4.)

### Edition: Pro (Scale + FP) — persistence & false-positive lifecycle

Goal: durable queryable history, cross-scan analytics at volume, and the
false-positive (FP) lifecycle.

- **P1.1 — Postgres store (findings consumer).**
  Durable, queryable backbone for findings + scan metadata. The SQLite
  sink already proves the shape; Postgres is its multi-host twin.
- **P1.2 — FP lifecycle.**
  Stable finding fingerprint, dedup, suppression/allowlist, FP status in
  Postgres; managed via CLI. This is the "FP" capability — and it doubles
  as the **labeling pipeline** that produces training data for the AI
  classifier in P2.4.
- **P1.3 — ClickHouse store (findings consumer).**
  High-volume analytics/aggregation (e.g. severity trends over time) at
  100-replica scale where OLTP Postgres is the wrong tool.
- **P1.4 — Helm chart for Kubernetes (the Pro deploy path).**
  Stateless services + NATS with per-service HPA, plus optional Postgres +
  ClickHouse subcharts. This is where multi-node 1→100-replica k8s scaling
  lives; Community stays single-machine Docker.

### Edition: Enterprise (Platform) — UI, roles, AI

Goal: a full service.

- **P2.1 — API server.**
  Read/control plane over the stores + NATS. The boundary the UI and
  external integrations consume.
- **P2.2 — Web UI + dashboards.**
  Reads the API / ClickHouse. The "UI page" with dashboards.
- **P2.3 — AuthN/AuthZ + RBAC roles.**
  Plus multi-tenancy if required.
- **P2.4 — AI FP-classifier.**
  Fine-tuned binary (true/false positive) model running as a findings
  consumer that annotates findings. **Depends on P1.2** — the FP labels
  produced there are its training data.
- **P2.5 — Helm: full platform deploy.**

---

## 3. Dependency ordering (why the sequence is not arbitrary)

1. **P0.2 (contract) before any module.** Otherwise Postgres / ClickHouse
   / UI bind to an unstable proto and churn.
2. **P1.2 (FP labeling) before P2.4 (AI).** The classifier needs labeled
   data; the FP lifecycle produces it.
3. **Helm is Pro-and-up** (P1.4 → P2.5), never in Community. Community ships
   single-machine Docker (install script + compose) only.

**Cross-cutting threads** running through all phases: secret-handling
security, observability (Prometheus metrics already exist), and
per-edition versioning/release pipelines.

---

## 4. First phase to implement

**P0.1 `harporis watch`** — already designed in
`docs/superpowers/specs/2026-06-14-harporis-watch-design.md`. After this
roadmap is committed, that spec proceeds to an implementation plan.
