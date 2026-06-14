# `harporis watch` — fleet view + source attribution

**Date:** 2026-06-14
**Status:** Approved (design)
**Roadmap phase:** P0.1 (see `2026-06-14-harporis-roadmap-design.md`)

A live, fleet-wide status dashboard plus per-event source attribution.
The single-scan `harporis watch <scan-id>` already exists; this adds the
no-argument fleet mode that shows every scan across all replicas in real
time, reading the shared NATS `STATUS` stream.

---

## 1. Why

The `STATUS` stream in NATS is the single source of truth for the whole
fleet, independent of replica count: getter and scanner replicas all
publish to one shared `HARPORIS_STATUS` stream. So one reader gives a
fleet view whether you run 1 replica in CI or 100 in Kubernetes — no
code change between scales. Today live following only exists for the one
scan you launched; `harporis history` gives a cross-scan *snapshot* but
not a live view.

## 2. Command surface

- `harporis watch` (no arg) → **new** fleet mode.
- `harporis watch <scan-id>` → unchanged single-scan mode.

One verb, two modes selected by argument presence. Behavior by
environment (mirrors the existing watch):

- **TTY and not `--json`** → live TUI table (new `FleetModel`).
- **non-TTY / piped** → plain status lines (`StreamStatusLinesAll`).
- **`--json`** → stream of protojson objects, one per event.

The fleet view has **no idle timeout** (unlike single-scan watch): it is a
dashboard and runs until `ctrl+c`.

## 3. Source attribution

- **proto:** add `string source = 7;` to `StatusEvent` (additive,
  backward compatible).
- **publishers:** getter and scanner set `source = "<service>-<hostname>"`
  (`os.Hostname()` read at startup, passed into the Publisher). Writer is
  **not** a status publisher — it only consumes `STATUS` for finalization
  — so only two publishers change.
- **Semantics — last-writer-wins.** A scan in `RUNNING` receives lifecycle
  events from one getter and `secrets_found` ticks from several scanner
  replicas. The `SOURCE` column therefore shows the most recent emitter
  ("who is actively working the scan"). The column may flicker between
  scanner replicas; aggregating a source list is deliberately out of
  scope (YAGNI).

## 4. Data flow (CLI) — seed-then-tail

Avoids replaying a week of history on every launch:

1. On start, call the existing `ListHistory()` once to seed the table with
   the current latest state of every scan (cost identical to
   `harporis history`).
2. Open a wildcard subscription to `harporis.status.>` with `DeliverNew`
   (new `SubscribeStatusAll()` in `natscli`, ephemeral +
   `InactiveThreshold` so the server reaps it if the CLI dies) and tail
   only new events.
3. Each event upserts into a `map[scanID]*StatusEvent`; the TUI redraws.

Steady-state work is bounded to new events. Reuses the proven
`ListHistory` + `FetchStatusEvents` machinery.

## 5. Components (new / touched)

| File | Change |
|---|---|
| `contracts/proto/harporis/v1/events.proto` | add `source` field; regenerate Go |
| `services/getter/internal/nats/publisher.go` (+ getter main) | set `source`; pass hostname in |
| `services/scanner/internal/nats/publisher.go` (+ scanner main) | set `source`; pass hostname in |
| `services/cli/internal/natscli/status_stream.go` | add `SubscribeStatusAll()` (wildcard, DeliverNew, InactiveThreshold) |
| `services/cli/internal/tui/fleet_model.go` | **NEW** `FleetModel` — table from the map, sorted (active first, then recent terminal), keys `q` / `↑↓` / `f` (filter by state) |
| `services/cli/internal/cmd/watch.go` | route no-arg → `RunFleetTUI` / `StreamStatusLinesAll`; `--json` branch |
| `services/cli/internal/ui/status.go` | `SOURCE` column in line output |

## 6. Error handling / edges

- Subscribe failure → print error, exit code 2.
- Empty stream → "no scans yet, waiting…", then populates live.
- Terminal scans stay in the table (marked ✔/✖), not removed; `f` can hide
  completed ones.
- `--json` is also fixed to emit real protojson in **both** fleet and
  single-scan modes (today `--json` only forces line output in single-scan
  mode rather than emitting JSON).

## 7. Testing

- `FleetModel.Update` — table-driven, no network: event folding into the
  map, sort order, terminal-state transitions.
- `natscli.SubscribeStatusAll` — against the existing embedded-NATS test
  pattern (confirm the integration harness during implementation).
- getter/scanner publishers — `source` is set and round-trips through
  protojson.

## 8. Approved decisions

- (a) no-arg `watch` is the fleet mode (not a separate `fleet`/`dashboard`
  command).
- (b) `SOURCE` = last emitter (last-writer-wins, may flicker).
- (c) seed-then-tail instead of full `DeliverAll`.
- (d) no idle timeout for fleet mode.
- (e) fix `--json` to emit real protojson in both modes.

## 9. Mockup (TUI mode)

```
 harporis watch — 4 scans live          nats://localhost:4222   14:23:07
 ─────────────────────────────────────────────────────────────────────
 SCAN_ID        STATE       SOURCE          CHUNKS   SECRETS   UPDATED
 repo-alpha     ● RUNNING   scanner-7f2a    1240     3         2s ago
 repo-beta      ● RUNNING   getter-3c11     310      0         1s ago
 mono-gamma     ✔ COMPLETED getter-1a8c     8900     17        12s ago
 legacy-delta   ✖ FAILED    scanner-9d04    42       0         40s ago
 ─────────────────────────────────────────────────────────────────────
 q quit   ↑↓ scroll   f filter state
```
