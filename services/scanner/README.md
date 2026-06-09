# harporis scanner

Secret-detection consumer for the Harporis pipeline.

- **Consumes:** `harporis.chunks.<scan_id>` from `HARPORIS_CHUNKS` (WorkQueuePolicy stream).
- **Produces:** `harporis.findings.<scan_id>` to `HARPORIS_FINDINGS`, with JetStream-MsgId dedup. Also publishes `StatusEvent.metrics.secrets_found` updates to `HARPORIS_STATUS` on a 5-second tick (per replica).
- **Stateless.** All state lives in JetStream. Spawn N replicas, NATS round-robins via the shared durable consumer `scanner-pool`.

Spec: [`docs/superpowers/specs/2026-06-01-scanner-design.md`](../../docs/superpowers/specs/2026-06-01-scanner-design.md).

## Build

    make build       # → bin/scanner
    make docker      # → harporis/scanner:dev image

## Run locally (against an existing stack)

    docker compose up -d nats         # if not already running
    ./bin/scanner --config config/scanner.yaml

Override the rule pack:

    ./bin/scanner --rules path/to/rules.yaml

Override worker count:

    SCANNER_WORKERS=8 ./bin/scanner --config config/scanner.yaml

## Run as part of the stack

    docker compose up -d              # nats + getter + scanner (1 replica each)
    docker compose up -d --scale scanner=4

## Verify

    curl http://harporis-scanner:9101/healthz     # compose-internal DNS
    curl http://harporis-scanner:9101/readyz
    curl http://harporis-scanner:9101/metrics

For host-side debugging, attach via:

    docker compose exec scanner wget -qO- http://localhost:9101/metrics | head

## Manual e2e against /tmp/leaky-repo

After `harporis scan --local /repos/leaky` completes, view findings:

    docker compose exec nats nats stream view HARPORIS_FINDINGS

(`harporis history show <id> --findings` is a follow-up CLI patch.)

## Horizontal scaling

Scanner is stateless. To run N replicas:

    docker compose up -d --scale scanner=N

In Kubernetes: see `deploy/k8s/`.

**v0.1 caveat:** `StatusEvent.secrets_found` is **per-replica**. With N > 1
replicas, multiple status events arrive per scan, each carrying that
replica's local count. Aggregation across replicas is the writer service's
job (separate phase). For correct aggregate counts today, run replicas: 1
or consume `HARPORIS_FINDINGS` directly.

## Configuration reference

| Key | Default | Env override | Notes |
|---|---|---|---|
| `nats_url` | `nats://nats:4222` | `NATS_URL` | |
| `workers` | `runtime.NumCPU()` | `SCANNER_WORKERS` | 0 = NumCPU |
| `fetch_batch` | 16 | `SCANNER_FETCH_BATCH` | JS Fetch batch size |
| `fetch_max_wait_ms` | 5000 | `SCANNER_FETCH_MAX_WAIT_MS` | |
| `ack_wait_seconds` | 30 | `SCANNER_ACK_WAIT_SECONDS` | InProgress heartbeat at ack_wait/3 |
| `max_deliver` | 5 | `SCANNER_MAX_DELIVER` | After this, chunk is Acked + dropped + logged |
| `max_ack_pending` | 64 | `SCANNER_MAX_ACK_PENDING` | Bounded in-flight per durable |
| `status_tick_ms` | 5000 | `SCANNER_STATUS_TICK_MS` | StatusEvent emission cadence |
| `publish_ack_wait_seconds` | 5 | `SCANNER_PUBLISH_ACK_WAIT_SECONDS` | Per-publish deadline |
| `metrics_addr` | `:9101` | `SCANNER_METRICS_ADDR` | /metrics + /healthz + /readyz |
| `log_level` | `info` | `LOG_LEVEL` | debug/info/warn/error |
| `rules_path` | `""` (embedded) | `SCANNER_RULES_PATH` | empty = embedded default.yaml |

## Rule pack format

See `internal/rules/default.yaml` for examples. Each rule must include
`positive` and `negative` examples; the build fails if a positive example
doesn't match its regex or a negative example does.

## Known limitations

- Per-replica `secrets_found` counter (see caveat above).
- No DLQ stream. Chunks exceeding `max_deliver` are logged + acked.
- No live secret verification (HEAD requests to providers). Out of scope
  for v0.1; tracked as `--verify` flag in v0.2.
- `harporis doctor` does not yet check the scanner. Follow-up CLI patch.
