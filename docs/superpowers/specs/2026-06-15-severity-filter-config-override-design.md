# Severity filter + config-override scaffold — design

**Date**: 2026-06-15
**Status**: Design approved, pending spec review → writing-plans

## Goal

Establish a reusable **"config default → CLI override"** scaffold for output-shaping
parameters, and land **one** parameter end-to-end as the reference instance: a
**severity filter** that controls which findings reach reports.

Scope is deliberately narrow: the scaffold (a precedence convention + a recipe)
plus the severity filter only. The other output-shaping parameters from the
handoff (context-lines default, matched-line display, field selection) are NOT
built here — they plug into the scaffold later via the documented recipe.

## Background (verified against code, 2026-06-15)

- **Per-scan output channel already exists**: CLI `scan` → `ScanRequest.output`
  (`OutputConfig`: `formats`, `context_lines`) → getter stamps each
  `GitRowChunk` (`OutputContextLines`, `OutputFormats`) → scanner copies onto
  each `Finding` (`output_formats`, field 40) → writer applies
  `sink.WantedByFinding(f.OutputFormats)` in `writer/cmd/writer/main.go`.
- **`ConfigOverride` (scan.proto field 5) and `allow_request_overrides`
  (getter.yaml) are a dead skeleton** — declared but applied nowhere in code.
  Not reused here.
- **Severity is an ordered enum** (`findings.proto`): `LOW=1, MEDIUM=2, HIGH=3,
  CRITICAL=4`. Assigned by the detection rule at scan time.
- **NDJSON is the authoritative source**; all other sink formats are projections
  of it. `writer/cmd/rebuild/main.go` (`writer-rebuild`) already replays
  `<scan>.ndjson` through sink machinery to regenerate a format. `findings show`
  streams the writer's pre-written `<scan>.<ext>` for `sarif/html/xlsx/pdf`, and
  renders from ndjson for `ndjson/pretty/json/csv/md`.

## Decisions

| # | Decision | Rationale |
|---|----------|-----------|
| D1 | Filter semantics = **set of levels** (`[CRITICAL, HIGH]`), not a `>=` threshold | Operator can pick any arbitrary set; more flexible than a single floor |
| D2 | Severity filter lives at **writer-time (config default) + read-time (CLI)** — NOT per-scan | Simpler: no proto/getter/scanner plumbing; matches handoff's lean |
| D3 | Carrier mechanism = **typed fields** where plumbing is needed (none needed for severity) | Type-safety for a security tool over map flexibility |
| D4 | Read-time `--severity` must work for **all formats** via NDJSON regeneration | NDJSON is source of truth; reuse the `writer-rebuild` path |
| D5 | Scaffold is a **precedence convention + recipe**, not a generic `Resolver[T]` engine | YAGNI; the rule is a one-liner |

## Scaffold (the reusable contract)

The heart of the "scaffold" is a single value-resolution rule that every future
output-shaping parameter follows:

```
effective = perScanOverride (if set) else configDefault else hardcodedDefault
```

**"unset" convention**: each parameter reserves a sentinel meaning "not set →
fall through to the next level down." For an empty severity set, `[]` (empty
list) = "no filter, all levels pass" (back-compat).

The scaffold is intentionally lightweight: a documented precedence rule + naming
conventions + the recipe in the final section — not a new framework. No generic
resolver machinery is built until a second parameter proves it's needed.

## Component design — severity filter

### 1. Writer-time filter (config default, applies to all 7 sinks)

The filter sits **before** the sinks in `writer/cmd/writer/main.go`, alongside
the existing `sink.WantedByFinding` format gate. Because it gates findings
before any sink sees them, it uniformly affects all formats (ndjson, sarif,
html, xlsx, pdf, parquet, sqlite) at first write.

- **Config** (`writer.yaml`): new key
  ```yaml
  severities: []   # empty = all levels (back-compat). Example: [CRITICAL, HIGH]
  ```
  Parsed into a `map[Severity]bool` (or set) at load. Unknown level name →
  config validation error in `writer/internal/config/validate.go` (fail-fast,
  matching existing validation style).
- **Filter**: a finding reaches the sinks only if its `severity` is in the set.
  Empty set → all pass.
- **Metrics**: add a counter for findings dropped by the severity filter,
  symmetric to the existing "unmatched format" counter in
  `writer/internal/metrics/metrics.go`.

### 2. Read-time CLI filter (`findings show --severity LEVELS`)

New flag accepting a comma-separated set, e.g. `--severity CRITICAL,HIGH`. Uses
the shared severity-set parser. Two code paths by format type:

- **CLI-rendered formats** (`ndjson, pretty, json, csv, md`): CLI parses ndjson
  and filters locally. For `ndjson` with `--severity` set, switch from
  "stream raw file" to "parse + re-emit filtered".
- **Proxy/binary formats** (`sarif, html, xlsx, pdf, parquet`): regenerate from
  NDJSON via `writer-rebuild`:
  - `findings show --severity ... --format pdf` → `docker compose exec writer
    writer-rebuild --severity ... --output-dir <temp>` → CLI streams the temp
    file → cleans it up.
  - The canonical `<scan>.<ext>` is **not** touched (regen writes to a temp
    path, not the live file).

### 3. `writer-rebuild --severity LEVELS`

Add a `--severity` flag to `writer/cmd/rebuild/main.go`. The filter is applied
inside `replay()`: a decoded `Finding` whose severity is not in the set is
skipped before `out.Write`. Empty/absent → no filter (current behaviour).

### 4. Shared severity-set parser

A single helper (in `kit/`) maps level names ↔ `Severity` enum:
- case-insensitive name parsing
- comma-separated set parsing
- unknown value → error listing valid levels

Used by both `writer.yaml` config loading and CLI `--severity` parsing, so the
string↔enum mapping lives in exactly one place.

## Data flow

```
writer-time (config default, all formats):
  writer.yaml: severities: [CRITICAL, HIGH]
    → writer gates findings before sinks
    → only CRITICAL/HIGH findings written to any of the 7 sinks

read-time (CLI override, all formats):
  findings show <scan> --severity CRITICAL
    text formats  → CLI filters ndjson locally
    binary formats→ writer-rebuild --severity CRITICAL → temp file → stream → cleanup
```

Precedence in practice: writer config `severities` = "what gets written to
reports at all"; read-time `--severity` = "what to surface now" (a subset of
what was written — read-time can only narrow, since it reads what the writer
already persisted).

## What is NOT touched

- `contracts/proto/**` (no new proto fields — severity is not per-scan)
- `services/getter/**`, `services/scanner/**`
- The dead `ConfigOverride` / `allow_request_overrides` skeleton

## Files touched

| File | Change |
|------|--------|
| `services/writer/config/writer.yaml` | new `severities: []` key |
| `services/writer/internal/config/*.go` | parse `severities`; validate level names |
| `services/writer/cmd/writer/main.go` | writer-time severity gate before sinks |
| `services/writer/internal/metrics/metrics.go` | dropped-by-severity counter |
| `services/writer/cmd/rebuild/main.go` | `--severity` flag, filter in `replay()` |
| `services/cli/internal/cmd/findings.go` | `show --severity`; proxy-format routing via rebuild |
| `kit/` (new pkg) | shared severity-set parser (name↔enum, set parsing) |

## Testing (TDD)

- **shared parser**: name↔enum table, case-insensitivity, comma-separated sets,
  unknown value → error.
- **writer config**: `severities` parses; unknown level → validation error;
  empty = all pass.
- **writer-time filter**: empty set passes all; set passes only listed; metric
  increments on drop.
- **writer-rebuild**: `--severity` filters in `replay()`; absent = no filter;
  output file contains only listed levels.
- **CLI `findings show --severity`**: text formats filter locally; binary
  formats route through rebuild and produce filtered output; invalid level →
  error; canonical sink file untouched after a filtered read.

## Recipe — adding the next output-shaping parameter

For future parameters (context-lines default, matched-line display, field
selection), reuse the scaffold:

1. **Choose the application point**: writer-config / per-scan / read-time (as
   done for severity — writer-config + read-time).
2. **If per-scan is needed**: add a typed field along
   `OutputConfig → GitRowChunk → Finding` (mirror `output_formats`).
3. **If a config default is needed**: add a key to the relevant `*.yaml` +
   validation in that service's `validate.go`.
4. **Add the CLI override flag** + reuse/extend the shared precedence/parse
   helper.
5. **Add a metric** if the parameter filters/drops data.

Note for context-lines: its existing `0 = no context` semantics collide with
"0 = unset"; the recipe (step 2) handles this by adding an explicit set-flag
when that parameter is built. Out of scope here.
