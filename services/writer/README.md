# services/writer

Stateless, horizontally scalable consumer of `HARPORIS_FINDINGS` that
materializes detected secrets to durable sinks. v0.1 ships one sink:
**NDJSON file-per-scan** under `/var/lib/harporis/findings/<scan_id>.ndjson`.

```
┌─────────┐     ┌─────────────┐     ┌────────┐     ┌─────────────┐     ┌──────────┐
│ scanner │ ──▶ │    NATS     │ ──▶ │ writer │ ──▶ │ NDJSON file │ ──▶ │ harporis │
│ (N rep) │ ◀── │ (JetStream) │ ◀── │ (Nrep) │     │   per scan  │     │ findings │
└─────────┘     └─────────────┘     └────────┘     └─────────────┘     └──────────┘
```

## What it does

- Pull-consumes `harporis.findings.>` via durable
  `writer-pool` (`wire.WriterDurableConsumer`) on the
  `HARPORIS_FINDINGS` `WorkQueuePolicy` stream.
- Serializes each `v1.Finding` to JSON (proto field names via
  `protojson.MarshalOptions{UseProtoNames: true}`) and appends one line
  to `/var/lib/harporis/findings/<scan_id>.ndjson`.
- Holds one open `*os.File` per scan_id with a per-file mutex;
  distinct scans write in parallel, same-scan writes are serialized.
- O_APPEND ensures kernel-side line atomicity up to PIPE_BUF (typically
  4 KiB) — same-scan tearing is extremely rare even across replicas if
  the underlying filesystem supports cross-process linearization on
  the same dirent.

## Run locally

```bash
# Brings up nats + getter + scanner + writer
make stack-up

# Submit a scan
harporis scan --local /repos/leaky --scan-id smoke-1

# Inspect findings
harporis findings list             # which scans have NDJSON files
harporis findings show smoke-1     # NDJSON output, one per line
```

The `harporis findings show` reads the file via `docker compose exec
writer cat`. Use `--output-dir <path>` if you've bind-mounted the
findings dir to a host path via `docker-compose.override.yml`.

## Configuration

`config/writer.yaml`:

| Field                  | Default                          | Notes |
|---|---|---|
| `nats_url`             | `nats://nats:4222`               | Compose-internal DNS. |
| `workers`              | `runtime.NumCPU()`               | Worker goroutines. |
| `fetch_batch`          | `16`                             | JS Fetch batch size. |
| `fetch_max_wait_ms`    | `5000`                           | JS Fetch MaxWait. |
| `ack_wait_seconds`     | `30`                             | JS consumer AckWait. |
| `max_deliver`          | `5`                              | Drop-and-log after this many tries. |
| `max_ack_pending`      | `64`                             | Bounded in-flight per durable. |
| `output_dir`           | `/var/lib/harporis/findings`     | NDJSON output root. |
| `metrics_addr`         | `:9102`                          | `/metrics`, `/healthz`, `/readyz`. |
| `log_level`            | `info`                           | `debug|info|warn|error`. |

All fields honour `${VAR:-default}` env substitution at load time.

## Metrics

Available at `:9102/metrics`. Hit them from the host via
`harporis metrics --service writer` (which uses `docker compose exec`).

```
writer_findings_consumed_total
writer_findings_write_seconds{sink}
writer_sink_writes_total{sink,severity}
writer_sink_errors_total{sink,reason}
writer_nats_delivery_errors_total{kind}
writer_active_scans
writer_build_info{version,commit,proto_version}
```

## Horizontal scaling

```bash
docker compose up -d --scale writer=2
```

`WorkQueuePolicy` on `HARPORIS_FINDINGS` plus the shared durable
`writer-pool` consumer gives round-robin delivery across replicas
without queue-group plumbing.

Storage caveat: with the default Docker named volume, scaling out only
works if all replicas can mount the same volume read-write (the local
driver allows this on one host). On Kubernetes, choose a
`ReadWriteMany` storage class for the PVC if `replicas > 1`.

## Deferred to v0.2

- SARIF output sink.
- SQLite / Postgres sinks for queryable history.
- File rotation policy (currently one file grows unbounded per scan).
- Per-finding TTL.

## Files

| File                                       | Purpose |
|---|---|
| `cmd/writer/main.go`                       | Wires config, NATS, sink, consumer, metrics, signal handling. |
| `internal/config/load.go`                  | YAML + env + defaults. |
| `internal/nats/consumer.go`                | Pull consumer with heartbeat + backoff + recover. |
| `internal/sink/sink.go`                    | NDJSON file-per-scan sink. |
| `internal/metrics/metrics.go`              | Prometheus collectors + HTTP serve. |
| `internal/health/health.go`                | `/healthz`, `/readyz`. |
| `Dockerfile`                               | Multi-stage alpine; runs as `harporis`. |
| `deploy/k8s/*.yaml`                        | Deployment, Service, ServiceMonitor, NetworkPolicy, PVC. |
