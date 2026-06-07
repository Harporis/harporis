# Harporis

Git-aware secret hunter. A horizontally scalable pipeline that ingests
git repositories, detects secrets with a hot-reloadable regex +
Shannon-entropy rule pack, and materializes findings to durable
sinks — NDJSON, SARIF, HTML, XLSX, PDF.

## Architecture

```
┌───────────┐      ┌─────────────┐      ┌──────────┐      ┌──────────┐      ┌──────────┐
│ harporis  │ ───▶ │    NATS     │ ───▶ │  getter  │ ───▶ │ scanner  │ ───▶ │  writer  │
│   (CLI,   │ ◀─── │ (JetStream) │ ◀─── │ (N reps) │      │ (N reps) │      │ (N reps) │
│   host)   │      │   + auth    │      └──────────┘      └──────────┘      └──────────┘
└───────────┘      └─────────────┘                                                 │
                                                                                   ▼
                                                                  ┌────────────────────────────┐
                                                                  │ ./findings/  (host bind)   │
                                                                  │  <scan_id>.{ndjson,sarif,  │
                                                                  │     html, xlsx, pdf}       │
                                                                  └────────────────────────────┘
```

- `getter` — clones the repo, normalizes to chunks, publishes on NATS.
- `scanner` — pulls chunks, applies the rule pack (hot-reloaded from
  `services/scanner/rules/default.yaml`), publishes findings.
- `writer` — pulls findings, fans out to enabled sinks
  (NDJSON / SARIF / HTML / XLSX / PDF), one file per scan_id per format.
- `harporis` (CLI) — submits scans, watches status, reads findings.
  Works from any CWD; auto-discovers `NATS_TOKEN` on localhost.

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

# only emit PDF + HTML for this scan (default: every enabled sink fires):
harporis scan --local ~/code/my-project -f pdf,html

# read the findings:
harporis findings list
harporis findings show <scan_id>              # NDJSON, one finding per line
harporis findings show <scan_id> -f pretty    # human-friendly table
harporis findings show <scan_id> -f pdf > report.pdf

# tear down:
make stack-down
```

Re-run the installer any time — every step is idempotent.

## Stack defaults

| Knob | Default | What it does |
|---|---|---|
| `HARPORIS_FINDINGS_DIR` | `./findings` (next to `docker-compose.yml`) | Host directory for materialized reports. Writer runs as host `${UID}:${GID}` so files are operator-owned. |
| `services/scanner/rules/default.yaml` | bind-mounted RO into scanner | Edit on the host; scanner re-parses + atomic-swaps within 5s. Invalid YAML preserves the previous pack (logged at Warn). |
| `NATS_TOKEN` | `harporis-dev` (compose substitution) | Required by NATS auth. CLI auto-discovers from `docker inspect harporis-nats` on localhost URLs; production must set explicitly. |
| `harporis scan -f <list>` | empty = every enabled sink fires | Per-scan format selection. Accepted: `ndjson`, `sarif`, `html`, `xlsx`, `pdf`. |
| `harporis findings show -f <fmt>` | `ndjson` | Read-side formats: `ndjson`, `pretty`, `sarif`, `json`, `csv`, `md`, `html`, `xlsx`, `pdf`. |

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
| `services/scanner`| Chunk consumer + secret detection + rule hot-reload |
| `services/writer` | Findings consumer → multi-format sinks              |
| `kit/`            | Cross-service Go primitives (`wire`, `scan`, `health`, `config`, `metrics`, `nats/pullconsumer`) |
| `contracts/`      | Proto definitions and generated Go                  |

## License

Apache-2.0 — see [`LICENSE`](LICENSE).
