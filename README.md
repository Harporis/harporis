# Harporis

Git-aware secret hunter. A horizontally scalable pipeline that ingests
git repositories, detects secrets with a regex + Shannon-entropy rule
pack, and materializes findings to durable NDJSON sinks.

## Architecture

```
┌───────────┐      ┌─────────────┐      ┌──────────┐      ┌──────────┐      ┌──────────┐
│ harporis  │ ───▶ │    NATS     │ ───▶ │  getter  │ ───▶ │ scanner  │ ───▶ │  writer  │
│   (CLI,   │ ◀─── │ (JetStream) │ ◀─── │ (N reps) │      │ (N reps) │      │ (N reps) │
│   host)   │      └─────────────┘      └──────────┘      └──────────┘      └──────────┘
└───────────┘                                                                     │
                                                                                  ▼
                                                                  ┌────────────────────────────┐
                                                                  │  /var/lib/harporis/findings│
                                                                  │   <scan_id>.ndjson         │
                                                                  └────────────────────────────┘
```

- `getter` — clones the repo, normalizes to chunks, publishes on NATS.
- `scanner` — pulls chunks, applies the rule pack, publishes findings.
- `writer` — pulls findings, materializes one NDJSON file per scan_id.
- `harporis` (CLI) — submits scans, watches status, reads findings.

## Install in one command

```bash
bash scripts/install.sh
```

This installs Go (if missing), Docker + compose v2 (with confirmation),
builds the `harporis` CLI to `~/.local/bin`, wires shell completion,
**brings up the full stack** (`nats + getter + scanner + writer`), and
runs `harporis doctor`.

Flags:

- `--skip-stack` — install CLI + dependencies only, do not bring up the stack.
- `PREFIX=/usr/local sudo -E bash scripts/install.sh` — system-wide.

After it finishes:

```bash
exec $SHELL                              # pick up updated PATH + completion

# scan any repo on your host (auto-translated via getter's read-only $HOME mount):
harporis scan --local ~/code/my-project

# read the findings:
harporis findings list
harporis findings show <scan_id>         # NDJSON, one finding per line

# tear down:
make stack-down
```

Re-run the installer any time — every step is idempotent.

## Hands-on docs

- CLI tour + install options: [`services/cli/README.md`](services/cli/README.md)
- Getter operator guide: [`services/getter/QUICKSTART.md`](services/getter/QUICKSTART.md)
- Scanner details: [`services/scanner/README.md`](services/scanner/README.md)
- Writer details: [`services/writer/README.md`](services/writer/README.md)
- Project status + roadmap: [`PROJECT_STATUS.md`](PROJECT_STATUS.md)

## Repo layout

| Path              | What                                                |
|-------------------|-----------------------------------------------------|
| `services/cli`    | `harporis` operator CLI (host-side)                 |
| `services/getter` | Git → NATS pipeline (server-side, containerized)    |
| `services/scanner`| Chunk consumer + secret detection                   |
| `services/writer` | Findings consumer → NDJSON file-per-scan            |
| `kit/`            | Cross-service Go primitives (`kit/nats/wire`, `kit/scan`) |
| `contracts/`      | Proto definitions and generated Go                  |

## License

Apache-2.0 — see [`LICENSE`](LICENSE).
