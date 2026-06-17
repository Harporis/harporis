# Remote repository authentication — design

**Date**: 2026-06-17
**Status**: Design approved, pending spec review → writing-plans
**Branch**: `feat/remote-auth`

## Goal

Let operators scan private repositories that need HTTPS credentials beyond
the current token/SSH support, with two complementary delivery paths:

1. **Per-scan auth** via CLI flags (one method per scan) — HTTPS Basic
   (login+password) and a flexible custom-header method (any header / any
   token scheme: `Authorization: Bearer <jwt>`, `Authorization: token <pat>`,
   GitLab `PRIVATE-TOKEN`, etc.).
2. **Config-default auth** in the getter service — credentials for one or
   more git hosts stored once in `getter.yaml`, auto-applied to any scan of a
   matching host that carries no per-scan auth.

Existing PAT-style `--remote-token` and SSH key/agent auth stay unchanged.

## Background (verified against code, 2026-06-17)

- **clone.go already implements HTTPS Basic.** `RemoteSource` has
  `BasicUser`/`BasicPassword`; `authMode()` returns `authHTTPSBasic`; the
  clone command injects the username into the URL and passes the password via
  `GIT_ASKPASS` (never in argv). Error redaction already covers
  token/password/user. The only gaps for Basic are proto, CLI, and the
  proto→`RemoteSource` mapping in `getter/cmd/getter/main.go`.
- **No custom-header auth mode exists.** `authHTTPSBearer` uses the PAT
  pattern (`x-access-token:<token>` via askpass), not an HTTP header. A true
  `Authorization: Bearer <jwt>` / `PRIVATE-TOKEN: <t>` needs a new mode using
  `git -c http.extraHeader`.
- **proto `RemoteRepo`** carries `oneof auth { string token; SshAuth ssh; }`.
- **getter mapping** (`main.go` ~line 230) maps `GetToken()`/`GetSsh()` only.
- **CLI `buildSource`** (`scan_source.go`) builds token/ssh only.
- **No config-default auth.** `getter.yaml` `git:` block has clone timeout /
  buffer sizes only.
- **Dependency:** e2e remote-clone testing requires the getter fixes on
  `fix/getter-redelivery` (the passwd-entry fix; otherwise `ssh`/`git` abort
  under arbitrary UID). That branch should merge to `main` before — or be
  rebased into — this feature's implementation.

## Decisions

| # | Decision | Rationale |
|---|----------|-----------|
| D1 | Carrier = typed messages in the existing `oneof auth` | Type-safe, mutually-exclusive methods at the contract level; matches ssh/severity style |
| D2 | Custom-header method is generic `HeaderAuth{name,value}`, not JWT-only | One mode covers Bearer/JWT, `token <pat>`, GitLab `PRIVATE-TOKEN`, custom |
| D3 | Separate, self-documenting CLI flags (not one packed `--remote-auth`) | Clearest; consistent with existing `--remote-*` flags |
| D4 | Each secret-bearing flag has an env fallback | Plaintext flag for CI/Jenkins that injects secrets; env keeps secrets out of shell history / `ps` |
| D5 | Config-default auth lives in `getter.yaml` as a per-host list | Server-side: credentials never travel in every request; one git host → one token, auto-applied |
| D6 | Precedence: per-scan auth → config default (host match) → none | Same "explicit overrides default" rule as the severity scaffold |

## Component design

### 1. Proto contract (`contracts/proto/harporis/v1/scan.proto`)

```proto
message BasicAuth  { string username = 1; string password = 2; }
message HeaderAuth { string name = 1; string value = 2; } // one HTTP header

message RemoteRepo {
  string url = 1;
  oneof auth {
    string     token  = 2;   // PAT-style: x-access-token:<token> (back-compat, unchanged)
    SshAuth    ssh    = 3;
    BasicAuth  basic  = 4;   // HTTPS Basic (login+password)
    HeaderAuth header = 5;   // arbitrary HTTP auth header
  }
}
```

`oneof` enforces exactly one method. `HeaderAuth` is a single header
(`repeated` deferred under YAGNI until a real multi-header server appears).

### 2. clone.go (`services/getter/internal/git/clone.go`)

- `authHTTPSBasic` — **already implemented**; no change beyond wiring.
- `authHTTPSHeader` — **new** `authModeKind`. When `RemoteSource.Header.Name`
  is set, clone with `git -c http.extraHeader="<name>: <value>" clone --quiet
  <url> <dest>`. The header value is never placed in the URL. Extend
  `redactSecrets` to strip `Header.Value` from error messages.
- `RemoteSource` gains a `Header struct{ Name, Value string }` field;
  `authMode()` returns `authHTTPSHeader` when it is set (selection order:
  ssh-like → token → basic → header).

### 3. CLI flags (`services/cli/internal/cmd/scan.go`, `scan_source.go`)

Per-scan, one method per scan (mutually exclusive — validated in
`buildSource`, extending the existing token↔ssh check):

| Flag | Method | Env fallback |
|------|--------|--------------|
| `--remote-token <pat>` | PAT (existing) | `HARPORIS_REMOTE_TOKEN` |
| `--remote-user <u>` + `--remote-password <p>` | HTTPS Basic | `HARPORIS_REMOTE_PASSWORD` |
| `--remote-bearer <token>` | `Authorization: Bearer <token>` | `HARPORIS_REMOTE_BEARER` |
| `--remote-header 'Name: Value'` | raw custom header (e.g. `PRIVATE-TOKEN: x`) | `HARPORIS_REMOTE_HEADER` |
| `--remote-ssh-key` / `--remote-known-hosts` | SSH (existing) | — |

- `--remote-bearer` is sugar that builds `HeaderAuth{name:"Authorization",
  value:"Bearer <token>"}`. `--remote-header` parses `Name: Value` into
  `HeaderAuth` directly.
- Env fallback: a flag left empty falls back to its env var before the request
  is built. Flag value wins when both are set.
- `buildSource` maps the chosen method to the proto `oneof`; >1 method → error
  listing the conflict.

### 4. Config-default auth (`services/getter/config/getter.yaml`, `internal/config`)

```yaml
git:
  clone_timeout_seconds: 600
  # ... existing keys ...
  default_auth:
    # Applied when a scan request for a matching host carries NO per-scan auth.
    # Matched by URL host; exact host or dot-suffix (".example.com"). First
    # match wins. Secrets via ${VAR} env-substitution — never hardcoded.
    - host: gitlab.mycompany.com
      header: { name: "PRIVATE-TOKEN", value: "${GITLAB_TOKEN}" }
    - host: github.com
      token: "${GITHUB_TOKEN}"
```

- New config types: `DefaultAuth []HostAuth`, where `HostAuth` has `Host
  string` plus one of `Token` / `Basic{User,Password}` / `Header{Name,Value}`
  (SSH default deferred — YAGNI).
- Validation (`validate.go`): each entry has non-empty `host` and exactly one
  auth method; unknown/empty → error (fail-fast at load).
- `${VAR}` substitution is already handled by `kit/config.LoadYAML`.

### 5. Mapping + precedence (`services/getter/cmd/getter/main.go`)

`sourceFromProto` (~line 230):
1. If the request's `RemoteRepo.oneof auth` is set → map it to `RemoteSource`
   (`GetToken`/`GetSsh`/`GetBasic`/`GetHeader`).
2. Else look up `cfg.Git.DefaultAuth` by the URL's host; first match →
   populate `RemoteSource` from that entry.
3. Else no auth (public clone).

A small `resolveAuth(url, reqAuth, defaults)` helper keeps the precedence in
one place (mirrors the severity scaffold's single resolution rule).

### 6. Security

- Secrets never in argv: Basic/token via `GIT_ASKPASS`; Header via
  `git -c http.extraHeader` (config value, not argv-visible to other users on
  multi-user hosts — acceptable as getter runs in its own container).
- Error redaction extended to `HeaderAuth.Value` and Basic password (Basic
  already redacted).
- Config secrets only via `${VAR}` env-substitution; plaintext in YAML is
  possible but documented as discouraged.

## Testing (TDD)

- **clone.go**: `authMode()` returns `authHTTPSHeader` when Header set; clone
  command contains `http.extraHeader` with the right `Name: Value` and the
  secret is NOT in the URL; redaction strips header value from errors. Basic
  path already covered — add a mapping test.
- **CLI**: each flag builds the correct proto `oneof`; `--remote-bearer`
  produces `Authorization: Bearer`; `--remote-header` parses `Name: Value`;
  env fallback applies when flag empty; >1 method → error; malformed
  `--remote-header` (no colon) → error.
- **getter config**: `default_auth` parses; per-entry validation (one method,
  non-empty host); `${VAR}` substitution.
- **precedence**: `resolveAuth` — per-scan wins over default; default applies
  on host match only; no match → no auth; host suffix matching.
- **getter mapping**: proto `GetBasic`/`GetHeader` → `RemoteSource`.

## What is NOT touched

- SSH key/agent auth (already works).
- The PAT-style `token` path (kept for back-compat).
- scanner / writer / contracts severity work.

## Recipe note

This reuses the severity scaffold's "config default → per-scan override"
precedence rule (D6) — `resolveAuth` is the auth instance of that pattern.
