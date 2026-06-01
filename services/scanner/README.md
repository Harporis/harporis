# harporis scanner

Secret-detection consumer for the Harporis pipeline. Reads `GitRowChunk` from
`HARPORIS_CHUNKS`, runs an embedded YAML rule pack (regex + Shannon entropy),
publishes `Finding` to `HARPORIS_FINDINGS` with JetStream-MsgId dedup.

Stateless and horizontally scalable: spawn N replicas, NATS round-robins
chunks across them via a shared durable consumer (`scanner-pool`).

See `docs/superpowers/specs/2026-06-01-scanner-design.md` for the design.

## Local dev

```bash
make build
./bin/scanner --config config/scanner.yaml
```

## Scaling locally

```bash
docker compose up -d --scale scanner=4
```

## Manual e2e check

After `harporis scan --local /repos/leaky` completes, view findings:

```bash
nats stream view HARPORIS_FINDINGS
```

(`harporis history show <id> --findings` is tracked as a follow-up CLI patch.)
