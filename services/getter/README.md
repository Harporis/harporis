# Harporis Getter

The **Getter** is the first stage of the Harporis secret-scanning pipeline. It pulls `ScanRequest` messages off NATS, normalises git repository contents into typed `GitRowChunk` messages, and publishes them for the **Validator** to scan and the **Writer** to persist.

```
CLI / API gateway
       │ publish ScanRequest
       ▼
┌──────────────────────────────────┐
│ harporis.scans.requests          │   queue group: getter-pool
│ (JetStream, durable, work-queue) │   exactly-once delivery to one getter
└──────────┬───────────────────────┘
           │
   ┌───────┼────────┐  N independent processes
   ▼       ▼        ▼
Getter   Getter   Getter
   │       │        │ publishes per scan:
   ▼       ▼        ▼
harporis.chunks.<scan_id>       ──▶  Validator pool
harporis.status.<scan_id>       ──▶  API gateway / Writer
```

Operators run as many getter instances as they want; NATS handles distribution. No external load balancer needed.

---

## Contents

- [Architecture](#architecture)
- [Build, test, run](#build-test-run)
- [Configuration](#configuration)
- [Wire protocol](#wire-protocol-natscontractswire)
- [Scan types](#scan-types)
- [Data model](#data-model)
- [Resource limits](#resource-limits)
- [Observability](#observability)
- [Operational notes](#operational-notes)
- [Known limitations](#known-limitations)
- [Codebase layout](#codebase-layout)

---

## Architecture

### Components within one getter process

```
┌────────────────────────────────────────────────────────────────────┐
│ getter process                                                     │
│                                                                    │
│  ┌──────────────────┐  ┌──────────────┐  ┌──────────────────────┐  │
│  │ NATS request sub │  │ NATS cancel  │  │ gRPC: Health         │  │
│  │ (getter-pool)    │  │ broadcast    │  │ (+ StartScanLocal,   │  │
│  │                  │  │              │  │  off by default)     │  │
│  └────────┬─────────┘  └──────┬───────┘  └──────────────────────┘  │
│           │ ScanRequest       │ cancel by scan_id                  │
│           ▼                   ▼                                    │
│  ┌────────────────────────────────────────────────────────────┐    │
│  │  Active-scans registry (scan_id → ScanContext, cancel fn)  │    │
│  └─────────────────────────────┬──────────────────────────────┘    │
│                                │                                   │
│  ┌─────────────────────────────▼──────────────────────────────┐    │
│  │ Per-scan runner                                            │    │
│  │                                                            │    │
│  │   git.PrepareRepo()      → local path / clone temp dir     │    │
│  │   buildFilter(...)       → 5-layer file filter             │    │
│  │                                                            │    │
│  │   ┌───────────────────────────────────────────────────┐    │    │
│  │   │ git walker (1 goroutine)                          │    │    │
│  │   │   rev-list + ls-tree, dedup by blob_sha           │    │    │
│  │   └──────────────────────────┬────────────────────────┘    │    │
│  │                              │ jobs channel (cap=2N)       │    │
│  │      ┌─────────┬─────────┬───┴─────┬─────────┐             │    │
│  │      ▼         ▼         ▼         ▼         ▼             │    │
│  │   Worker-1  Worker-2  ...      Worker-N                    │    │
│  │   own `git cat-file --batch` subprocess per worker         │    │
│  │   ↓                                                        │    │
│  │   filter.ShouldScan(path, size, NUL-sniff)                 │    │
│  │   ↓                                                        │    │
│  │   chunk.Builder (line-based, byte-budget, overlap)         │    │
│  │   ↓                                                        │    │
│  │   nats.Publisher.PublishChunk → harporis.chunks.<scan_id>  │    │
│  │                                                            │    │
│  └────────────────────────────────────────────────────────────┘    │
│                                                                    │
│  Throughout: Prometheus metrics on :9100/metrics                   │
└────────────────────────────────────────────────────────────────────┘
```

**Walker–worker contract:** the walker is the sole producer of `BlobJob`s. Each worker owns its own `git cat-file --batch` subprocess for blob content. Workers run independently — no shared per-blob state. Per-scan counters (`blobs_scanned`, `chunks_published`, etc.) are `atomic.Int64`. A watchdog cancels the walker if all workers fail to spawn cat-file (e.g. git binary missing), preventing producer deadlock.

### Cross-instance topology

- **Distribution:** all getters subscribe to `harporis.scans.requests` as the `getter-pool` queue group. NATS delivers each `ScanRequest` to exactly one getter. Add/remove instances at will.
- **Cancel:** clients publish `CancelScanRequest{scan_id}` to `harporis.scans.cancel`. Every getter receives it (broadcast subscription), but only the one holding that scan_id in its registry reacts.
- **Idempotency:** if the same scan_id is delivered twice (e.g. retry), the registry returns `ErrAlreadyExists`, the handler NAKs, NATS doesn't re-deliver.

### Architectural decisions

- **`git` CLI over a library:** shells out to system git for clone, cat-file --batch, ls-tree, rev-list, diff. Reasons: matches operator's git version exactly; benefits from system git's GPG/SSH/credential helper integration; reduces Go-side complexity vs `go-git`. Cost: one process per worker for cat-file streaming.
- **Line-based chunks:** `GitRowChunk` contains `GitRow{line_number, byte_offset, content}` entries. Validator reconstructs text by `\n`-joining content. Preserves line numbers for finding reports without re-scanning.
- **Overlap between chunks:** when a blob spans multiple chunks, consecutive chunks share `OverlapLines` lines so multi-line secrets (PEM blocks, multi-line JWTs) appear intact in at least one chunk.
- **Dedup by `blob_sha`:** `FULL_HISTORY` and `BRANCH_FULL` scans walk every commit but emit each unique blob once with all its `(commit, path)` references. ~10x payload reduction on typical repos.
- **`bytes` on the wire for SHAs:** `blob_sha` and `commit_sha` are raw 20-byte SHA-1 (or 32-byte SHA-256), not 40-char hex. Hex-string encoding is reserved for git CLI input and human-readable logs.

---

## Build, test, run

### Prerequisites

- Go 1.26+
- `git` 2.40+ on PATH
- A NATS server with JetStream enabled for production runs (tests use an in-process embedded server — no external NATS needed for `go test`)

### Build

```bash
make build
# → bin/getter (≈22 MB)
```

### Test

```bash
make test
# go test ./... -race -timeout 60s
```

All 9 packages have tests; expect ~80+ test cases including:

- `internal/chunk`: line scanner LF/CRLF/long-line, builder byte-budget, multi-chunk overlap property
- `internal/git`: real git subprocess tests (clone, cat-file, ls-tree, rev-list, walker dedup, diff parser)
- `internal/scan`: state machine, registry, single+multi-worker runner, status-failure resilience
- `internal/nats`: publisher, request queue subscriber, cancel broadcast (all against embedded NATS)

### Run locally

```bash
# 1. NATS with JetStream (one shell)
nats-server -js                       # or: docker run --rm -p 4222:4222 nats:latest -js

# 2. Getter (another shell)
./bin/getter --config config/getter.yaml --metrics-port 9100
```

Expected log: `getter ready nats=nats://localhost:4222 grpc=:50051 metrics=9100`.

### Submit a scan

There's no built-in CLI yet — until then, publish a `ScanRequest` to NATS via the [nats CLI](https://github.com/nats-io/natscli) or a small Go helper. A `ScanRequest` for the local working tree as `CURRENT_STATE`:

```bash
# protobuf-encoded ScanRequest
cat <<EOF | nats pub harporis.scans.requests --stdin
$(go run ./scripts/encode-scanrequest --scan-id manual-1 --type CURRENT_STATE --local /path/to/repo)
EOF
```

(`scripts/encode-scanrequest` is not part of the repo yet — see [Known limitations](#known-limitations).)

---

## Configuration

Loaded from YAML (default path `config/getter.yaml`). Supports `${VAR}` and `${VAR:-default}` env substitution.

Full reference (every field, with defaults from `config/getter.yaml`):

| Path                                          | Type     | Default                       | Purpose |
|-----------------------------------------------|----------|-------------------------------|---------|
| `service.name`                                | string   | `getter`                      | Identifier in logs/metrics. |
| `service.log_level`                           | enum     | `info`                        | `debug` \| `info` \| `warn` \| `error`. |
| `grpc.port`                                   | int      | `50051`                       | gRPC listener port (Health + optional StartScanLocal). |
| `grpc.allow_local_start`                      | bool     | `false`                       | If `true`, exposes `StartScanLocal` RPC. **Keep `false` in prod.** |
| `workspace.work_dir`                          | string   | `/var/lib/harporis/scans`     | Where remote clones land. |
| `workspace.cleanup_on_complete`               | bool     | `true`                        | Remove the clone dir on scan finish. |
| `resources.max_cpu_cores`                     | int      | `4`                           | `GOMAXPROCS` + worker pool size. `0` = `runtime.NumCPU()`. |
| `resources.max_ram_mb`                        | int      | `512`                         | Soft `GOMEMLIMIT` cap. `0` = unlimited. |
| `git.clone_timeout_seconds`                   | int      | `600`                         | Max wall-clock for `git clone` of a remote source. |
| `git.cat_file_batch_buffer_kb`                | int      | `64`                          | (Reserved, not currently consumed.) |
| `chunking.row_size_target_kb`                 | int      | `256`                         | Target chunk size (bytes of content). |
| `chunking.row_overlap_lines`                  | int      | `64`                          | Lines shared between consecutive chunks of one blob. |
| `chunking.diff_context_lines`                 | int      | `30`                          | Context lines on each side of a diff hunk (`git diff -U<N>`). |
| `chunking.max_file_size_mb`                   | int      | `10`                          | Blobs larger than this are skipped (logged as `size_cap`). |
| `filters.path_exclusions`                     | []string | `.git/`, `node_modules/`, …   | Glob-style paths to skip. Trailing `/` = dir match anywhere in path. |
| `filters.binary_extensions`                   | []string | `.png`, `.jpg`, `.pdf`, …     | Lowercased ext lookup. |
| `nats.url`                                    | string   | `nats://localhost:4222`       | Cluster URL. Supports `${NATS_URL}` substitution. |
| `nats.jetstream.requests_stream`              | string   | `HARPORIS_REQUESTS`           | Stream name for `harporis.scans.>`. |
| `nats.jetstream.chunks_stream`                | string   | `HARPORIS_CHUNKS`             | Stream name for `harporis.chunks.>`. |
| `nats.jetstream.status_stream`                | string   | `HARPORIS_STATUS`             | Stream name for `harporis.status.>`. |
| `nats.jetstream.publish_ack_wait_seconds`     | int      | `5`                           | Per-publish ack timeout (≥ 1). |
| `nats.consumer.requests_subject`              | string   | `harporis.scans.requests`     | Subject we pull ScanRequests from. |
| `nats.consumer.cancel_subject`                | string   | `harporis.scans.cancel`       | Subject we listen for cancels on. |
| `nats.consumer.queue_group`                   | string   | `getter-pool`                 | NATS queue group for the request stream. |
| `nats.consumer.request_ack_wait_seconds`      | int      | `60`                          | Timeout for one ScanRequest handler (≥ 5). |
| `nats.consumer.max_in_flight_scans`           | int      | `4`                           | Max concurrent ScanRequests this instance handles (≥ 1). |
| `allow_request_overrides`                     | []string | a few chunking/resources keys | Allowlist of config paths that `ScanRequest.ConfigOverride` can override per-scan. |

Validation runs at startup (`config.Validate`). Bad configs cause `FATAL: config invalid: ...` with all issues joined.

---

## Wire protocol (NATS + `contracts/wire`)

All shared NATS primitives live in [`contracts/wire/`](../../contracts/wire/wire.go). Validator and Writer import this package and never re-implement subjects, streams, or queue groups. Constants:

```go
const (
    ScansRequestsSubject = "harporis.scans.requests"
    ScansCancelSubject   = "harporis.scans.cancel"

    RequestsStream = "HARPORIS_REQUESTS"
    ChunksStream   = "HARPORIS_CHUNKS"
    StatusStream   = "HARPORIS_STATUS"
    FindingsStream = "HARPORIS_FINDINGS"

    GetterPoolQueueGroup    = "getter-pool"
    ValidatorPoolQueueGroup = "validator-pool"
    WriterPoolQueueGroup    = "writer-pool"
)

func ChunksSubject(scanID string) string   { ... }   // "harporis.chunks.<scan_id>"
func StatusSubject(scanID string) string   { ... }
func FindingsSubject(scanID string) string { ... }
```

Plus shared connection helpers:

```go
wire.Dial(wire.DialConfig{URL: "nats://...", ClientName: "harporis-validator"}) (*Client, error)
wire.EnsureStreams(js) error    // idempotent; safe to call from N processes
```

### Subjects/streams

| Subject pattern              | Published by   | Consumed by              | Stream            | Queue group         |
|------------------------------|----------------|--------------------------|-------------------|---------------------|
| `harporis.scans.requests`    | API gateway    | Getter pool              | `HARPORIS_REQUESTS` (work-queue) | `getter-pool`    |
| `harporis.scans.cancel`      | API gateway    | All getters (broadcast)  | `HARPORIS_REQUESTS`              | — (Core NATS)    |
| `harporis.chunks.<scan_id>`  | Getter         | Validator pool           | `HARPORIS_CHUNKS` (work-queue)   | `validator-pool` |
| `harporis.status.<scan_id>`  | Getter         | API gateway / Writer     | `HARPORIS_STATUS` (limits)       | — (broadcast)    |
| `harporis.findings.<scan_id>`| Validator      | Writer pool              | `HARPORIS_FINDINGS` (work-queue) | `writer-pool`    |

All messages are protobuf-encoded ([`contracts/proto/harporis/v1/`](../../contracts/proto/harporis/v1/)).

### Lifecycle events

`StatusEvent.state` transitions: `PENDING → RUNNING → {COMPLETED | FAILED | CANCELLED}`. The `RUNNING` event carries `output_config` so the writer knows where to persist findings. Status delivery is best-effort with retry; on permanent failure the scan continues (counter `harporis_getter_status_publish_errors_total` increments).

---

## Scan types

`ScanRequest.type` is one of:

| Type                | Walks                                    | Dedup        | Output kind       |
|---------------------|------------------------------------------|--------------|-------------------|
| `FULL_HISTORY`      | All commits across all branches          | `blob_sha`   | `BLOB` chunks     |
| `BRANCH_FULL`       | All commits reachable from `range.branch`| `blob_sha`   | `BLOB` chunks     |
| `COMMIT_RANGE`      | Commits in `range.commit_from..range.commit_to` | `blob_sha` | `BLOB` chunks |
| `CURRENT_STATE`     | Files at HEAD                            | `blob_sha`   | `BLOB` chunks     |
| `BRANCH_DIFF`       | `git diff base..branch`                  | none         | `DIFF_WINDOW`     |
| `HEAD_DIFF`         | `git diff` (unstaged)                    | none         | `DIFF_WINDOW`     |
| `STAGED`            | `git diff --cached`                      | none         | `DIFF_WINDOW`     |

`DIFF_WINDOW` chunks contain added + context lines (removed lines are not scanned — secrets can only be introduced, not deleted).

---

## Data model

### `GitRowChunk`

```proto
message GitRowChunk {
  string    scan_id          = 1;   // UUID
  string    chunk_id         = 2;   // UUIDv4
  int64     sequence_number  = 3;   // monotonic per process (not per blob — see "Sequence ordering")
  bool      is_last_in_scan  = 4;
  ChunkKind kind             = 5;   // BLOB | DIFF_WINDOW

  // BLOB-kind only
  bytes                   blob_sha = 10;  // 20-byte SHA-1 (or 32-byte SHA-256)
  repeated CommitFileRef  refs     = 11;  // every (commit, path) where this blob appears

  // DIFF_WINDOW-kind only
  bytes  commit_sha          = 20;  // resolved HEAD SHA at scan time
  string file_path           = 21;
  int32  context_lines_above = 22;
  int32  context_lines_below = 23;

  // Slice metadata (both kinds)
  int32 start_line  = 30;
  int32 end_line    = 31;
  int32 total_lines = 32;
  int32 chunk_index = 33;
  int32 chunk_count = 34;

  repeated GitRow rows = 40;
}

message GitRow {
  int32 line_number = 1;   // 1-based, as in an editor
  int64 byte_offset = 2;   // start offset in the source blob
  bytes content     = 3;   // raw bytes WITHOUT trailing \n
}
```

### Sequence ordering

`sequence_number` is globally monotonic per getter process but **not** ordered per-blob with `Workers > 1`. Downstream consumers (validator, writer) must use `(chunk_id, chunk_index, chunk_count)` for per-blob ordering, not `sequence_number`.

---

## Resource limits

The constraint from the design spec: **"scan must complete regardless of available resources (besides disk) — slowly is OK"**.

- **CPU:** `resources.max_cpu_cores` sets `GOMAXPROCS` and worker pool size. The walker is one goroutine; workers are N. Each worker holds at most one chunk in flight.
- **RAM:** `resources.max_ram_mb` sets `runtime/debug.SetMemoryLimit` (Go's soft cap — triggers more aggressive GC, doesn't kill the process). Chunk size is bounded by `chunking.row_size_target_kb`; per-worker peak memory is ~1 chunk + transient buffers.
- **Disk:** remote clones land in `workspace.work_dir/<uuid>/`. Cleaned up on scan completion if `cleanup_on_complete: true`. Disk is the only hard requirement — getter cannot scan what it cannot clone.

Backpressure: NATS JetStream applies natural backpressure at publish (ack-wait blocks the worker until validator catches up). Per-publish retry with exponential backoff (1s, 2s, 4s) handles transient broker hiccups.

---

## Observability

### Prometheus

`/metrics` on `:9100` (configurable via `--metrics-port`):

| Metric                                              | Type      | Labels                       | Meaning |
|-----------------------------------------------------|-----------|------------------------------|---------|
| `harporis_getter_blobs_scanned_total`               | Counter   | `scan_id`                    | Blobs that passed all 5 filter layers and were chunk-published. |
| `harporis_getter_blobs_skipped_total`               | Counter   | `scan_id`, `reason`          | Skipped blobs by reason (`path_excluded`, `binary_extension`, `size_cap`, `gitattributes_binary`, `nul_byte`). |
| `harporis_getter_chunks_published_total`            | Counter   | `scan_id`, `kind`            | Chunks emitted by kind (`BLOB`, `DIFF_WINDOW`). |
| `harporis_getter_bytes_published_total`             | Counter   | `scan_id`                    | Total content bytes (excluding proto framing). |
| `harporis_getter_errors_total`                      | Counter   | `scan_id`, `type`            | Recoverable errors per scan. |
| `harporis_getter_status_publish_errors_total`       | Counter   | `scan_id`                    | Failures publishing `StatusEvent`. |
| `harporis_getter_scan_duration_seconds`             | Histogram | `scan_id`, `status`          | End-to-end scan wall-clock. |
| `harporis_getter_active_scans`                      | Gauge     | —                            | Currently running scans on this instance. |

### Logs

Structured JSON via `log/slog`. Key events:

- `getter ready` (startup)
- `shutdown initiated` (SIGTERM/SIGINT)
- `scan request handler` (per-request error path)
- `status publish failed` (warn, with `scan_id` and `state`)
- `unmarshal ScanRequest` / `unmarshal cancel` (error path)

Log level is set by `service.log_level`.

### gRPC

- `Health(HealthRequest) → HealthResponse{status: "SERVING"}` — minimal liveness probe.
- `StartScanLocal(ScanRequest) → StartScanLocalResponse` — local-dev backdoor for skipping NATS. Disabled in prod (`grpc.allow_local_start: false`).

---

## Operational notes

### Scaling

- Horizontal: add more getter instances. They join `getter-pool` automatically and start consuming requests.
- Vertical: bump `resources.max_cpu_cores` and `chunking.row_size_target_kb`. The runner uses a worker pool of `max_cpu_cores` goroutines, each holding its own cat-file subprocess.

### Cancellation

Publishing a `CancelScanRequest{scan_id, reason}` to `harporis.scans.cancel` cancels the scan wherever it's running. Every getter receives the broadcast; only the holder reacts (others ignore). The runner's context is cancelled, the walker exits on `ctx.Done()`, workers see the closed `jobs` channel and exit, cat-file subprocesses are killed via the cancelled exec context.

### Failure modes

- **NATS down at startup:** `wire.Dial` returns error; getter exits with `FATAL: nats dial`.
- **NATS drops mid-scan:** publisher retries with exponential backoff; if all retries fail, the chunk-emit returns error and the scan counter increments. Status events that fail are logged + metered but don't fail the scan.
- **git binary missing or unspawnable cat-file:** worker watchdog cancels the walker after a brief grace period, scan returns `FAILED`.
- **OOM:** `GOMEMLIMIT` triggers aggressive GC. Hard OOM is possible only if a single line/blob exceeds the in-memory cap (configurable via `chunking.max_file_size_mb`).
- **Disk full during clone:** clone returns error, scan returns `FAILED`. Subsequent scans on this instance are unaffected.

### Graceful shutdown

SIGTERM / SIGINT cancels the root context. In-flight scans see the cancel and produce `CANCELLED` status events (best-effort). NATS subscriptions are unsubscribed, metrics HTTP server is shut down with a 30s timeout, then the process exits.

---

## Known limitations

| # | Topic | Status |
|---|-------|--------|
| 1 | No built-in CLI to encode/submit `ScanRequest` — use `nats pub` with a hand-rolled proto encoder for now. | Will add `cmd/getter-cli` in v2. |
| 2 | `DIFF_WINDOW` chunks scan only added + context lines. Deleted lines are intentionally discarded (per [`internal/git/diff.go` ParseUnifiedDiff doc-comment](internal/git/diff.go)). | Intentional. |
| 3 | `FULL_HISTORY` `BlobJob.Refs` carries only first-seen commit, not all commits where the blob appears. Multi-commit reporting needs a second pass. | Spec'd for v2. |
| 4 | `git.cat_file_batch_buffer_kb` config field is reserved but not currently consumed. | Will wire when we add a custom-buffered stdout reader for cat-file. |
| 5 | Resume-after-crash is not implemented. Restarting a getter mid-scan loses progress; client must re-submit with a new `scan_id`. | Spec'd as deferred (checkpoint-based resume). |
| 6 | No `PARTIAL` state ever emitted by the runner — only `COMPLETED` or `FAILED`. | Acceptable for MVP; spec lists `PARTIAL` as optional. |

---

## Codebase layout

```
services/getter/
├── cmd/getter/main.go              # Entrypoint: config, signals, wiring
├── config/getter.yaml              # Sample config
├── Makefile                        # build / test / lint / run
├── README.md                       # This file
├── go.mod
└── internal/
    ├── config/                     # Config struct + YAML loader + validator
    ├── filter/                     # 5-layer file filter (paths / exts / size / .gitattributes / NUL)
    ├── chunk/                      # Line scanner + chunk builder (byte-budget + overlap)
    ├── git/                        # CLI wrappers: clone, cat-file, ls-tree, rev-list, walker, diff
    ├── scan/                       # State machine + active-scans registry + runner orchestrator
    ├── nats/                       # Typed publisher + request queue + cancel broadcast
    │                               #   (uses contracts/wire for connect/streams/subjects)
    ├── grpc/                       # Health + gated StartScanLocal
    ├── resource/                   # GOMAXPROCS / GOMEMLIMIT + sync.Pool
    ├── metrics/                    # Prometheus registry + /metrics handler
    └── testutil/                   # Temp git repo + embedded NATS helpers (test-only)
```

Shared with other services (validator, writer):

```
contracts/
├── proto/harporis/v1/              # Proto definitions
│   ├── types.proto                 # GitRow, GitRowChunk, ChunkKind, CommitFileRef
│   ├── scan.proto                  # ScanRequest, ScanType, Source, OutputConfig
│   ├── events.proto                # StatusEvent, CancelScanRequest, ScanState
│   └── service.proto               # GetterService (Health + StartScanLocal)
├── gen/go/harporis/v1/             # Generated Go bindings (committed)
└── wire/                           # NATS connect + streams + subject builders + queue group constants
                                    #   Imported by all three services
```

---

## See also

- Design spec: [`docs/superpowers/specs/2026-05-23-getter-design.md`](../../docs/superpowers/specs/2026-05-23-getter-design.md)
- Implementation plan: [`docs/superpowers/plans/2026-05-23-getter.md`](../../docs/superpowers/plans/2026-05-23-getter.md)
