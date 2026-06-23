# Interactive Watch — Design

**Date**: 2026-06-22
**Status**: Approved (brainstorming complete)
**Scope**: Phase 1 of the interactive `harporis watch` fleet dashboard — cursor
navigation, drill-in to per-scan status + event history, structured filtering,
and multi-column sorting. Findings (secrets) drill-in is explicitly a **later,
separate spec** (phase 2).

## Goal

Turn the read-only fleet table (`harporis watch` with no scan-id) into an
interactive master-detail dashboard. The operator can move a cursor over the
live scan table, drill into any scan to see its full metrics and time-ordered
event history, filter the table by structured `key:value` queries, and sort by
any column in either direction — all inside the existing single bubbletea
program and single NATS subscription.

## Non-Goals (phase 1)

- Viewing actual findings / secrets (requires the writer container; deferred to
  a phase-2 spec).
- Changing the single-scan live panel (`harporis watch <id>` → `WatchModel`).
  It stays exactly as-is.
- Any new NATS subscription beyond the existing wildcard status tail. The one
  added round-trip is a one-shot `ShowHistory` fetch on drill-in.

## Architecture

Everything lives in `services/cli/internal/tui/`. `FleetModel` graduates from
"table + fold" to a master-detail controller with a view mode. To keep each
unit focused (the current `fleet_model.go` would otherwise grow past a clean
single responsibility), the work is split into focused files:

```
fleet_model.go    master: state, Update (keys/events), View router (list|detail)
detail_model.go   detail: selected scanID, metrics block, scrollable history, render
filter.go         Filter{}: parse("state:failed source:gh") -> predicate; match(ev)
sort.go           sortColumn enum (ScanID|State|Source|Chunks|Secrets|Updated)
                  + reverse flag + comparators
```

- `cmd/watch.go` `RunFleetTUI`: minimal change — pass the `*natscli.Client` into
  the model (needed for the `ShowHistory` Cmd on `Enter`) and let the program
  handle the new `HistoryLoadedMsg`. Subscription stays single, as today.
- `WatchModel` (single-scan) is untouched.
- The existing `evictIfOver`, `sorted`, `agoString` helpers move/adapt;
  `sorted()` is now parameterized by active column + direction + filter.

## Master list (list mode)

New `FleetModel` state: `cursor int`, `sortCol sortColumn`, `sortRev bool`,
`filter Filter`, `filtering bool` (filter-input mode), `view viewMode`,
`filterInput string`, `filterErr string`.

Keys in list mode:

| Key | Action |
|---|---|
| `↑`/`k`, `↓`/`j` | move cursor over the visible (filtered+sorted) rows |
| `Enter` | drill-in: remember `scanID` under cursor, `view=detail`, emit `ShowHistory` Cmd |
| `s` / `S` | cycle sort column / reverse direction; `↑`/`↓` indicator on the column header |
| `/` | enter filter-input mode — `filter> ` line at the bottom; `Enter` apply, `Esc` cancel |
| `f` | quick toggle active-only (equivalent to `state:active`) — retained |
| `q`/`ctrl+c` | quit |

- The cursor clamps when the filter/sort changes the visible row count.
- Cursor row is highlighted via a lipgloss style.

### Sort semantics

- Default view keeps today's behavior: **active-first as the primary key**, then
  most-recent (`sortCol=Updated`, `sortRev=false`).
- Once the user explicitly picks a column via `s`, sorting is **purely by that
  column** (active-first is no longer forced) — predictable mental model.
- Tiebreak everywhere: `ScanId` ascending (deterministic, no flicker).

## Detail view (drill-in)

Data flow:

- On `Enter`, the model emits a `tea.Cmd` calling `cl.ShowHistory(scanID, ~1s)`
  → `HistoryLoadedMsg{scanID, events}`. Until it arrives, detail shows
  "loading history…"; metrics/state are already available from the latest
  `StatusEvent` in `m.scans[scanID]`.
- The live tail keeps running: an incoming `StatusEventMsg` for the selected
  scan, while in detail mode, updates the metrics and **appends** a row to the
  history (deduped by timestamp so the seed isn't doubled).

Render:

```
scan <id> ── <STATE> ── <source>
metrics  blobs N (skipped M) · chunks N · bytes N · errors N · secrets N · dur Xs
─ history ──────────────────────────
  15:04:01  PENDING    queued
  15:04:03  RUNNING    walking tree
  …                                  (scrollable: up/down·j/k, esc back)
```

- History scrolls in a viewport window sized to the terminal height.
- `Esc`/`q` → back to list, **cursor preserved**.
- A `ShowHistory` error renders as a line inside detail; it does not crash the TUI.

## Error handling & edge cases

- Empty fleet / empty filter result: `(no scans match)` plus a hint to clear the
  filter.
- Invalid filter (`foo:bar` with an unknown key): no crash — show
  `filter error: unknown key 'foo'` under the input line; the filter is not
  applied. Known keys: `state`, `source`, `id` (plus a bare word = substring
  across all fields).
- `Enter` on a scan already evicted by `evictIfOver` between frames: detail opens
  on the last-known snapshot; history may be empty — acceptable.
- Drill-in while a `SubscribeErrMsg` arrives: the tail error still quits the
  program (unchanged), with the existing exit behavior.

## Testing (TDD)

- `filter_test.go`: parse valid/invalid; match per key + bare word; combinations.
- `sort_test.go`: each column asc/desc; default active-first; tiebreak determinism.
- `fleet_model_test.go` (extended): cursor navigation with clamp; `Enter`→detail
  mode + history Cmd emitted; `s`/`S` mutate state; `/`-input applies filter;
  `f` == `state:active`.
- `detail_model_test.go`: `HistoryLoadedMsg` seeds history; a live event appends
  with dedup; `Esc` returns to list with preserved cursor.
- No e2e NATS in unit tests — models are tested against synthetic `StatusEvent`s,
  as `fleet_model_test.go` already does.

## Key decisions

| Decision | Rationale |
|---|---|
| Single bubbletea program, single subscription (Approach A) | Reuses live fleet data; no messy second program / second per-scan subscription |
| Reuse `ShowHistory` for the one-shot history seed | History already exists over NATS; no new infra |
| Active-first only in the default view, pure-column once `s` is pressed | Predictable sort; preserves current default UX |
| Structured `key:value` filter (not free-text fuzzy) | Operator chose precision over fuzzy matching |
| `s`/`S` cycle+reverse (not number keys) | Operator chose compact key set |
| Findings drill-in deferred to a separate phase-2 spec | Avoids pulling the writer container / NDJSON parsing into phase 1 |
| `WatchModel` untouched | Single-scan live panel is a separate, working concern |

## Data reference

`StatusEvent`: `ScanId`, `State`, `Timestamp`, `Message`, `Source`, `Metrics`.
`ScanMetrics`: `BlobsScanned`, `BlobsSkipped`, `ChunksPublished`,
`BytesPublished`, `ErrorsTotal`, `DurationMs`, `SecretsFound`.
History source: `(*natscli.Client).ShowHistory(scanID, wait)`.
