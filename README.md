# Harporis

Git-aware secret hunter. A small set of services that consume git
repositories, normalize them into chunks, and (eventually) detect
secrets and other sensitive patterns at scale.

## Architecture

```
+-----------+        +-------------+        +------------+
| harporis  | -----> |    NATS     | -----> |   getter   |
|   (CLI,   | <----- | (JetStream) | <----- | (container)|
|   host)   |        +-------------+        +------------+
+-----------+
```

- `getter` (container) — consumes `ScanRequest` from NATS, emits
  chunk + status events. See `services/getter/`.
- `nats` (container) — JetStream message broker.
- `harporis` (host) — operator CLI. See `services/cli/`.

## Quick start

One-liner — installs Go (if needed), Docker (with confirmation, if
needed), builds harporis, configures shell completion, runs `doctor`:

```bash
bash scripts/install.sh
```

Then:

```bash
exec $SHELL                       # pick up updated PATH + completion
make stack-up                     # docker compose up -d (NATS + getter)
harporis doctor                   # 4/4 OK?
harporis scan --local /repos/demo # run a scan with live dashboard
```

Default install location is `~/.local/bin` (no sudo). For system-wide:
`PREFIX=/usr/local sudo -E bash scripts/install.sh`.

For a hands-on walkthrough see [`services/getter/QUICKSTART.md`](services/getter/QUICKSTART.md).
For CLI install options and the full command tour see [`services/cli/README.md`](services/cli/README.md).

## Repo layout

| Path              | What                                                |
|-------------------|-----------------------------------------------------|
| `services/getter` | Git → NATS pipeline (server-side, containerized)    |
| `services/cli`    | `harporis` operator CLI (host-side)                 |
| `services/scanner`| (planned) secret detection consumer                 |
| `kit/`            | Cross-service Go primitives (`kit/nats/wire`)       |
| `contracts/`      | Proto definitions and generated Go                  |

## License

Apache-2.0 — see [`LICENSE`](LICENSE).
