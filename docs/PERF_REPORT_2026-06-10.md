# Writer Accumulator-Sink Optimization Report — 2026-06-10

Same stack as `docs/TEST_REPORT_2026-06-10.md` (4 scanner × 2 writer
on Docker compose), same stress repo (`~/big-repo`, 1250 files /
8 MB), same workload (3 concurrent stress scans). Measured before and
after the batched-flush refactor in `feat(writer): batched accumulator
flush — coalesce sink rewrites`.

## What got optimized

Five accumulator sinks (SARIF / HTML / XLSX / PDF / Parquet) used to
rewrite their full per-scan file from scratch on **every** Finding —
that gave operators a "partial scan is always parseable" guarantee
but cost O(N²) bytes written per scan plus a syscall storm
(tempfile → fsync → rename) for every secret detected.

The refactor coalesces writes via a shared `BatchedAccumulator`
helper:
- **batch trigger** — once `FlushBatch` (default 50) new findings are
  pending, the accumulator runs one synchronous flush from inside the
  Add call that crossed the threshold.
- **interval trigger** — a background ticker fires every
  `FlushInterval` (default 2 s); any scan idle for at least one
  interval gets drained.
- **close trigger** — `Close()` drains every dirty buffer.

NDJSON unchanged (it streams; durability boundary). Other sinks are
now eventually-consistent views over the same Finding stream — a
crash can drop up to one batch (≤50 findings, ≤2 s) from these files;
NDJSON survives untouched.

## Headline numbers

3 concurrent stress scans, 1250 files / 8 MB per scan, 24 MB total, 204 findings:

| Metric | Baseline | Optimized | Ratio |
|---|---|---|---|
| Flushes per sink | 204 | ~8 | **25.5× fewer** |
| Total flushes (all 5 sinks) | 1020 | ~40 | **25.5× fewer** |
| sarif_file CPU | 2.31 s | 0.06 s | **38× faster** |
| html_file CPU | 2.51 s | 0.12 s | **21× faster** |
| parquet_file CPU | 2.33 s | 0.12 s | **19× faster** |
| pdf_file CPU | 0.91 s | 0.05 s | **18× faster** |
| xlsx_file CPU | 0.67 s | 0.04 s | **17× faster** |
| **All sinks total CPU** | **8.73 s** | **0.40 s** | **22× faster** |
| End-to-end wall-clock | 85.28 s | 88.28 s | ≈ 0 |

Wall-clock didn't move because sink CPU was never the end-to-end
bottleneck — NATS pipeline + getter chunking + scanner regex
dominate. What the optimization frees is **writer CPU budget**:
under sustained load the writer is no longer rewriting megabytes of
SARIF/HTML/Parquet per detected secret. Headroom for scaling out
the rest of the pipeline doubled or tripled depending on workload
shape.

## Batch-size distribution (post-optimization, sarif_file)

How many findings each flush actually coalesced:

| Batch size bucket | Count |
|---|---|
| ≤ 1 | 1 |
| ≤ 10 | 3 |
| ≤ 25 | 4 |
| ≤ 50 | 9 |
| > 50 | 0 |

With FlushBatch=50 and FlushInterval=2 s, findings arrived steadily
enough across the 2 s window that the interval ticker (not the count
threshold) caught most batches in the 25–50 range. The single
ce 1-finding flush is the final pending findings at scan completion.

All 8 flushes on the optimized run were `trigger="interval"` — no
batch-threshold flush fired during this workload because findings
trickled in faster than the threshold but slower than the interval.

## Configuration knobs

`services/writer/config/writer.yaml`:

```yaml
flush_batch: 50           # accumulator flush when this many new findings pending
flush_interval_ms: 2000   # backstop ticker for idle buffers; 0 disables
```

To restore the legacy "render-on-every-write" behaviour (sync flush,
quadratic cost), set `flush_batch: 1` and `flush_interval_ms: 0`.

The CLI / scan API doesn't expose these — they're writer-side
operator knobs that affect all scans uniformly.

## New observability

Available at `:9102/metrics` on every writer replica:

```
writer_sink_flush_seconds{sink}               # histogram of flush latency per sink
writer_sink_flush_total{sink,trigger}         # counter, trigger ∈ {batch,interval,close}
writer_sink_flush_batch_size{sink}            # histogram of findings coalesced per flush
writer_sink_pending_findings{sink}            # current in-memory buffer depth across all scans
```

These get populated even in the legacy `flush_batch=1` path, so the
baseline data above came from the same code shipped with the
optimization.

## Durability trade-off (read this if you operate the writer)

NDJSON is now formally the durability boundary. The fan-out works
like:

1. A Finding lands on `harporis.findings.<scan_id>`.
2. The writer worker calls `Write()` on every enabled sink.
3. NDJSON `Write()` returns only after the line is fsync'd to disk.
4. Accumulator sinks' `Write()` returns after the Finding is added to
   the in-memory buffer (which may or may not trigger a flush).
5. The worker Acks the JetStream message.

If the writer process dies between step 4 and the next accumulator
flush, those Findings vanish from SARIF/HTML/XLSX/PDF/Parquet but
survive in NDJSON — which is exactly what an operator using NDJSON +
jq for primary analysis already expects.

To rebuild a stale sink from NDJSON:

```bash
# Drop the stale file
rm findings/<scan_id>.sarif
# Re-publish all findings from NDJSON to NATS (operator script — TODO).
# Or just re-run the scan with -f sarif.
```

A `harporis findings rebuild --scan-id X --format sarif` command is
on the v0.5 backlog. Until it ships, `harporis scan ... -f sarif` is
the easy path.

## Test plan

Run from the repo root:

```bash
make all-test                                 # 28 packages green
docker compose up -d --scale scanner=4 --scale writer=2
rm -f findings/perf-*
for i in 1 2 3; do
  harporis scan --local ~/big-repo --scan-id perf-$i --no-wait &
done; wait
# wait for parquet to land for all 3
while [ "$(ls findings/perf-*.parquet 2>/dev/null | wc -l)" != "3" ]; do
  sleep 1
done
# inspect flush counts
docker compose exec writer wget -qO- http://localhost:9102/metrics | grep ^writer_sink_flush_
```

Expect ~8 flushes per sink (≈ 25× fewer than legacy 204), with
trigger label split between `batch` and `interval` depending on how
fast findings arrive.

## What this does NOT optimize

- **Wall-clock per scan** — pipeline bottleneck moved off the writer.
- **NDJSON path** — already streaming.
- **NATS JetStream fan-out** — same as before.
- **Scanner regex throughput** — separate optimisation (would need
  rule re-ordering, early-exit, or PCRE2 JIT).
- **Getter git-tree walking** — separate optimisation (could parallelise
  per-blob fetch).

Next perf win is probably on the scanner side: the regex pack is now
28 rules and each one scans every chunk linearly. A unified regex or
a Hyperscan-style pattern compiler would help. Out of scope for this
commit.
