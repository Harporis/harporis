# harporis CLI — design

**Date:** 2026-05-25
**Branch:** `feat/harporis-cli`
**Status:** approved, ready for plan

## Goal

Replace ad-hoc `./services/getter/bin/getter-cli …` invocation with a
proper, globally-installable `harporis` command that exposes the full
Harporis stack (scan operations, stack lifecycle, diagnostics, history)
behind one beautiful, terminal-first interface.

## Non-goals

- Building a new server-side service. CLI publishes/subscribes to NATS;
  there is no new socket, no new daemon.
- Replacing the NATS wire contract. Any other client (CI scripts, future
  language SDKs) keeps working.
- Public package repository (apt repo with PGP signing, Homebrew tap,
  AUR, etc.). Local `.deb` install via `dpkg -i` is enough for now.

## Architecture

CLI lives on the **host**; the rest of the stack (getter, NATS, future
scanner) runs in containers. CLI is a separate Go module — it does not
import any `services/getter/internal/*` package and depends only on the
public NATS contract (`kit/nats/wire`) and proto types
(`contracts/gen/go/harporis/v1`).

```
services/cli/                                 NEW module
  go.mod                                      module github.com/Harporis/harporis/services/cli
                                              replace contracts => ../../contracts
                                              replace kit       => ../../kit
  Makefile                                    build / install / deb / test / lint
  README.md                                   install + command tour

  cmd/harporis/main.go                        entry; cobra.Execute()

  internal/
    cmd/                                      one file per subcommand
      root.go                                 root cmd + global flags
      scan.go cancel.go watch.go
      up.go down.go ps.go logs.go
      health.go history.go metrics.go doctor.go version.go
    ui/                                       isolated styling
      theme.go                                lipgloss styles (red/blue/purple palette)
      banner.go                               ASCII HARPORIS banner + tagline
      icons.go                                Unicode + ASCII-fallback icon set
      table.go                                row helpers used by history/ps/doctor
    tui/                                      bubble tea models (unit-testable)
      watch_model.go                          live scan dashboard
      up_model.go                             stack-startup checklist
    natscli/                                  thin wrapper around kit/nats/wire
      client.go                               Dial + EnsureStreams helpers
      history.go                              read past scans from status stream
    compose/                                  `docker compose` exec wrapper
      compose.go                              detects `docker compose` vs `docker-compose`
    doctor/                                   environment checks
      checks.go                               Check interface + concrete checks
    version/
      version.go                              ldflags-injected version/commit/protoVer

services/getter/cmd/getter-cli/               REMOVED in implementation phase
services/getter/Makefile                      `build-cli` target removed
services/getter/QUICKSTART.md                 all `getter-cli` refs → `harporis`
services/getter/README.md                     same
docker-compose.yml                            comment updated
README.md (root)                              install section + quickstart links
Makefile (root, NEW)                          proxy targets (cli/cli-install/cli-deb/stack-up/stack-down)
```

The CLI never reads getter internals. It speaks NATS just like any
operator would: `scan` → publish to `harporis.scans.requests`,
`cancel` → publish to `harporis.scans.cancel`, `watch`/`history` →
subscribe to `harporis.status.<scan_id>` on the status stream.

## Command tree

```
harporis                              banner + short help
harporis version                      version + git sha + proto version
harporis help [cmd]

# Stack lifecycle (docker compose wrapper)
harporis up      [--profile X] [--build]
harporis down    [-v]
harporis ps
harporis logs    [svc] [-f]

# Diagnostics
harporis doctor                       env checks (docker, compose v2, NATS, getter, proto, bind mounts)
harporis health                       NATS RTT + getter gRPC health + /metrics HTTP 200
harporis metrics [--watch] [--filter re]

# Scan operations
harporis scan    [flags]              wait by default; --no-wait to fire-and-forget
harporis cancel  <id> [--reason …]
harporis watch   <id> [--timeout dur]

# History
harporis history          [--limit N]
harporis history show <id>
```

### Global flags

| Flag         | Env         | Default                  | Purpose                                                |
|--------------|-------------|--------------------------|--------------------------------------------------------|
| `--nats`     | `NATS_URL`  | `nats://localhost:4222`  | NATS server URL                                        |
| `--no-color` | `NO_COLOR`  | auto (isatty + termenv)  | Disable ANSI styling                                   |
| `--json`     | —           | off                      | Machine-readable output on read commands               |
| `--quiet,-q` | —           | off                      | Suppress banner + secondary output                     |
| `--config`   | —           | `~/.config/harporis/config.yaml` | Optional config file (nats_url, color, defaults) |

### Scan flags

All flags from existing `getter-cli scan` carry over without rename:
`--type`, `--scan-id`, `--local`, `--remote-url`, `--remote-token`,
`--remote-ssh-key`, `--remote-known-hosts`, `--branch`, `--base-branch`,
`--from`, `--to`. Changes:

- `--wait` becomes the default. Use `--no-wait` to skip status subscription.
- Cobra parses everything; no more `shuffleFlagsFirst` hack.

### Exit codes

| Code | Meaning                                |
|------|----------------------------------------|
| 0    | success                                |
| 1    | user / flag error                      |
| 2    | NATS unreachable                       |
| 3    | scan terminated in FAILED / CANCELLED  |
| 124  | --timeout reached                      |

## Visual design

### Palette

| Role               | Color    | Hex      |
|--------------------|----------|----------|
| Red team / danger  | red      | `#FF3B3B` |
| Blue team / infra  | blue     | `#2D8CFF` |
| Brand / synthesis  | purple   | `#B14AED` |
| Success / healthy  | green    | `#3DD68C` |
| Warning / partial  | amber    | `#F2A93B` |
| Secondary text     | grey     | `#6E7681` |

`termenv.NewOutput(os.Stdout).Profile` selects true-color / ANSI256 /
ANSI16 / Ascii at runtime. `NO_COLOR` env or `!isatty(stdout)` forces
Ascii.

### Banner

Shown only at: `harporis` (no args), `harporis version`, `--help`,
`harporis up`, `harporis doctor`. Block-style "HARPORIS" wordmark in
purple with a bottom-bar showing live `version · proto vX · NATS URL`.
Never printed in `scan`/`cancel`/`watch`/`ps`/`history`/`metrics`/`logs`
to keep pipeable output clean.

### Icons (Unicode → ASCII fallback)

`✓ → [+]`, `✗ → [-]`, `⚡ → [*]`, `🛡 → [#]`, `▸ → ->`, `● → o`, `─ → -`.

### Bubble Tea — `harporis watch <id>`

Live dashboard with:

- header bar `scan <id>  STATE  elapsed`
- source / branch lines
- two progress bars (`walker`, `publish`)
- byte counter + error counter
- rolling event log (last N status events)
- footer with key hints (`ctrl+c`, `q`, `l`, `↑/↓`)

On terminal state the frame recolors (green / red / grey), final summary
is rendered inside a box, the program exits. If `!isatty` or `--json`,
the command falls back to the existing line-per-event format.

### Bubble Tea — `harporis up`

Stepped checklist with spinner:

```
✓ docker compose up           (1.2s)
✓ nats container started      (0.4s)
⚡ nats /healthz                ← spinner
○ getter container             (pending)
○ getter gRPC health           (pending)
```

Each step turns green/red on resolution; final message points the user
at `harporis scan --local /repos/demo`.

### Plain commands

`scan`, `cancel`, `ps`, `health`, `history`, `metrics`, `doctor`,
`logs` use lipgloss-styled tables and one-line status output. No
banners, no animation — they have to be pipeable.

## Install and packaging

Three install paths, documented in root `README.md`:

1. **`make install`** (primary) — `go build` + `sudo install -m 0755
   bin/harporis /usr/local/bin/harporis`. `make install PREFIX=$HOME/.local`
   installs to `~/.local/bin` without sudo.

2. **`go install`** — `go install
   github.com/Harporis/harporis/services/cli/cmd/harporis@latest`.
   Works once tags are published; until then, clone + `go install
   ./services/cli/cmd/harporis` works from repo root.

3. **`.deb`** — `make deb` produces
   `dist/harporis_<ver>_amd64.deb` via [nfpm](https://nfpm.goreleaser.com/).
   nfpm config lives in `services/cli/packaging/nfpm.yaml`. `make rpm`
   is a free spin-off of the same config.

Shell completions and man page ship via cobra's
`completion <bash|zsh|fish>` and the nfpm `contents:` block.

Version is injected via ldflags at build time:

```
-X 'main.version=<git describe>'
-X 'main.commit=<short sha>'
-X 'main.protoVersion=v1'
```

Without a tag, the version is `dev-<sha>`.

## Migration

- `services/getter/cmd/getter-cli/` is deleted whole. It was introduced
  in commit `f89c03b` on `fix/getter-review`; no external caller exists.
- `services/getter/Makefile` loses the `build-cli` target.
- `services/getter/QUICKSTART.md` replaces every
  `./services/getter/bin/getter-cli scan …` with `harporis scan …` and
  links to `services/cli/README.md` for install.
- `services/getter/README.md` updates the operator-tool reference.
- `docker-compose.yml` header comment updates.
- Root `README.md` gets an Install section + quickstart links.

The NATS wire contract is unchanged, so any non-CLI consumer keeps
working.

## Testing

### Unit (`services/cli/internal/...`)

- **`ui/`** — golden snapshots of styled strings with
  `lipgloss.SetColorProfile(termenv.Ascii)` so output is deterministic.
- **`tui/watch_model.go`** — drive the model via `Init/Update(msg)`
  without `tea.Program`; assert `View()` renders the expected sections
  given a sequence of fake `StatusEvent` messages.
- **`natscli/history.go`** — embed `nats-server` in the test (the
  pattern getter integration tests already use); assert past scans are
  read back correctly from the status stream.
- **`compose/compose.go`** — `exec.Command` is hidden behind a `Runner`
  interface; tests use a stub that records invocations.
- **`doctor/checks.go`** — each `Check` is independently tested with
  fake environment fixtures.

### Integration (`services/cli/integration_test.go`, build tag `integration`)

In-process `nats-server` plus invocation of the built `harporis` binary
via `exec.Command`. Asserts:

- `harporis scan` publishes the right subject with the right protobuf
- `harporis cancel` publishes a `CancelScanRequest`
- `harporis watch` consumes status events and prints the expected lines
- exit codes match the table above

### E2E (`make e2e`, local only)

`docker compose up`, real `harporis scan` against the mounted demo repo,
assert final status `COMPLETED`. Not part of CI.

### CI

- `make -C services/cli test` (unit)
- `make -C services/cli test-integration` (with `-tags integration`)
- `make -C services/cli lint` (`go vet`)

## YAGNI list (explicitly out of scope)

- Snap / Homebrew / AUR / Chocolatey packages
- In-CLI self-updater
- Signed `.deb` / public apt repository
- Authentication for NATS (current stack is unauthenticated; when auth
  is added at the stack level, CLI gains creds via env/config)
- New scan types beyond what `ScanType` proto already defines

## Open questions / follow-ups

- Should `harporis history` create a JetStream KV bucket of scan
  summaries (cheap, fast list)? Currently we read the status stream
  directly. Decide in implementation when we see the cost.
- `harporis logs` is a pure passthrough to `docker compose logs`; if we
  ever need styling there it becomes its own bubble tea view.
