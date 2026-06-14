# Harporis Test Report — 2026-06-10

End-to-end functional + load test on the v0.4 stack post-merge to `main`.
Stack rebuilt from `feat/scanner` HEAD `d29b471` (bounded NATS streams)
running on Docker compose with NATS + getter + scanner ×4 + writer ×2.

## TL;DR

| Dimension | Result |
|---|---|
| Six sinks fire by default | ✅ NDJSON / SARIF / HTML / XLSX / PDF / Parquet (78 findings on fixtures) |
| Per-scan `-f` filter | ✅ `-f parquet,html` → only those 2 files materialize |
| `--context N` | ✅ N before/after harvested, chunk-edge clamp |
| `mask_secrets:true` | ✅ HTML masked (79× `***`), NDJSON/SARIF unchanged |
| Determinism | ✅ Two scans of same repo → identical semantic findings |
| Concurrency (4 scanner × 2 writer × 5 scans) | ✅ 5×79 unique findings, zero duplicates, semantic equality |
| Throughput (3 concurrent × 1250 files / 8 MB each) | ✅ ~86s end-to-end, ~280 KB/s aggregate |
| Rule coverage | ✅ All 28 rules fire at least once |
| NATS bounds applied | ✅ WorkQueue: 2 GiB MaxBytes DiscardOld; STATUS: 7d MaxAge + 512 MiB MaxBytes |
| WorkQueue drains | ✅ CHUNKS + FINDINGS streams at 0 messages post-load |

## Stack Configuration

- Stack: `docker compose up --build` from `feat/scanner` HEAD
- Replicas: `--scale scanner=4 --scale writer=2` for concurrency phases
- Rule pack: `services/scanner/rules/default.yaml` — 28 rules (14 original + 14 new modern keys)
- Fixtures: `~/scanner-fixtures/` — 13 files × ~38 planted secrets covering every rule family
- Stress repo: `~/big-repo/` — 1250 files / 8 MB, 50 `.env` files with planted secrets + 1200 random base64 noise files

## Rule Pack Updates

14 rules added on top of the v0.1 pack (which covered AWS, GCP, Stripe, Slack, GitHub PAT/OAuth, JWT, PEM, generic high-entropy):

| Rule | Severity | Pattern shape |
|---|---|---|
| openai-api-key | CRITICAL | `sk-...T3BlbkFJ...` |
| openai-project-key | CRITICAL | `sk-proj-{40+}` |
| anthropic-api-key | CRITICAL | `sk-ant-api03-{90-108}` |
| sendgrid-api-key | CRITICAL | `SG.{22}.{43-55}` |
| twilio-account-sid | HIGH | `AC<32-hex>` |
| twilio-api-key | HIGH | `SK<32-hex>` |
| npm-access-token | CRITICAL | `npm_<36-b62>` |
| pypi-upload-token | CRITICAL | `pypi-AgEIcHl...` |
| cloudflare-api-token | HIGH | `CF_API_TOKEN={40}` + entropy filter |
| discord-bot-token | CRITICAL | `[MN]<24>.<6>.<27+>` |
| heroku-api-key | HIGH | UUID v4 in `HEROKU_API_KEY=...` |
| square-access-token | CRITICAL | `EAAA<60>` |
| telegram-bot-token | HIGH | `<8-11digits>:<35-b64url>` |
| mailgun-api-key | HIGH | `key-<32-hex>` |

Total rules: **28**. All hot-reloaded into running scanner without restart.

## Smoke Test

### Default fan-out (all enabled sinks fire)

```
$ harporis scan --local ~/scanner-fixtures --scan-id smoke-default
COMPLETED | scanned=13 chunks=13 bytes=5627 errors=0
$ ls findings/smoke-default.*
smoke-default.html  smoke-default.ndjson  smoke-default.parquet
smoke-default.pdf   smoke-default.sarif   smoke-default.xlsx
$ wc -l findings/smoke-default.ndjson
78
```

### Rule coverage

```
$ jq -r .rule_id findings/smoke-default.ndjson | sort -u | wc -l
28
```

All 28 rules in the active pack produced at least one finding.

### Per-scan `-f` filter

```
$ harporis scan --local ~/scanner-fixtures --scan-id smoke-filter -f parquet,html
$ ls findings/smoke-filter.*
smoke-filter.html  smoke-filter.parquet
```

Only the requested formats materialized; NDJSON/SARIF/XLSX/PDF skipped.

### `--context 3`

```
$ harporis scan --local ~/scanner-fixtures --scan-id smoke-context --context 3
$ jq '... context_before/after presence ...'
{rule_id: "sendgrid-api-key", line_number: 7, ctx_before: 3, ctx_after: 0}
{rule_id: "generic-high-entropy-secret", line_number: 6, ctx_before: 3, ctx_after: 1}
```

`ctx_before` / `ctx_after` populated up to N=3, clamped at file edges
(line 1 → 0 before; last line → 0 after).

### `mask_secrets: true`

```
$ # writer.yaml: mask_secrets: true; rebuild + restart writer
$ harporis scan --local ~/scanner-fixtures --scan-id smoke-mask
$ grep -c '\*\*\*' findings/smoke-mask.html findings/smoke-default.html
findings/smoke-mask.html:79     (one *** per finding)
findings/smoke-default.html:0   (baseline)
$ grep -c 'AKIAIOSFODNN7EXAMPLE' findings/smoke-mask.html
0   (no raw AWS secret in masked HTML)
$ jq -r .matched_secret findings/smoke-mask.ndjson | base64 -d | head -1
AKIAIOSFODNN7EXAMPLE   (NDJSON keeps raw — automation feed)
```

Mask applies only to HTML + PDF (per design); NDJSON / SARIF / XLSX / Parquet keep raw secrets.

### `findings show -f`

All 9 read-side formats verified:

```
$ harporis findings show smoke-default -f pretty | head -2
SEVERITY  RULE                            PATH:LINE         SECRET
LOW       generic-high-entropy-secret     ci/deploy.yml:4   npm_abc...
$ harporis findings show smoke-default -f csv | head -1
severity,rule,path,line,secret
$ harporis findings show smoke-default -f md | head -2
| Severity | Rule | Path:Line | Secret |
|---|---|---|---|
```

Plus passthrough formats (sarif/html/xlsx/pdf/parquet) stream the writer's `<scan_id>.<ext>` raw.

## Determinism

```
$ harporis scan --local ~/scanner-fixtures --scan-id det-a
$ harporis scan --local ~/scanner-fixtures --scan-id det-b
$ wc -l findings/det-a.ndjson findings/det-b.ndjson
   79 findings/det-a.ndjson
   79 findings/det-b.ndjson
$ diff <(jq ... det-a) <(jq ... det-b)   # ignoring scan_id, finding_id, detected_at_ms
diff_exit=0   # ZERO semantic differences
```

Same input → same (rule_id, file_path, line_number, matched_secret) set.
UUIDs + timestamps differ as expected.

## Concurrency

```
$ docker compose up -d --scale scanner=4 --scale writer=2
$ for i in 1..5; do harporis scan ... --scan-id conc-$i --no-wait; done
$ for i in 1..5; do
    total=$(wc -l <findings/conc-$i.ndjson)
    uniq=$(jq -r .finding_id ... | sort -u | wc -l)
    echo "conc-$i: total=$total unique=$uniq"
done
conc-1: total=79 unique=79
conc-2: total=79 unique=79
conc-3: total=79 unique=79
conc-4: total=79 unique=79
conc-5: total=79 unique=79
$ diff <(jq ... conc-1 | sort -u) <(jq ... conc-5 | sort -u)
diff_exit=0
```

- No duplicate `finding_id` within any scan (no double-publish across scanner replicas)
- No missing findings across scans (no lost dispatch on WorkQueue redistribution)
- Identical semantic content across all 5 concurrent scans

## Speed / Load

Stress repo: 1250 files / 8 MB (200 base64-noise files × 6 dirs + 50 planted-secret `.env` files).

Single scan:

```
$ start=$(date +%s.%N); harporis scan --local ~/big-repo --scan-id load-2; ...
COMPLETED | scanned=1250 chunks=1250 bytes=8009482 errors=0
wall-clock: 0.19s   (CLI → terminal status event)
... ~5-10s additional for writer accumulator sinks to flush 6 outputs
68 findings (50 generic + 9 ghp + 9 AWS)
```

3 concurrent scans:

```
$ start; for i in 1..3; do harporis scan ... --scan-id stress-$i --no-wait & done; wait
$ # wait for all 18 sink files (6 per scan × 3 scans)
end-to-end (3 concurrent): 86s
total work: 24 MB / 3750 files
aggregate throughput: ~280 KB/s
findings: 3 × 68 = 204 (all semantically equal)
```

**Bottleneck**: writer accumulator sinks (SARIF/HTML/XLSX/PDF/Parquet)
rewrite the full per-scan file on every Finding write. With 68 findings/scan
× 5 accumulator sinks × atomic-tempfile-rename, that's 340 file rewrites
per scan. Linear in (findings × accumulator-sinks).

**Not a bottleneck**: NATS (workQueue drained to 0 between scans), scanner
detector (1250 chunks scanned in sub-second per replica).

## NATS Stream Health

Post-load stream report:

| Stream | Retention | MaxAge | MaxBytes | Discard | Messages (idle) |
|---|---|---|---|---|---|
| HARPORIS_REQUESTS | workqueue | — | 2 GiB | old | 0–3 (drains as getter acks) |
| HARPORIS_CHUNKS | workqueue | — | 2 GiB | old | 0 |
| HARPORIS_FINDINGS | workqueue | — | 2 GiB | old | 0 |
| HARPORIS_STATUS | limits | 7d | 512 MiB | old | 207 (= status events for ~12 scans this session) |

WorkQueue streams drain completely between scans (Ack deletes). STATUS
accumulates within bounds — will auto-trim at 7d MaxAge or 512 MiB.

The `d29b471` migration (added bounds on existing streams via
`streamConfigDrifted`) took effect on the rebuilt stack.

## Files

- `~/scanner-fixtures/` — 13 source files with planted secrets (kept; useful for next session)
- `~/big-repo/` — 1250-file stress repo (kept; reuse for repeat load tests)
- `services/scanner/rules/default.yaml` — 28 rules total (committed)
- `findings/` — all scan outputs (gitignored; cleanup with `rm -f findings/*` between sessions)
