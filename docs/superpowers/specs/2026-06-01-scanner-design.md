# `services/scanner` — Design Spec

**Status:** Draft.
**Date:** 2026-06-01.
**Author:** Brainstorming session, harporis-resume context.
**Supersedes:** Suggested scoping in `PROJECT_STATUS.md` § "Next module: `services/scanner`".

## 1. Goal

Build `services/scanner` — a stateless, horizontally scalable secret-detection consumer of the Harporis pipeline.

```
+-----------+        +-------------+        +------------+        +-----------+        +----------+
| harporis  | -----> |    NATS     | -----> |  getter    | -----> |  scanner  | -----> | writer   |
|   (CLI)   | <----- | (JetStream) | <----- | (N reps)   |        | (N reps)  |        | (future) |
+-----------+        +-------------+        +------------+        +-----------+        +----------+
                          ^                                                                ^
                          +-- HARPORIS_FINDINGS stream lives here, written by scanner ------+
```

**Scanner's contract:**

- **Consumes:** `harporis.chunks.>` from `HARPORIS_CHUNKS` (`WorkQueuePolicy`).
- **Produces:** `harporis.findings.<scan_id>` to `HARPORIS_FINDINGS` + `StatusEvent` deltas to `HARPORIS_STATUS`.
- **Does NOT** persist findings to any sink (file / DB / SARIF / UI). That is the **writer** service, a separate future phase.

The scanner detects → tags → publishes back to NATS. End of responsibility.

## 2. Detection engine

**Regex + Shannon entropy.** This is the "B" option from brainstorming (vs. regex-only or regex+live-verification).

**Rule pack:** YAML, embedded into the binary via `//go:embed`. Optional override via `--rules <path>` CLI flag.

### 2.1 Rule pack schema

```yaml
# services/scanner/rules/default.yaml
- id: aws-access-key-id
  description: AWS Access Key ID
  severity: high
  regex: '(?:^|[^A-Z0-9])(AKIA|ASIA)[A-Z0-9]{16}(?:[^A-Z0-9]|$)'
  entropy:
    min: 3.5          # Shannon entropy threshold over target_group
    target_group: 0   # 0 = full match; >0 = capture-group index
  tags: [aws, cloud]
  examples:
    positive: ["AKIAIOSFODNN7EXAMPLE"]
    negative: ["AKIA-not-a-real-key"]
```

| Field | Required | Notes |
|---|---|---|
| `id` | yes | Unique, kebab-case, stable across releases. Used in `Finding.rule_id` and Prometheus labels. |
| `description` | yes | One-line human-readable. |
| `severity` | yes | Enum: `low` / `medium` / `high` / `critical`. Maps 1:1 to proto `Severity`. |
| `regex` | yes | Go RE2 syntax. `(?i)` flag allowed. Multi-line via `(?s)` allowed for PEM blocks. |
| `entropy.min` | no | If present, match is only emitted when Shannon entropy of `target_group` ≥ this value. Default: not applied. |
| `entropy.target_group` | no | Default `0` (full match). Used for rules like `token=<value>` where only `<value>` should be entropy-checked. |
| `tags` | no | Free-form labels for grouping in UI. Not propagated into `Finding` proto — `rule_id` is enough; tags live with the rule pack. |
| `examples.positive` / `examples.negative` | yes | Both required. Drive unit tests: each rule MUST match all `positive` examples and MUST NOT match any `negative` examples. Tests fail the build. |

### 2.2 Initial rule pack (v0.1 scope)

Minimum viable set against `/tmp/leaky-repo` and the top cloud providers:

| ID | Target |
|---|---|
| `aws-access-key-id` | AWS access key (`AKIA…`/`ASIA…`) |
| `aws-secret-access-key` | AWS secret (40-char entropy ≥ 4.5) |
| `gcp-service-account-json` | GCP `"private_key": "-----BEGIN PRIVATE KEY-----…"` |
| `gcp-api-key` | GCP API key (`AIza[0-9A-Za-z_-]{35}`) |
| `stripe-secret-key` | Stripe `sk_live_…` / `sk_test_…` |
| `slack-bot-token` | `xoxb-…` / `xoxp-…` / `xoxa-…` |
| `slack-webhook` | `hooks.slack.com/services/T…/B…/…` |
| `github-pat-classic` | `ghp_…` (40 base62 chars) |
| `github-pat-fine` | `github_pat_…` |
| `github-oauth-token` | `gho_…` / `ghu_…` / `ghs_…` / `ghr_…` |
| `jwt` | `eyJ[A-Za-z0-9_-]+\.eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+` |
| `private-key-pem` | `-----BEGIN (RSA|EC|OPENSSH|PGP|DSA|ENCRYPTED|PRIVATE) KEY-----` (multi-line) |
| `generic-high-entropy` | `(?i)(token|secret|api[_-]?key|password|passwd|pwd)\s*[:=]\s*['"]?([A-Za-z0-9+/=_-]{20,})['"]?` with `entropy.target_group=2`, `entropy.min=4.0` |

Target: ~30-50 rules. The list above is the **minimum to pass leaky-repo ground truth**; the design contract is the **format**, not the exact list.

### 2.3 Loader & validation

`internal/rules/loader.go`:

- `LoadEmbedded() ([]Rule, error)` — reads embedded `default.yaml`.
- `LoadFile(path string) ([]Rule, error)` — reads user-supplied YAML.
- `Validate(rules []Rule) error` — per rule: regex compiles, ID is unique, severity ∈ enum, all `examples.positive` match the regex, none of `examples.negative` match.
- Fail-fast at startup if validation fails (scanner exits 1, logs offending rule).

`internal/rules/rule.go`:

```go
type Rule struct {
    ID          string
    Description string
    Severity    v1.Severity
    Regex       *regexp.Regexp
    EntropyMin  float64  // 0 = disabled
    EntropyGrp  int
    Tags        []string
}
```

### 2.4 Detector

`internal/detect/detector.go`:

```go
type Detector struct {
    rules []Rule
}

func (d *Detector) ScanChunk(c *v1.GitRowChunk) []*v1.Finding {
    // 1. Reconstruct text per-line (rows[].content joined with "\n").
    // 2. For each rule, regex over the joined text (multi-line aware via (?s)).
    // 3. For each match:
    //    - compute line_number / line_number_end from byte offsets via prefix-sum of row lengths
    //    - if EntropyMin > 0, compute shannon(match[target_group]) and skip if below
    //    - build *v1.Finding with all metadata copied from chunk
    // 4. Return findings slice (may be empty).
}
```

**Single-pass, no concurrency at rule level.** RE2 is fast (~100 MB/s); ~50 rules × N lines easily fits in one core. Parallelism lives at chunk level (worker pool).

## 3. Contract changes

This phase requires changes to two shared modules. Each will be **tagged independently** before scanner can depend on them.

### 3.1 `contracts/` — new proto

**New file:** `contracts/proto/harporis/v1/findings.proto`

```proto
syntax = "proto3";
package harporis.v1;
import "harporis/v1/types.proto";  // CommitFileRef
option go_package = "github.com/Harporis/harporis/contracts/gen/go/harporis/v1;harporisv1";

enum Severity {
  SEVERITY_UNSPECIFIED = 0;
  LOW      = 1;
  MEDIUM   = 2;
  HIGH     = 3;
  CRITICAL = 4;
}

message Finding {
  string scan_id     = 1;
  string finding_id  = 2;   // UUIDv4 generated by scanner
  string chunk_id    = 3;   // FK to source GitRowChunk.chunk_id
  string rule_id     = 4;   // matches Rule.ID in pack
  Severity severity  = 5;

  // Location.
  // DIFF_WINDOW chunks: file_path + commit_sha set; refs empty.
  // BLOB chunks:        file_path/commit_sha empty; refs populated (copied verbatim from chunk.refs).
  string file_path                    = 10;
  bytes  commit_sha                   = 11;
  repeated CommitFileRef refs         = 12;
  int32  line_number                  = 13;   // first matching line, 1-based, == GitRow.line_number
  int32  line_number_end              = 14;   // last matching line, 1-based (== line_number for single-line matches)
  int64  byte_offset                  = 15;   // start of match in matched_line bytes

  // Match content. Scanner publishes RAW; redaction is the writer/UI's concern.
  bytes  matched_secret               = 20;   // the captured secret
  bytes  matched_line                 = 21;   // the full matched line(s), newline-joined
  double entropy_score                = 22;   // Shannon entropy of matched_secret; 0 if rule has no entropy filter

  // Provenance.
  int64  detected_at_ms               = 30;   // unix millis
  string detector_version             = 31;   // e.g. "scanner/v0.1.0"
}
```

**Modified file:** `contracts/proto/harporis/v1/events.proto`

Add `secrets_found` to `ScanMetrics`:

```proto
message ScanMetrics {
  int64 blobs_scanned      = 1;
  int64 blobs_skipped      = 2;
  int64 chunks_published   = 3;
  int64 bytes_published    = 4;
  int64 errors_total       = 5;
  int64 duration_ms        = 6;
  int64 secrets_found      = 7;  // NEW: number of findings emitted by scanner pool for this scan_id
}
```

Backward compatible (proto3 additive field).

**Tag:** `contracts/v0.2.0` after regeneration.

### 3.2 `kit/` — wire constants and stream config

**Modified file:** `kit/nats/wire/wire.go`

Add `Duplicates` window to `FindingsStream` so MsgId dedup actually fires:

```go
{
  Name:       FindingsStream,
  Subjects:   []string{"harporis.findings.>"},
  Storage:    nats.FileStorage,
  Retention:  nats.WorkQueuePolicy,
  Duplicates: 5 * time.Minute,  // NEW
},
```

**Migration for existing deployments.** `EnsureStreams` currently calls `AddStream` and short-circuits on `ErrStreamNameAlreadyInUse`. That path does **not** update an existing stream's config. For any deployment that already has a `HARPORIS_FINDINGS` stream from before this change, the `Duplicates` field will stay at its old value (likely 0 = no dedup).

Two options for `EnsureStreams`, pick one:

| Option | Behavior | Trade-off |
|---|---|---|
| **A (recommended)** | If `AddStream` returns "already in use", fetch `StreamInfo` and compare relevant config; if drift detected, call `UpdateStream`. | Idempotent, self-healing, runs at every service startup. Slightly more code (~30 LOC). |
| **B** | Document `nats stream update HARPORIS_FINDINGS --dupe-window=5m` as an operator one-time step in `services/scanner/README.md`. | Zero code change. Risk: someone forgets it and dedup silently doesn't work. |

This phase ships **option A**. The diff in `kit/nats/wire/wire.go` is bounded and tested via the existing `wire_test.go` integration tests.

Add the scanner consumer name constant (analogous to existing `ValidatorPoolQueueGroup`):

```go
const ScannerDurableConsumer = "scanner-pool"
```

(`ValidatorPoolQueueGroup` stays defined but unused — it was reserved for an alternative architecture; we end up using a pull consumer with durable name instead. Removed in a follow-up cleanup phase, **not** in this one.)

**Tag:** `kit/v0.2.0` after change.

### 3.3 Scanner `go.mod`

Mirror the CLI module's pattern with explicit `replace` directives (the `go.work` approach was already established as a failed approach — see HANDOFF.md):

```go
module github.com/Harporis/harporis/services/scanner

go 1.26

require (
  github.com/Harporis/harporis/contracts v0.2.0
  github.com/Harporis/harporis/kit       v0.2.0
  github.com/nats-io/nats.go             v1.x
  github.com/prometheus/client_golang    v1.x
  github.com/google/uuid                 v1.x
  gopkg.in/yaml.v3                       v3.x
)

replace github.com/Harporis/harporis/contracts => ../../contracts
replace github.com/Harporis/harporis/kit       => ../../kit
```

## 4. Runtime architecture

```
                       ┌─────────────────────────────────────────────┐
                       │ scanner process (one of N replicas)         │
  HARPORIS_CHUNKS      │                                             │
  durable: scanner-pool│  ┌──────────┐    ┌──────────┐    ┌────────┐ │
  ───────────────────► │  │ worker 1 │ ─► │ Detector │ ─► │publish │─┼──► HARPORIS_FINDINGS
                       │  │  Fetch   │    │ + rules  │    │ (MsgId)│ │
                       │  └──────────┘    └──────────┘    └────────┘ │
                       │     ...               (shared, immutable)    │
                       │  ┌──────────┐                                │
                       │  │ worker N │ ─► ...                         │
                       │  │  Fetch   │                                │
                       │  └──────────┘                                │
                       │                                             │
                       │  ┌────────────────────────────────────────┐  │
                       │  │ status emitter (5s ticker + is_last)   │──┼──► HARPORIS_STATUS
                       │  │   reads atomic counter per scan_id     │  │
                       │  └────────────────────────────────────────┘  │
                       │                                             │
                       │  ┌──────────────┐  ┌──────────────────┐     │
                       │  │ :9101/metrics│  │ :9101/healthz    │     │
                       │  │ /readyz      │  │                  │     │
                       │  └──────────────┘  └──────────────────┘     │
                       └─────────────────────────────────────────────┘
```

### 4.1 Worker pool

- N goroutines (default `runtime.NumCPU()`, override via `--workers` / `SCANNER_WORKERS`).
- Each worker loops: `Fetch(batch=16, MaxWait=5s)` → for each msg → unmarshal `GitRowChunk` → `Detector.ScanChunk(c)` → publish findings → `msg.Ack()`.
- **No shared mutable state.** Detector is read-only (rules slice).

### 4.2 NATS consumer (chunks)

```go
sub, _ := js.PullSubscribe(
    wire.ChunksWildcardSubject,        // "harporis.chunks.>"
    wire.ScannerDurableConsumer,       // "scanner-pool"
    nats.BindStream(wire.ChunksStream),
    nats.ManualAck(),
    nats.AckWait(30 * time.Second),
    nats.MaxDeliver(5),                // after 5 retries → log + ack to unblock
    nats.MaxAckPending(64),            // bounded in-flight per durable
)
```

`WorkQueuePolicy` on `HARPORIS_CHUNKS` guarantees **each message is delivered to exactly one consumer instance**. Across N replicas sharing the same durable name, NATS round-robins messages. No queue-group plumbing needed.

**Heartbeat for slow chunks:** worker calls `msg.InProgress()` every ~10s if `Detector.ScanChunk` is still running. Implementation: timer started before `ScanChunk`, cancelled on return. Without this, a 30s+ chunk would be redelivered to another replica → wasted work.

### 4.3 Idempotent finding publish

Every finding publish uses `nats.MsgId(...)`:

```go
key := fmt.Sprintf("%s|%s|%s|%d|%d", scanID, chunkID, ruleID, lineNumber, byteOffset)
msgID := hex.EncodeToString(sha256.Sum256([]byte(key))[:])
js.Publish(wire.FindingsSubject(scanID), payload, nats.MsgId(msgID))
```

**Why this works:**

- If a chunk is redelivered (worker crash / restart / AckWait timeout), the second worker computes the same `msg_id` for the same finding.
- `HARPORIS_FINDINGS` has `Duplicates: 5*time.Minute`. JetStream drops the duplicate publish at server side.
- The `5min` window must cover: max `AckWait` (30s) + restart slack (a few minutes) + clock skew. 5min is conservative.

**At-least-once chunk delivery + idempotent finding MsgId = effectively-once finding publish.**

### 4.4 StatusEvent emission

To avoid spamming `HARPORIS_STATUS`:

- Each worker maintains `atomic.Int64` per scan_id: `findings_for_scan[scanID]++` on each emit.
- One **status-emitter goroutine** ticks every 5s:
  - For each active `scanID`: read counter, publish `StatusEvent{scan_id, metrics: {secrets_found: N}}` to `harporis.status.<scan_id>`.
  - Only publishes if counter changed since last tick (suppress no-op updates).
- On chunk with `is_last_in_scan = true`, the worker:
  - Publishes a final `StatusEvent` immediately with the latest counter.
  - Removes the scan_id from the active map.

`StatusStream` is `LimitsPolicy`, so duplicate-MsgId dedup is not the protection here — the rate limiter (5s ticker) is.

#### v0.1 caveat: per-replica counter

With **N replicas**, each scanner instance only knows about the chunks **it consumed**. Its `secrets_found` counter is therefore **per-replica, not aggregated across the pool**. Multiple `StatusEvent`s for the same `scan_id` will land in `HARPORIS_STATUS`, each carrying that replica's local count.

**Implications:**

- `harporis history show <id>` (CLI) currently picks the last `StatusEvent` per scan_id. With N replicas, that last event will reflect *one replica's* count, not the total.
- Correct aggregation is the **writer** service's job (it consumes the full `HARPORIS_FINDINGS` stream and can produce a true count).
- For replicas = 1 (default in `docker-compose.yml` as we ship it), the counter is exact. Operator opts into the per-replica caveat when scaling.

This is **accepted** for v0.1. Alternatives (NATS KV bucket as shared counter, or replicas tagging StatusEvent with replica ID) are tracked in §10 as future work but not blocking.

### 4.5 Lifecycle

- `main.go` → load config → load rules → validate → connect NATS → `wire.EnsureStreams(js)` → create durable consumer → start workers → start metrics server → block on signal.
- `SIGTERM` / `SIGINT`:
  1. Cancel context. Workers stop pulling new batches.
  2. In-flight chunks finish their current `ScanChunk` + publish + ack.
  3. Drain timeout: 30s. After that, log "abandoning N in-flight chunks" and exit. AckWait will redeliver them.
  4. Close NATS connection.
- No DLQ stream in v0.1. Chunks that exceed `MaxDeliver=5` are logged at ERROR level with `chunk_id` + `scan_id`, then acked to unblock the stream. DLQ-stream comes in a follow-up.

## 5. Configuration

**File:** `services/scanner/config/scanner.yaml` (loaded by `internal/config/load.go`, mirrors `services/getter/internal/config`).

```yaml
nats_url: "nats://nats:4222"     # NATS_URL env override
workers: 0                        # 0 = runtime.NumCPU(); SCANNER_WORKERS override
fetch_batch: 16                   # SCANNER_FETCH_BATCH
fetch_max_wait_ms: 5000           # SCANNER_FETCH_MAX_WAIT_MS
ack_wait_seconds: 30              # SCANNER_ACK_WAIT_SECONDS
max_deliver: 5                    # SCANNER_MAX_DELIVER
status_tick_ms: 5000              # SCANNER_STATUS_TICK_MS
metrics_addr: ":9101"             # SCANNER_METRICS_ADDR
log_level: "info"                 # SCANNER_LOG_LEVEL (debug/info/warn/error)
rules_path: ""                    # SCANNER_RULES_PATH (empty = embedded default.yaml)
```

Precedence (lowest → highest): defaults in code → YAML file → env vars → CLI flags.

## 6. Observability

### 6.1 Metrics (Prometheus)

Endpoint: `:9101/metrics`. Registered with `promauto`.

| Metric | Type | Labels | Purpose |
|---|---|---|---|
| `scanner_chunks_consumed_total` | counter | — | Throughput / rate. |
| `scanner_chunks_dropped_total` | counter | `reason` (`unmarshal_error`/`max_deliver_exceeded`) | Chunks acked-without-output because they hit a terminal failure. Not a DLQ — see §4.5. |
| `scanner_chunk_processing_seconds` | histogram | `kind` (`BLOB`/`DIFF_WINDOW`) | Latency, p50/p95/p99. |
| `scanner_rule_matches_total` | counter | `rule_id`, `severity` | Which rules are noisy. |
| `scanner_findings_published_total` | counter | `severity` | Output rate. |
| `scanner_entropy_filter_dropped_total` | counter | `rule_id` | How often entropy filter saves us from false positives. |
| `scanner_status_updates_published_total` | counter | — | StatusEvent ticker activity. |
| `scanner_nats_publish_errors_total` | counter | `subject` | NATS health. |
| `scanner_active_scans` | gauge | — | Current size of active-scans map. |
| `scanner_build_info` | gauge (always 1) | `version`, `commit`, `proto_version` | Identity, scraped at every replica. |

### 6.2 Health

- `GET :9101/healthz` → 200 OK if NATS connection up.
- `GET :9101/readyz` → 200 OK if durable consumer created AND at least one worker has started its loop.

### 6.3 Logs

`slog`, JSON output, log levels via config. Standard fields per chunk-processing log line: `scan_id`, `chunk_id`, `chunk_kind`, `rule_id` (when relevant), `duration_ms`.

## 7. Horizontal scaling: N replicas

### 7.1 Scanner — designed-in scaling

Scanner is **stateless by construction**. All shared state lives in JetStream:

- Durable consumer state (last-acked seq, in-flight messages) — in NATS.
- Findings deduplication window — in NATS (`Duplicates: 5min`).
- Per-scan finding counters — in-process, but each replica owns the counters for the chunks it consumed; status emitter ticks per-replica; CLI/writer aggregates by `scan_id` across replicas via `StatusEvent.metrics.secrets_found`.

**Result:** spawn N scanner pods → NATS round-robins chunks across them → linear throughput scaling up to bottleneck of (chunk publish rate from getter pool) or (NATS server capacity).

#### Docker compose (local dev)

`docker-compose.yml` adds the scanner service. Crucially, it sets **no `container_name` and no host port mappings** — those break replicas > 1.

```yaml
  scanner:
    build:
      context: .
      dockerfile: services/scanner/Dockerfile
    image: harporis/scanner:dev
    depends_on:
      nats:
        condition: service_healthy
    environment:
      NATS_URL: nats://nats:4222
      LOG_LEVEL: info
    expose:
      - "9101"             # /metrics scraped via compose-internal DNS, not host
    deploy:
      replicas: 2          # tunable; compose v2 supports this without swarm
    restart: unless-stopped
```

Scaling at runtime:

```bash
docker compose up -d --scale scanner=4
```

Prometheus scrape (if added later) discovers replicas via Docker Swarm/compose service-DNS (`tasks.scanner:9101`).

#### Kubernetes (production blueprint)

`services/scanner/deploy/k8s/` — manifests checked in alongside the code (no Helm chart in v0.1):

```yaml
# deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata: {name: harporis-scanner, labels: {app: harporis-scanner}}
spec:
  replicas: 4
  strategy: {type: RollingUpdate, rollingUpdate: {maxSurge: 1, maxUnavailable: 0}}
  selector: {matchLabels: {app: harporis-scanner}}
  template:
    metadata: {labels: {app: harporis-scanner}}
    spec:
      containers:
        - name: scanner
          image: ghcr.io/harporis/scanner:v0.1.0
          env:
            - {name: NATS_URL,        value: "nats://harporis-nats.harporis.svc:4222"}
            - {name: SCANNER_WORKERS, value: "4"}
            - {name: LOG_LEVEL,       value: "info"}
          ports:
            - {name: metrics, containerPort: 9101}
          readinessProbe: {httpGet: {path: /readyz, port: metrics}, periodSeconds: 5}
          livenessProbe:  {httpGet: {path: /healthz, port: metrics}, periodSeconds: 10}
          resources:
            requests: {cpu: 500m, memory: 256Mi}
            limits:   {cpu: 2,    memory: 1Gi}
---
# service.yaml — headless service for Prometheus to discover replicas
apiVersion: v1
kind: Service
metadata: {name: harporis-scanner, labels: {app: harporis-scanner}}
spec:
  clusterIP: None
  selector: {app: harporis-scanner}
  ports:
    - {name: metrics, port: 9101, targetPort: metrics}
---
# servicemonitor.yaml (optional, for kube-prometheus)
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata: {name: harporis-scanner}
spec:
  selector: {matchLabels: {app: harporis-scanner}}
  endpoints:
    - {port: metrics, interval: 15s}
```

**HPA** based on `scanner_chunks_consumed_total` rate, or CPU. Documented in `deploy/k8s/README.md`, not enabled by default.

### 7.2 Getter — same shape, requires compose changes

The getter is already architecturally ready for N replicas — its request subscriber is `js.QueueSubscribe(ScansRequestsSubject, GetterPoolQueueGroup, ...)` with `nats.Durable(GetterPoolQueueGroup)`. WorkQueue + queue group = round-robin across replicas.

**But `docker-compose.yml` blocks replicas > 1** as it stands today. Three changes:

1. **Remove `container_name: harporis-getter`.** Compose auto-names replicas (`harporis-getter-1`, `-2`, …).
2. **Remove `ports: ["9100:9100", "50051:50051"]` → use `expose:` instead.** Host can no longer reach getter at fixed ports, but CLI talks to NATS, not getter directly. gRPC `Health` is for service-mesh use; if the dev workflow needs it, use `docker compose exec` or a temporary `--scale getter=1` for the dev container.
3. **Replace `volumes: getter-work:/var/lib/harporis/scans` with `tmpfs`** (or a per-replica `--mount type=volume,target=...` strategy). N replicas sharing one named volume will race on `WORK_DIR`. `tmpfs` is fine: getter's work dir is ephemeral (scan workspaces), wiped after scan completes anyway.

The CLI's `harporis doctor` check `getter /metrics → HTTP 200` currently expects a single endpoint. With N replicas behind compose-internal DNS, the doctor check becomes "any replica responds" via `tasks.getter:9100`. **Scope decision: this CLI-side change is a follow-up** (separate small commit, after scanner phase merges, called out in HANDOFF.md). The scanner phase itself does NOT modify `harporis doctor`.

For Kubernetes, the getter manifests mirror the scanner ones (same Deployment + headless Service + ServiceMonitor pattern). **Scope decision: getter k8s manifests land in this phase too**, so the project has a coherent k8s deploy story by the time scanner ships.

`deploy/k8s/` files for getter: `services/getter/deploy/k8s/{deployment,service,servicemonitor}.yaml`.

### 7.3 Summary table

| Service | NATS pattern | Stateless | Compose `replicas: N` works? | k8s manifests in this phase? |
|---|---|---|---|---|
| `nats` | server, single replica (clustering = future phase) | persistent (JetStream files) | N/A | No (use `nats-operator` later) |
| `getter` | `QueueSubscribe + Durable(getter-pool)` on `WorkQueue` | yes (after compose changes) | **after** dropping `container_name` + ports + named volume | yes (this phase) |
| `scanner` | `PullSubscribe + Durable(scanner-pool)` on `WorkQueue` | yes by design | yes, day 1 | yes (this phase) |
| `writer` (future) | TBD (probably `PullSubscribe + Durable(writer-pool)` on `HARPORIS_FINDINGS`) | TBD | TBD | not in this phase |

## 8. Module layout

```
services/scanner/
├── README.md
├── Makefile                       # build / test / install / deb / docker / docker-push
├── Dockerfile                     # multi-stage: golang:1.26-alpine → distroless/static:nonroot
├── go.mod                         # replace contracts/kit → ../../
├── go.sum
├── cmd/
│   └── scanner/
│       └── main.go                # entrypoint: config → rules → NATS → workers → block
├── config/
│   └── scanner.yaml               # default config (mounted into container)
├── rules/
│   └── default.yaml               # embedded rule pack
├── internal/
│   ├── config/                    # YAML+env+flags loader, mirrors getter
│   │   ├── load.go
│   │   └── load_test.go
│   ├── rules/                     # parse / validate / hold rules
│   │   ├── rule.go
│   │   ├── loader.go              # //go:embed default.yaml
│   │   ├── loader_test.go
│   │   └── entropy.go             # Shannon entropy
│   ├── detect/                    # the actual detector
│   │   ├── detector.go
│   │   ├── detector_test.go       # table tests over every rule's positive/negative examples
│   │   └── line_index.go          # byte-offset → line-number prefix sums
│   ├── nats/                      # consumer + publisher
│   │   ├── consumer.go            # PullSubscribe + Fetch loop
│   │   ├── consumer_test.go       # uses embedded JS server
│   │   ├── publisher.go           # findings + status publish with MsgId
│   │   ├── publisher_test.go
│   │   └── msgid.go               # sha256(scan|chunk|rule|line|offset)
│   ├── worker/                    # pool wiring
│   │   ├── pool.go
│   │   └── pool_test.go
│   ├── status/                    # 5s ticker + per-scan counters
│   │   ├── tracker.go
│   │   └── tracker_test.go
│   ├── metrics/                   # promauto registrations
│   │   └── metrics.go
│   ├── health/                    # /healthz /readyz handlers
│   │   └── health.go
│   └── version/                   # ldflags-injected build identity
│       └── version.go
├── deploy/
│   └── k8s/
│       ├── deployment.yaml
│       ├── service.yaml
│       ├── servicemonitor.yaml
│       └── README.md
└── integration_test.go            # //go:build integration — end-to-end: embed NATS, publish chunk, assert finding
```

## 9. Testing strategy

| Layer | What | How |
|---|---|---|
| **Rule validation** | Every rule must match all `examples.positive` and reject all `examples.negative` | `internal/rules/loader_test.go` iterates over `default.yaml`, runs regex against each example. Fails the build. |
| **Detector unit** | Detector emits correct Finding for each rule kind, picks right line numbers, handles multi-line PEM blocks, applies entropy filter | Table tests in `internal/detect/detector_test.go`. Synthetic `GitRowChunk` builders. |
| **Entropy** | Shannon math correctness on known strings | `internal/rules/entropy_test.go`. |
| **NATS consumer/publisher** | MsgId formula stable; Fetch + Ack semantics; InProgress heartbeat extends ack | `internal/nats/*_test.go` with `nats-server/v2/test` embedded JS server (same pattern as getter and CLI). |
| **Worker pool** | Pool consumes from a fed Fetch source, calls detector, publishes findings | `internal/worker/pool_test.go`, fake NATS via interfaces. |
| **Status tracker** | Counters increment correctly; 5s ticker emits only on change; final emit on `is_last_in_scan` | `internal/status/tracker_test.go` with `clock` interface to fake time. |
| **Integration** | End-to-end: spin embedded JS, publish a `GitRowChunk` containing `AKIAIOSFODNN7EXAMPLE` to `HARPORIS_CHUNKS`, assert a `Finding` lands on `HARPORIS_FINDINGS` with `rule_id=aws-access-key-id` | `integration_test.go` with `//go:build integration`, mirrors `services/cli/integration_test.go`. |
| **Live e2e** | Manual: scan `/tmp/leaky-repo` through the full stack (CLI → getter → NATS → scanner) and observe findings in `harporis history show <id> --findings`* | Documented in `services/scanner/README.md`. (*history-show flag is a follow-up; for now, `nats stream view HARPORIS_FINDINGS`.) |
| **Concurrency / replicas** | Spin 3 scanner containers via `docker compose up --scale scanner=3`, scan a sizeable repo, assert finding count is stable (no dupes, no losses) | Documented manual procedure in `services/scanner/README.md`. |

**No mocks of NATS itself** — `nats-server/v2/test` embed gives a real server, and that's what shook out real bugs in the CLI. Same rule applies here.

## 10. Out of scope (explicit)

These are **intentionally deferred** and will not be touched by this phase:

1. **Writer service.** `services/writer` is the next phase. Scanner publishes findings to NATS; writer subscribes and serializes to file/SARIF/DB/UI.
2. **CLI integration of findings (`harporis history show <id> --findings`).** Tracked separately. Without it, findings are visible via `nats stream view`.
3. **Live secret verification (HEAD requests to providers).** That was option C; we picked B.
4. **DLQ stream.** Chunks exceeding `MaxDeliver=5` are logged + acked. Real DLQ stream is a follow-up phase if observability shows it's needed.
5. **gitleaks-format rule import.** Possible follow-up `--rules-gitleaks rules.toml`; not v0.1.
6. **Cross-module `isTerminal` / `SanitizeConsumerName` extraction.** Already deferred in HANDOFF.md; that's its own phase.
7. **NATS clustering.** Single NATS node remains; clustering is its own phase.
8. **HPA / VPA in k8s manifests.** Replicas set statically; HPA documented but not applied by default.
9. **Cross-replica `secrets_found` aggregation in StatusEvent.** v0.1 emits per-replica counters (see §4.4 caveat). The clean fix is a shared counter (NATS KV bucket) or aggregation in the writer. Tracked here; not blocking v0.1 of scanner.

## 11. Tags shipped by this phase

- `contracts/v0.2.0` — new `findings.proto`, `secrets_found` field in `ScanMetrics`.
- `kit/v0.2.0` — `FindingsStream.Duplicates`, `ScannerDurableConsumer` const.
- `scanner/v0.1.0` — initial scanner release.
- `getter/v0.2.0` (small): compose changes to support replicas + k8s manifests. (CLI gets a follow-up `cli/v0.1.1` for `doctor` compatibility, **not in this phase**.)

## 12. Risks & mitigations

| Risk | Likelihood | Mitigation |
|---|---|---|
| MsgId formula changes break dedup across rolling upgrade | low | Formula is documented in `internal/nats/msgid.go`; any change is a major version of `kit/` and gated by review. |
| Regex catastrophic backtracking | low (RE2 is linear) | RE2 has no backtracking. Document the constraint in rule-author docs. |
| Entropy filter too aggressive → drops real secrets | medium | `entropy.min` per rule, tuned via `examples.positive`. Metric `scanner_entropy_filter_dropped_total{rule_id}` makes it visible. |
| N replicas + named volume race on getter | high (would happen if we shipped scaling without compose changes) | Compose changes in §7.2 are part of this phase. |
| `StatusEvent` flood at high finding rate | medium | 5s ticker + suppress no-op. Scales linearly with active-scan count, not finding count. |
| `is_last_in_scan` flag from getter is wrong / never set | low | Status tracker also emits on 5s tick AND on graceful shutdown. If getter never sets the flag, the final tick before idle-timeout still captures it. |
| Rule pack drift between scanner replicas (one upgraded, one not) | medium | All replicas tag `detector_version` into every Finding. Writer can detect mixed-version emission. Operator policy: rolling upgrade. |
| Embedded NATS test server flaky on CI | low | Pattern already proven in getter + CLI. Reuse `runJSServer(t)` helper. |

---

## Appendix A — Why a `PullSubscribe + Durable` over `QueueSubscribe`

Getter uses `QueueSubscribe(ScansRequestsSubject, GetterPoolQueueGroup, ...)`. Why does scanner differ?

- **Both patterns achieve identical fan-out semantics** on a `WorkQueuePolicy` stream when you bind to the same durable name across replicas: each message → exactly one replica.
- **Pull gives explicit backpressure** (`Fetch(batch, MaxWait)`) — scanner pulls work at its own rate, no risk of NATS pushing faster than workers can handle.
- **Pull is the recommended pattern for scalable JetStream consumers** in nats.go 1.30+. Push consumers are being de-emphasized.
- **`MaxAckPending` and `InProgress()` heartbeats compose more cleanly** with pull semantics.

The two services using different patterns is acceptable — getter's request subscription is a known-low-rate workload (one scan request at a time per scan_id); scanner's chunk consumption is high-rate. They have different shapes and different patterns suit each.

A future refactor might unify them, but it's not on the critical path.
