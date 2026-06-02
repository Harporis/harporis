# Harporis — Project Status

> Snapshot for handoff between development sessions.
> Last updated: 2026-05-30.

## TL;DR

The CLI is shipped and operational. The next module to build is `services/scanner` — the consumer of NATS chunks that actually detects secrets. Everything else listed below is polish, infrastructure, or follow-up work that does not block the next module.

## Architecture today

```
+-----------+        +-------------+        +------------+        +-----------+
| harporis  | -----> |    NATS     | -----> |  getter    | -----> |  scanner  |
|   (CLI,   | <----- | (JetStream) | <----- | (container)|        | (planned) |
|   host)   |        +-------------+        +------------+        +-----------+
+-----------+
```

| Component        | Status              | Where                                              |
|------------------|---------------------|----------------------------------------------------|
| `kit/nats/wire`  | done                | `kit/nats/wire/wire.go`                            |
| `contracts/*`    | done (proto v1)     | `contracts/proto/harporis/v1/*.proto`              |
| `services/getter`| done (MVP)          | `services/getter/`                                 |
| `services/cli`   | done (`cli/v0.1.0`) | `services/cli/`                                    |
| `services/scanner`| done (v0.1.0)      | `services/scanner/` (only go.mod + stub main.go)   |

## What ships today (CLI)

12 subcommands, all wired:

- `scan` / `cancel` / `watch` — scan lifecycle. `watch` has a Bubble Tea live dashboard on tty; falls back to line output on `--json` / non-tty.
- `up` / `down` / `ps` / `logs` — docker compose wrapper. `up` has a stepwise Bubble Tea checklist with real timings.
- `health` / `doctor` / `metrics` — diagnostics. `doctor` has a pluggable check framework (docker / compose v2 / NATS / getter).
- `history list` / `history show <id>` — walks the JetStream status stream.
- `version` / `completion` — meta.

**Quality of life:**
- ASCII HARPORIS banner with dynamic version / proto / NATS bar
- Lipgloss palette: red-team / blue-team / purple, auto-fallback to ASCII when `--no-color` or `NO_COLOR` is set
- Global flags `--nats` / `--no-color` / `--json` / `--quiet` / `--config`; optional `~/.config/harporis/config.yaml`
- Exit codes typed (0 ok / 1 user error / 2 NATS or doctor failure / 3 scan FAILED|CANCELLED / 124 idle timeout)
- Packaging: `make install` (PREFIX) / `make deb` / `make rpm` via nfpm, plus shell completions
- One-shot installer: `bash scripts/install.sh` (auto Go + Docker, builds, wires shell completion, runs doctor)

**Tag:** `cli/v0.1.0` on origin.

## What was deferred during cleanup

These are flagged with reasoning so the next session can pick them up without rediscovering the problem.

### Cross-module / contract changes (out of CLI scope)

- **`isTerminal(ScanState)` in `services/getter/internal/scan/state.go`** still duplicates the (now-canonical) `tui.IsTerminal`. CLI cannot import getter internals. Right move: extract to either `contracts/` (companion Go package) or `kit/` so all consumers agree on terminal-state classification. Would land alongside any next proto change.
- **`SanitizeConsumerName` is CLI-side only.** The validation gap is real: a scan-id containing `.` is accepted by the CLI but rejected server-side by getter's `ValidateScanID`. Solving this means moving the validator into `kit/` (or `contracts/`) and calling it from both CLI submit-path and getter ingest-path. Cross-cutting fix, deferred.

### Efficiency follow-ups

- **`ListHistory` walks the full status stream with no time bound.** Fine while the stream is small; add a `--since DURATION` flag and pass it as `OptStartTime` once the stream grows. Not blocking.
- **`harporis up` opens fresh NATS connection per 500 ms while waiting for reachability.** Works; cleaner pattern would be one connection with `nats.MaxReconnects(-1)` + `ConnectedCB`. Not blocking.
- **`EnsureStreams` runs on every `scan` and `watch`.** 4 JetStream API calls of overhead; safe + idempotent and cheap on localhost. Could become a `--skip-ensure` flag if remote latency ever matters.

### Workflow / process

- **No code review on tasks 2-16** (user opted out mid-implementation). The cleanup pass that produced this document is a partial substitute, but no third-party review has happened on the CLI.
- **No GitHub PR** for the CLI work. Pushed directly to `main` per user request. `gh` is not installed locally.
- **No release pipeline.** `make deb` works locally; no GH Actions / goreleaser job publishes binaries. Without that, `go install github.com/Harporis/.../harporis@latest` doesn't work for outsiders (private repo), and `apt install ./harporis_*.deb` requires a local build first.

### Quality / maintainability

- **`version.{Version, Commit, ProtoVersion}` are mutable globals.** Tests mutate them directly. If multiple `internal/cmd` tests ever run in parallel and both set version vars, they race. Low priority; would need a constructor-injection refactor if it surfaces.
- **`make lint` is only `go vet`.** Could add `golangci-lint` once it's available on the dev machines.
- **`UpModel` has no `ExitCode()`.** `WatchModel` does. If a CI step ever does `harporis up && ...`, a failed startup will report exit 0. Define `tui.ExitCoder` interface and implement on both models when this becomes a real consumer.

## Operational notes for the next session

1. **Local dev stack is already running** in containers (`docker compose ps` shows `harporis-nats`, `harporis-getter` healthy). Past scans (`leaky-1`, `demo-1`) visible in `harporis history`.
2. **`docker-compose.override.yml`** is gitignored. Per-developer repo mounts go there (the comment in `docker-compose.yml` shows the snippet). If you want to keep scanning leaky-repo:
   ```yaml
   services:
     getter:
       volumes:
         - /tmp/leaky-repo:/repos/leaky:ro
   ```
3. **Tag `cli/v0.1.0` is on origin.** Next CLI release would be `cli/v0.2.0` (or `v0.1.1` for a patch). `services/cli/Makefile` matches `cli/v*` for `git describe`.
4. **CLI binary lives at `services/cli/bin/harporis` (build) and `/usr/local/bin/harporis` (installed).** Tests assume the binary can be `go build`'d at integration time.

## Next module: `services/writer`

`HARPORIS_FINDINGS` is now populated by the scanner. The writer is the
next missing piece: consumer of findings → sink (file / SARIF / JSON / DB / UI).

The wire constant `WriterPoolQueueGroup = "writer-pool"` is already
reserved in `kit/nats/wire/wire.go`. Architecture mirrors the scanner:
durable pull consumer on `HARPORIS_FINDINGS` (also WorkQueuePolicy), N
replicas share `writer-pool`, output destinations selected by the
operator (start with: file-JSON, file-SARIF; later: SQLite, Postgres,
Grafana data source).

Also pending (cross-cutting, not blocking writer):

1. **`harporis history show <id> --findings`** — CLI subcommand to pull
   findings from `HARPORIS_FINDINGS` for a given scan_id. Small patch.
2. **`harporis doctor`** — add a scanner-replica health check (any
   replica responds via compose-internal DNS).
3. **Cross-replica `secrets_found` aggregation in StatusEvent.** Scanner
   currently emits per-replica counters; writer can aggregate by
   consuming the full findings stream.

## File map (where things live)

```
.
├── PROJECT_STATUS.md             ← this file
├── README.md                     ← user-facing entry point
├── Makefile                      ← proxy targets (cli/getter/stack)
├── docker-compose.yml            ← NATS + getter; per-user mounts go in override
├── docker-compose.override.yml   ← gitignored; per-user repo mounts
├── scripts/install.sh            ← one-shot installer (Go + Docker + harporis + completion)
│
├── contracts/                    ← proto definitions and generated Go (tagged v0.1.0)
├── kit/                          ← cross-service primitives, currently kit/nats/wire (tagged v0.1.0)
│
├── services/getter/              ← server-side git → NATS pipeline (containerized)
│   ├── QUICKSTART.md             ← hands-on getter walkthrough
│   ├── Dockerfile
│   └── …
│
├── services/cli/                 ← host-side operator CLI (tagged cli/v0.1.0)
│   ├── README.md                 ← CLI command tour + install paths
│   ├── Makefile                  ← build / install / deb / rpm / completions
│   ├── packaging/nfpm.yaml
│   ├── cmd/harporis/main.go
│   └── internal/
│       ├── cmd/                  ← cobra commands, exit codes, version
│       ├── ui/                   ← lipgloss palette, banner, icons, table, status formatter
│       ├── tui/                  ← bubble tea models (watch, up) + IsTerminal helper
│       ├── natscli/              ← Dial + EnsureStreams + SubscribeStatus + FetchStatusEvents
│       ├── compose/              ← docker compose Runner interface
│       ├── doctor/               ← Check interface + concrete checks
│       ├── config/               ← ~/.config/harporis/config.yaml loader
│       └── version/              ← ldflags-injected build identity
│
├── services/scanner/             ← STUB. Next module to build.
│
└── docs/superpowers/
    ├── specs/
    │   ├── 2026-05-23-getter-design.md
    │   └── 2026-05-25-harporis-cli-design.md
    └── plans/
        ├── 2026-05-23-getter.md
        └── 2026-05-25-harporis-cli.md
```

## Sanity check (run before starting next session)

```bash
make all-test                     # getter + cli unit tests
make -C services/cli test-integration  # cli vs embedded NATS
make stack-up                     # NATS + getter healthy
harporis doctor                   # all four checks OK?
```

If any of those fail, that's the first thing to fix.
