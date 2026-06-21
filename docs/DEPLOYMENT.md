# Deploying Harporis

Three ways to run the stack, from simplest to hardened:

1. **[Quick start](#1-quick-start-published-images)** — one file, published images, default token.
2. **[Configure](#2-configure-via-env)** — token, version pinning, storage, scaling via `.env`.
3. **[Production hardening](#3-production-hardening-tls--jwt)** — NATS over TLS + JWT client auth.

The stack is four containers — `nats` (JetStream message bus) + `getter`,
`scanner`, `writer` — plus the `harporis` CLI you run on your machine.

---

## 1. Quick start (published images)

No source checkout, no build. Grab one file and bring it up:

```bash
curl -O https://raw.githubusercontent.com/Harporis/harporis/main/docker-compose.ghcr.yml
docker compose -f docker-compose.ghcr.yml up -d
```

This pulls `ghcr.io/harporis/harporis-{getter,scanner,writer}:latest`, starts
JetStream with a default dev token, and writes reports to `./findings`.

Install the CLI (host-side) to drive it:

```bash
go install github.com/Harporis/harporis/services/cli/cmd/harporis@latest
# then:
harporis health
harporis scan --remote-url https://github.com/octocat/Hello-World.git
harporis findings list
```

> The default token (`harporis-dev`) is fine for a private box. For anything
> exposed, set a real token — see below.

---

## 2. Configure via `.env`

Drop a `.env` file next to `docker-compose.ghcr.yml`. Every value is optional;
compose substitutes it automatically.

```ini
# Rotate the NATS auth token (any string; generate a strong one):
NATS_TOKEN=replace-with-openssl-rand-hex-24

# Pin a release instead of :latest (per-service tags from GHCR):
HARPORIS_VERSION=v0.5.0

# Where reports land on the host (absolute path recommended):
HARPORIS_FINDINGS_DIR=/srv/harporis/findings

# Opt into the shared SQLite sink (cross-scan SQL in findings/findings.db):
HARPORIS_SQLITE_ENABLED=true

# Findings severity filter is writer-side config; see services/writer.
LOG_LEVEL=info
```

Generate a token:

```bash
echo "NATS_TOKEN=$(openssl rand -hex 24)" >> .env
docker compose -f docker-compose.ghcr.yml up -d   # re-applies env
```

### Scaling (replication)

Every service is queue-based and scales horizontally:

```bash
docker compose -f docker-compose.ghcr.yml up -d --scale scanner=4 --scale getter=2
```

`getter` and `scanner` fair-share automatically (JetStream work-queue). The
`writer` needs deterministic per-scan routing when run with multiple replicas —
set `HARPORIS_FINDINGS_SHARDS=N` and use the sharded topology (see
`scripts/sharded-compose.sh` in the repo).

---

## 3. Production hardening (TLS + JWT)

For untrusted networks, layer the production overlay on top of the base stack.
It swaps the static token for **NATS over TLS** plus **JWT/nkey client auth**.

```bash
docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d --wait
```

It needs two credential bundles on the host, pointed at by env vars:

| Env var | Holds | Files |
|---|---|---|
| `HARPORIS_NATS_CERT_DIR` | TLS material (server-side) | `ca.pem`, `server-cert.pem`, `server-key.pem` |
| `HARPORIS_NATS_AUTH_DIR` | JWT client auth | `operator.jwt`, `client.creds`, `ca.pem` |
| `HARPORIS_NATS_RESOLVER` | JWT resolver config | MEMORY-resolver body or URL |

### 3a. TLS certificates — use the helper

The repo ships a generator for a self-signed CA + server cert (the TLS half):

```bash
scripts/gen-nats-certs.sh /etc/ssl/harporis-nats nats
export HARPORIS_NATS_CERT_DIR=/etc/ssl/harporis-nats
```

`nats` is the in-cluster DNS name clients connect to (the compose service
name). Clients trust the generated `ca.pem`. For a public deployment with a
real DNS name, either re-run with that CN or use certs from your own CA / Let's
Encrypt — drop them in the same three filenames.

### 3b. JWT/nkey client auth — `nsc`

NATS account auth uses NATS's `nsc` tool (operator → account → user). Install
it from https://github.com/nats-io/nsc, then:

```bash
nsc add operator harporis
nsc add account harporis
nsc add user --account harporis svc           # service identity
nsc generate creds -a harporis -n svc > client.creds   # -> HARPORIS_NATS_AUTH_DIR/client.creds
nsc describe operator --raw > operator.jwt             # -> HARPORIS_NATS_AUTH_DIR/operator.jwt
cp "$HARPORIS_NATS_CERT_DIR/ca.pem" "$HARPORIS_NATS_AUTH_DIR/ca.pem"
export HARPORIS_NATS_AUTH_DIR=/etc/harporis/nats-auth
export HARPORIS_NATS_RESOLVER="$(nsc describe operator --field nats.resolver 2>/dev/null || echo MEMORY)"
```

All three client services (`getter`/`scanner`/`writer`) load
`client.creds` + `ca.pem` from `/etc/harporis/nats-auth/` (mounted read-only)
and connect over `tls://nats:4222`. The dev token is wiped so a missing creds
file fails loudly instead of silently using the dev secret.

> Simpler middle ground: if you only need transport encryption (not per-client
> identity), TLS + a strong `NATS_TOKEN` is enough — skip 3b and keep the token
> from section 2. Full JWT (3b) adds revocable per-client identities.

---

## Scanning private repositories

Provide credentials per scan (see `harporis scan --help`) or set service-side
defaults so a given git host is authenticated automatically — see the remote
authentication design in `docs/superpowers/specs/2026-06-17-remote-auth-design.md`.

## Health & troubleshooting

```bash
harporis health                 # NATS RTT + service /metrics
harporis ps                     # container status
docker compose -f docker-compose.ghcr.yml logs getter --since 5m
```
