# Handoff: remote-auth shipped (branch pushed) + interactive-watch & docs backlog

**Generated**: 2026-06-21
**Branch**: `main` (current checkout, `0bbe420`). All feature branches now merged.
**Status**: remote-auth + suffix fix + release-tails + repo-cleanup all **merged & pushed to main**. Next = start **interactive watch** (subsystem A, not started).

## Goal

Continue the repo backlog. This session closed the `findings show` suffix bug and the **remote-auth** feature (HTTPS Basic + arbitrary-header auth for private-repo scans). Remaining backlog: interactive watch TUI, then a docs overhaul.

## Completed this session

- [x] **`findings show --format {pdf,html,sarif,xlsx,parquet}` suffix bug FIXED** — branch `fix/findings-show-suffix` (commit `d09f62e`, pushed). Added `resolveFindingsName` (exact `<scan><ext>` → glob `<scan>*<ext>` → clear error) in `services/cli/internal/cmd/findings.go`; suffix-tolerant in both host (`--output-dir`) and `docker compose exec writer` paths. Unit-tested + e2e-verified on the live stack (repro scan `3258dee2-475c-417a-a3c7-917b98e205c1`, suffix `.3bad9a0183a5`). All 5 proxy formats now stream; binaries stay valid; clear "no <fmt> report … available: …" on miss.
- [x] **remote-auth feature — MERGED to main (PR #17, `6b9bebb`)**. 8 commits `2e923a2..589a94f`. Full spec→plan→impl. Plan: `docs/superpowers/plans/2026-06-21-remote-auth.md`. Built via subagent-driven TDD, per-task review + final opus whole-branch review (verdict: merge-with-fixes, no Critical/Important code defects; fixes applied). All contracts/getter/cli tests green.

## Not Yet Done

- [x] **All branches merged to `main`** (`0bbe420`, pushed 2026-06-21): `fix/findings-show-suffix` (merge `94301e1`), `chore/repo-cleanup` (`459e4b4`), `docs/release-tails` (`0bbe420`). README.md + findings.go auto-merged cleanly; all 4 modules (contracts/getter/cli/writer) build + test green. (`feat/remote-auth` → PR #17; `fix/getter-redelivery` → PR #16 earlier.) The 3 local branches can be deleted.
- [ ] **remote-auth Task 7 Step 3 — live e2e** (deferred; doc Step 1-2 shipped in `589a94f`, already in main). Can now run against `main` directly — see Resume.
- [ ] **Interactive watch** (subsystem A, NOT started) — drill into scans (view results), filter + sort by all fields in the bubbletea TUI (`services/cli/internal/tui/{fleet,watch}_model.go`). Next: brainstorm→spec→plan→impl.
- [ ] **Docs overhaul** (subsystem B, NOT started) — do LAST; should cover remote-auth + watch.
- [ ] Optional follow-up (M3 from review): `slog.Warn` when auth is attached to an `http://` (cleartext) URL in `services/getter/internal/git/clone.go` `authMode`. Non-blocking.

## Failed Approaches (Don't Repeat These)

None abandoned this session. One deliberate **improvement over the spec** (not a failure): the spec said deliver the header via `git -c http.extraHeader=<v>`, but `-c key=value` is argv-visible (`ps`/proc) — violates the spec's own "secrets never in argv" rule. Implemented via `GIT_CONFIG_COUNT=1` / `GIT_CONFIG_KEY_0=http.extraHeader` / `GIT_CONFIG_VALUE_0=<Name>: <Value>` env vars instead. Same git behavior, secret never in argv. Documented in the plan's Self-Review.

## Key Decisions

| Decision | Rationale |
|----------|-----------|
| Header auth via `GIT_CONFIG_*` env, not `git -c` | Keeps secret out of argv (the spec's own security rule) |
| `feat/remote-auth` branched from `main` (not waiting) | `fix/getter-redelivery` was ALREADY merged to main (PR #16) — dependency self-resolved; no conflict, e2e now unblocked |
| Push branches, do NOT open PRs | User opens PRs; matches prior-handoff convention |
| `--remote-user` has NO env fallback (only password/bearer/header/token do) | Spec-faithful — usernames aren't secrets; accepted as-is in final review |

## Current State

**Working**: dev stack up (`harporis-{getter,scanner,writer,nats}-1`, `harporis/*:dev` images). All scans + reports work, including the previously-broken proxy formats. `feat/remote-auth` compiles & all unit tests pass; remote-auth wired end-to-end (proto → getter clone/mapping/config/precedence → CLI flags).

**Broken**: nothing known.

**Uncommitted Changes**: only untracked `.claude/` and `HANDOFF.md`. All work committed + pushed. Subagent-driven progress ledger + task briefs/reports live in `.git/sdd/` (not tracked) — useful if resuming remote-auth.

## Files to Know

| File | Why It Matters |
|------|----------------|
| `docs/superpowers/plans/2026-06-21-remote-auth.md` | The remote-auth plan; Task 7 Step 3 (e2e) is the only undone step |
| `contracts/proto/harporis/v1/scan.proto` | `RemoteRepo.oneof auth` now has `basic=4`, `header=5`; regen via `cd contracts && make gen` |
| `services/getter/internal/git/clone.go` | `authHTTPSHeader` clone mode (GIT_CONFIG_* env); `redactSecrets`; `authMode` order ssh→token→basic→header→none |
| `services/getter/cmd/getter/main.go` | `sourceFromProto(s, cfg)` maps proto→`RemoteSource`; `resolveAuth` applies per-host config defaults |
| `services/getter/internal/config/{config,validate}.go` + `config/getter.yaml` | `GitConfig.DefaultAuth []HostAuth` + validation + documented `default_auth` example |
| `services/cli/internal/cmd/{scan,scan_source}.go` | `remoteAuth` struct + `buildSource`; flags `--remote-user/-password/--remote-bearer/--remote-header` + env fallbacks |
| `services/cli/internal/tui/{fleet,watch}_model.go` | Where the NEXT feature (interactive watch) lives |

## Code Context

**CLI auth flags** (per-scan, exactly one; SSH mutually exclusive):
```
--remote-token <pat>                 # x-access-token PAT (existing)   env: HARPORIS_REMOTE_TOKEN
--remote-user <u> --remote-password  # HTTPS Basic                     env: HARPORIS_REMOTE_PASSWORD
--remote-bearer <t>                  # -> Authorization: Bearer <t>    env: HARPORIS_REMOTE_BEARER
--remote-header 'Name: Value'        # raw header, e.g. PRIVATE-TOKEN  env: HARPORIS_REMOTE_HEADER
```

**Precedence** (`resolveAuth` in getter main.go): per-scan auth → config `default_auth` (first host match: exact OR `.suffix`) → none. `reqHasAuth` (token||ssh||basic||header set) bypasses defaults.

**getter.yaml config default** (host match auto-applies when scan carries no per-scan auth):
```yaml
git:
  default_auth:
    - host: gitlab.mycompany.com
      header: { name: "PRIVATE-TOKEN", value: "${GITLAB_TOKEN}" }
    - host: github.com
      token: "${GITHUB_TOKEN}"
```

## Resume Instructions

**To finish remote-auth e2e (Task 7 Step 3) — now unblocked, feature is in main:**
1. `git checkout main && git pull` (remote-auth merged via PR #17)
2. Rebuild getter image and scan a private HTTPS repo with a real credential, e.g.:
   `harporis scan --remote-url https://gitlab.com/<grp>/<private>.git --remote-header "PRIVATE-TOKEN: $GL_TOKEN"`
   - Expected: scan reaches `completed` with chunks produced.
   - Verify redaction: `docker logs harporis-getter-1 | grep -i <secret>` returns NOTHING.
   - If clone fails under arbitrary UID: that's the passwd-entry fix — already in main via `fix/getter-redelivery`, so should be fine.

**To start interactive watch (subsystem A):**
1. Run `superpowers:brainstorming` on the watch feature first (it's a fresh feature, not yet specced).
2. Read `services/cli/internal/tui/fleet_model.go` and `watch_model.go` for the current bubbletea TUI shape.
3. Then spec→plan→impl (subagent-driven TDD, same as remote-auth).

## Warnings

- **Tag pushes: ONE AT A TIME** — >3 tags in a single `git push` triggers NO GitHub Actions release build (silent).
- **gopls diagnostics lie here**: the editor often shows "undefined: X" / "could not import" / stale function signatures for the `services/getter` & `services/cli` modules (they're not in a `go.work`). Trust `go build ./...` + `go test ./...` run from each module dir, not the IDE squiggles.
- GHCR packages default to PRIVATE — `docker compose -f docker-compose.ghcr.yml up` needs the packages made public OR `docker login ghcr.io` (PAT w/ read:packages).
- User language: **respond in Russian** (memory `user-language-russian`).
- Commits: **no `Co-Authored-By` trailer**.
- Multi-module monorepo: run `go` commands from the module dir (`services/getter`, `services/cli`, `contracts`) — there is no top-level `go.work`.
