# services/writer

Stateless, horizontally scalable consumer of `HARPORIS_FINDINGS` that
materializes detected secrets to durable sinks. Ships five sinks
out of the box — **NDJSON**, **SARIF**, **HTML**, **XLSX**, **PDF** —
all enabled by default and selectable per-scan from the CLI.

```
┌─────────┐     ┌─────────────┐     ┌────────┐     ┌──────────────────────┐     ┌──────────┐
│ scanner │ ──▶ │    NATS     │ ──▶ │ writer │ ──▶ │ <scan_id>.{ndjson,   │ ──▶ │ harporis │
│ (N rep) │ ◀── │ (JetStream) │ ◀── │ (Nrep) │     │  sarif, html,        │     │ findings │
└─────────┘     └─────────────┘     └────────┘     │  xlsx, pdf}          │     └──────────┘
                                                   │  (host bind-mount)   │
                                                   └──────────────────────┘
```

## What it does

- Pull-consumes `harporis.findings.>` via durable `writer-pool`
  (`wire.WriterDurableConsumer`) on the `HARPORIS_FINDINGS`
  `WorkQueuePolicy` stream.
- For each `v1.Finding`, fans out to the enabled sinks (filtered
  per-finding by `finding.output_formats` if the operator passed
  `harporis scan -f <list>`; empty = every enabled sink fires).
- Files land under `/var/lib/harporis/findings/` inside the container
  and `${HARPORIS_FINDINGS_DIR:-./findings}` on the host (bind-mount).
  Writer runs as host `${UID}:${GID}` so the files are operator-owned.
- Sink semantics differ by format:
  - **NDJSON** — append-per-write, one open `*os.File` per scan_id with
    O_APPEND for kernel-side line atomicity up to PIPE_BUF.
  - **SARIF / HTML / XLSX / PDF** — in-memory accumulator per scan_id,
    rewritten atomically (tempfile + `os.Rename`) on every Write.
    Capped per scan_id to bound memory.

## Run locally

```bash
# Brings up nats + getter + scanner + writer
make stack-up

# Submit a scan (all enabled sinks fire by default)
harporis scan --local /repos/leaky --scan-id smoke-1

# Or restrict to specific formats for this scan
harporis scan --local /repos/leaky --scan-id smoke-2 -f pdf,html

# Inspect findings
harporis findings list                          # which scans have any file
harporis findings show smoke-1                  # NDJSON, one per line
harporis findings show smoke-1 -f pretty        # human table
harporis findings show smoke-1 -f pdf > r.pdf   # binary stream via `docker exec writer cat`
```

`harporis findings show -f` accepts `ndjson`, `pretty`, `sarif`, `json`,
`csv`, `md`, `html`, `xlsx`, `pdf`. Binary formats (xlsx/pdf) round-trip
through `docker exec` without corruption — `exec.Command` does not
allocate a TTY, so no LF translation.

## Configuration

`config/writer.yaml`:

| Field                  | Default                          | Notes |
|---|---|---|
| `nats_url`             | `nats://nats:4222`               | Compose-internal DNS. |
| `nats_token`           | empty                            | Required by NATS auth; compose sets `harporis-dev` for dev. |
| `workers`              | `runtime.NumCPU()`               | Worker goroutines. |
| `fetch_batch`          | `16`                             | JS Fetch batch size. |
| `fetch_max_wait_ms`    | `5000`                           | JS Fetch MaxWait. |
| `ack_wait_seconds`     | `30`                             | JS consumer AckWait. |
| `max_deliver`          | `5`                              | Drop-and-log after this many tries. |
| `max_ack_pending`      | `64`                             | Bounded in-flight per durable. |
| `output_dir`           | `/var/lib/harporis/findings`     | Sink output root (bind-mounted from host). |
| `ndjson_enabled`       | `true`                           | Enable NDJSON sink. |
| `sarif_enabled`        | `true`                           | Enable SARIF v2.1.0 sink. |
| `html_enabled`         | `true`                           | Enable HTML sink. |
| `xlsx_enabled`         | `true`                           | Enable XLSX sink. |
| `pdf_enabled`          | `true`                           | Enable PDF sink. |
| `metrics_addr`         | `:9102`                          | `/metrics`, `/healthz`, `/readyz`. |
| `log_level`            | `info`                           | `debug|info|warn|error`. |

All fields honour `${VAR:-default}` env substitution at load time.

Per-scan `-f` filtering is best-effort: if the operator requests
`-f pdf` but `pdf_enabled: false`, the request is silently dropped
(the sink isn't in the writer's list to filter from).

## Metrics

Available at `:9102/metrics`. Hit them from the host via
`harporis metrics --service writer` (works from any CWD via direct
`docker exec`).

```
writer_findings_consumed_total
writer_findings_write_seconds{sink}
writer_sink_writes_total{sink,severity}
writer_sink_errors_total{sink,reason}
writer_nats_delivery_errors_total{kind}
writer_active_scans
writer_build_info{version,commit,proto_version}
```

`{sink}` is one of `ndjson_file`, `sarif`, `html`, `xlsx`, `pdf`.

## Horizontal scaling

```bash
docker compose up -d --scale writer=2
```

`WorkQueuePolicy` on `HARPORIS_FINDINGS` plus the shared durable
`writer-pool` consumer gives round-robin delivery across replicas
without queue-group plumbing.

Storage caveat: all replicas must mount the same `output_dir`
read-write. With the host bind-mount (default), this works locally
because all replicas share the host path. On Kubernetes, choose a
`ReadWriteMany` storage class for the PVC if `replicas > 1`.

The accumulator sinks (SARIF/HTML/XLSX/PDF) write the full file from
in-memory state on every Write, so concurrent replicas on the same
scan_id race; the last-writer-wins via atomic rename. NDJSON is the
only sink that aggregates safely under multi-replica fan-out for the
same scan_id (O_APPEND linearizes).

## Files

| File                                       | Purpose |
|---|---|
| `cmd/writer/main.go`                       | Wires config, NATS, sinks (NDJSON+SARIF+HTML+XLSX+PDF), consumer, metrics, signal handling. |
| `internal/config/load.go`                  | YAML + env + defaults; per-sink enable flags. |
| `internal/nats/consumer.go`                | Pull consumer (delegates to `kit/nats/pullconsumer`). |
| `internal/sink/sink.go`                    | `Sink` interface + NDJSON file-per-scan implementation. |
| `internal/sink/sarif.go`                   | SARIF v2.1.0 accumulator, 10k findings/scan cap. |
| `internal/sink/html.go`                    | Self-contained HTML with inline CSS+JS (sort, filter, badges). |
| `internal/sink/xlsx.go`                    | XLSX via excelize; frozen header, per-severity row fill. |
| `internal/sink/pdf.go`                     | PDF via gopdf + `gofont.GoRegular/GoBold` (no system fonts). |
| `internal/sink/filter.go`                  | `WantedByFinding(s, formats)` — per-scan format filter. |
| `internal/metrics/metrics.go`              | Prometheus collectors + HTTP serve. |
| `internal/health/health.go`                | `/healthz`, `/readyz`. |
| `Dockerfile`                               | Multi-stage alpine; runs as `harporis` (overridden to host UID in compose). |
| `deploy/k8s/*.yaml`                        | Deployment, Service, ServiceMonitor, NetworkPolicy, PVC. |
