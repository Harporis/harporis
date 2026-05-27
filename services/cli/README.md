# harporis CLI

Host-side operator CLI for the Harporis stack. Submits scan requests to
NATS, watches status events in a live dashboard, manages the local
docker compose stack, and runs environment diagnostics.

CLI is a separate service: it speaks only the public NATS contract
(`kit/nats/wire`) and the proto types (`contracts/gen/go/harporis/v1`).
It never touches `services/getter` internals.

## Install

### From source (Make)

```bash
git clone https://github.com/Harporis/harporis.git
cd harporis/services/cli
make install                       # /usr/local/bin/harporis (sudo)
# or:
make install PREFIX=$HOME/.local   # ~/.local/bin/harporis (no sudo)
```

### Via `go install`

```bash
go install github.com/Harporis/harporis/services/cli/cmd/harporis@latest
```

### Debian / Ubuntu

```bash
cd services/cli && make deb
sudo dpkg -i dist/harporis_*.deb
```

The `.deb` ships the binary at `/usr/bin/harporis` plus bash/zsh/fish
completion scripts in the standard system paths.

## Quick start

```bash
harporis up                       # docker compose up + wait for health
harporis doctor                   # verify environment
harporis scan --local /repos/demo # submit a scan, live dashboard
harporis history                  # past scans (latest event per scan)
harporis down                     # stop the stack
```

## Commands

| Command       | What it does                                              |
|---------------|-----------------------------------------------------------|
| `scan`        | submit a `ScanRequest` to NATS; waits for terminal state  |
| `cancel <id>` | publish a `CancelScanRequest`                             |
| `watch <id>`  | bubble tea live dashboard (line-based on non-tty/--json)  |
| `up`          | docker compose up + wait for NATS + getter health         |
| `down`        | docker compose down                                       |
| `ps` / `logs` | passthrough to docker compose                             |
| `doctor`      | environment checks (docker, compose v2, NATS, getter)     |
| `health`      | quick liveness probe                                      |
| `metrics`     | fetch + regex-filter the getter Prometheus output         |
| `history`     | `history list` + `history show <id>`                      |
| `completion`  | generate shell completion script                          |
| `version`     | binary version / commit / proto contract                  |

## Global flags

| Flag         | Env         | Default                          |
|--------------|-------------|----------------------------------|
| `--nats`     | `NATS_URL`  | `nats://localhost:4222`          |
| `--no-color` | `NO_COLOR`  | auto (tty + termenv detection)   |
| `--json`     | —           | off                              |
| `--quiet,-q` | —           | off                              |
| `--config`   | —           | `~/.config/harporis/config.yaml` |

Config file fields: `nats_url`, `color`, `default_scan_type`. Missing
file is not an error.

## Exit codes

| Code | Meaning                                |
|------|----------------------------------------|
| 0    | success                                |
| 1    | user / flag error                      |
| 2    | NATS unreachable / doctor failures     |
| 3    | scan terminated in FAILED / CANCELLED  |
| 124  | --timeout reached                      |

## Tests

```bash
make test               # unit (race detector)
make test-integration   # spins embedded NATS, drives the built binary
```
