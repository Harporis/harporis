# Remote Repository Authentication Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let operators scan private HTTPS repos with Basic (user+password) and arbitrary-header (Bearer/JWT/`PRIVATE-TOKEN`/custom) auth — per-scan via CLI flags, or as auto-applied per-host defaults in `getter.yaml`.

**Architecture:** Add two typed messages to the proto `oneof auth` (`BasicAuth`, `HeaderAuth`). The getter maps them onto the existing `git.RemoteSource` (Basic is already implemented in clone.go; Header is a new clone mode using git config env vars). A `resolveAuth(host, reqAuth, defaults)` helper layers per-host config defaults under per-scan auth. The CLI grows self-documenting `--remote-*` flags with env fallbacks.

**Tech Stack:** Go, protobuf (protoc via `contracts/Makefile`), cobra CLI, git CLI (`GIT_ASKPASS`, `GIT_CONFIG_*`), YAML config (`kit/config.LoadYAML` with `${VAR}` substitution).

## Global Constraints

- **Base branch:** create `feat/remote-auth` from `main` **only after `fix/getter-redelivery` is merged**, OR rebase this branch onto it before implementing Tasks 3/5/7. `getter/cmd/getter/main.go`, `services/getter/config/getter.yaml`, and `services/getter/internal/config/validate.go` are touched by both — sequence to avoid conflicts. (Spec §Background, Warnings.)
- **Secrets never in argv:** Basic/token passwords go via `GIT_ASKPASS`; header values go via `GIT_CONFIG_COUNT`/`GIT_CONFIG_KEY_0`/`GIT_CONFIG_VALUE_0` env vars (NOT `git -c key=value`, which exposes argv on multi-user hosts). Usernames may appear in the URL (not secret).
- **`oneof` enforces exactly one method** at the contract level. The CLI rejects >1 method before building the request.
- **Config secrets via `${VAR}` env-substitution** only (already handled by `kit/config.LoadYAML`); plaintext YAML allowed but documented as discouraged.
- **Precedence (D6):** per-scan auth → config default (first host match) → no auth (public clone).
- **Back-compat untouched:** existing `--remote-token` (PAT `x-access-token` pattern) and SSH key/agent auth keep their current behavior. No changes to scanner/writer/severity.
- **TDD throughout:** every new function gets a failing test first. Run `go test ./...` from each affected module root (`services/getter`, `services/cli`, `contracts`).

---

## File Structure

| File | Responsibility | Change |
|------|----------------|--------|
| `contracts/proto/harporis/v1/scan.proto` | wire contract | add `BasicAuth`, `HeaderAuth`; extend `oneof auth` |
| `contracts/gen/go/harporis/v1/scan.pb.go` | generated | regenerate (`make gen`) |
| `services/getter/internal/git/clone.go` | clone command builder | add `Header` field, `authHTTPSHeader` mode, redaction |
| `services/getter/internal/git/clone_test.go` | clone tests | new cases |
| `services/getter/cmd/getter/main.go` | proto→source mapping + precedence | map Basic/Header; call `resolveAuth` |
| `services/getter/cmd/getter/main_test.go` | mapping tests | new cases (create if absent) |
| `services/getter/internal/config/config.go` | config types | add `DefaultAuth []HostAuth`, `HostAuth` |
| `services/getter/internal/config/validate.go` | config validation | validate `default_auth` |
| `services/getter/internal/config/validate_test.go` | validation tests | new cases |
| `services/getter/config/getter.yaml` | shipped defaults + docs | documented `default_auth` example (commented) |
| `services/cli/internal/cmd/scan.go` | flag wiring | new `--remote-*` flags, env fallback |
| `services/cli/internal/cmd/scan_source.go` | request builder | `remoteAuth` struct + `buildSource` |
| `services/cli/internal/cmd/scan_source_test.go` | builder tests | new cases |

---

## Task 1: Proto contract — BasicAuth + HeaderAuth

**Files:**
- Modify: `contracts/proto/harporis/v1/scan.proto` (the `RemoteRepo` message, ~line 30)
- Regenerate: `contracts/gen/go/harporis/v1/scan.pb.go`

**Interfaces:**
- Produces (generated Go): `*v1.BasicAuth` with `GetUsername() string`, `GetPassword() string`; `*v1.HeaderAuth` with `GetName() string`, `GetValue() string`; `*v1.RemoteRepo_Basic{Basic *BasicAuth}`, `*v1.RemoteRepo_Header{Header *HeaderAuth}`; accessors `rem.GetBasic() *BasicAuth`, `rem.GetHeader() *HeaderAuth`.

- [ ] **Step 1: Edit the proto.** In `contracts/proto/harporis/v1/scan.proto`, replace the `RemoteRepo` message and add the two messages directly above it:

```proto
message BasicAuth {
  string username = 1;
  string password = 2;
}

// HeaderAuth carries one HTTP request header, e.g.
// {name:"Authorization", value:"Bearer <jwt>"} or {name:"PRIVATE-TOKEN", value:"<t>"}.
message HeaderAuth {
  string name  = 1;
  string value = 2;
}

message RemoteRepo {
  string url = 1;
  oneof auth {
    string     token  = 2;   // PAT-style: x-access-token:<token> (back-compat)
    SshAuth    ssh    = 3;
    BasicAuth  basic  = 4;   // HTTPS Basic (login+password)
    HeaderAuth header = 5;   // arbitrary HTTP auth header
  }
}
```

- [ ] **Step 2: Regenerate Go bindings.**

Run: `cd contracts && make gen`
Expected: exit 0; `git -C .. status` shows `contracts/gen/go/harporis/v1/scan.pb.go` modified. If `protoc`/`protoc-gen-go`/`protoc-gen-go-grpc` are missing, install them first (`go install google.golang.org/protobuf/cmd/protoc-gen-go@latest` and `.../protoc-gen-go-grpc@latest`, ensure `protoc` on PATH).

- [ ] **Step 3: Verify it compiles.**

Run: `cd contracts && go build ./...`
Expected: PASS (no errors).

- [ ] **Step 4: Commit.**

```bash
git add contracts/proto/harporis/v1/scan.proto contracts/gen/go/harporis/v1/scan.pb.go
git commit -m "feat(contracts): add BasicAuth + HeaderAuth to RemoteRepo oneof"
```

---

## Task 2: clone.go — HeaderAuth clone mode

**Files:**
- Modify: `services/getter/internal/git/clone.go` (`RemoteSource` ~line 36; `buildCloneCommand` ~line 134; `authModeKind` consts ~line 217; `authMode` ~line 225; `redactSecrets` ~line 327)
- Test: `services/getter/internal/git/clone_test.go`

**Interfaces:**
- Consumes: existing `RemoteSource{URL, Token, BasicUser, BasicPassword, SSH*}`.
- Produces: `RemoteSource.Header struct{ Name, Value string }`; new const `authHTTPSHeader`; `authMode` returns it when `Header.Name != ""`; `buildCloneCommand` emits a clone whose env carries `GIT_CONFIG_COUNT=1`, `GIT_CONFIG_KEY_0=http.extraHeader`, `GIT_CONFIG_VALUE_0=<Name>: <Value>`.

- [ ] **Step 1: Write the failing test** in `clone_test.go`:

```go
func TestAuthMode_HeaderSelected(t *testing.T) {
	src := RemoteSource{URL: "https://example.com/r.git", Header: struct{ Name, Value string }{Name: "Authorization", Value: "Bearer xyz"}}
	if got := authMode(src); got != authHTTPSHeader {
		t.Fatalf("authMode = %v, want authHTTPSHeader", got)
	}
}

func TestBuildCloneCommand_HeaderUsesGitConfigEnvNotArgv(t *testing.T) {
	src := RemoteSource{URL: "https://example.com/r.git", Header: struct{ Name, Value string }{Name: "PRIVATE-TOKEN", Value: "s3cr3t"}}
	cc, err := buildCloneCommand(src, "/tmp/dest")
	if err != nil {
		t.Fatalf("buildCloneCommand: %v", err)
	}
	defer cc.Cleanup()
	// secret must NOT be in argv
	for _, a := range cc.Args {
		if strings.Contains(a, "s3cr3t") {
			t.Fatalf("secret leaked into argv: %v", cc.Args)
		}
	}
	env := strings.Join(cc.Env, "\n")
	for _, want := range []string{"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.extraHeader", "GIT_CONFIG_VALUE_0=PRIVATE-TOKEN: s3cr3t"} {
		if !strings.Contains(env, want) {
			t.Fatalf("env missing %q; got:\n%s", want, env)
		}
	}
	// URL must be unchanged (header is not embedded in URL)
	if cc.Args[len(cc.Args)-2] != "https://example.com/r.git" {
		t.Fatalf("URL altered: %v", cc.Args)
	}
}

func TestRedactSecrets_StripsHeaderValue(t *testing.T) {
	src := RemoteSource{Header: struct{ Name, Value string }{Name: "Authorization", Value: "Bearer leaky"}}
	out := redactSecrets(src, "fatal: auth failed with Bearer leaky")
	if strings.Contains(out, "leaky") {
		t.Fatalf("header value not redacted: %q", out)
	}
}
```

- [ ] **Step 2: Run to verify it fails.**

Run: `cd services/getter && go test ./internal/git/ -run 'Header|RedactSecrets_StripsHeaderValue' -v`
Expected: build failure (`Header` field undefined, `authHTTPSHeader` undefined).

- [ ] **Step 3: Add the `Header` field** to `RemoteSource` (after `BasicPassword`, line 42):

```go
	// HTTPS auth
	Token         string
	BasicUser     string
	BasicPassword string

	// Arbitrary HTTP header auth (Bearer/JWT/PRIVATE-TOKEN/custom).
	Header struct {
		Name  string
		Value string
	}
```

- [ ] **Step 4: Add the const** `authHTTPSHeader` to the `authModeKind` block (after `authHTTPSBasic`, line 220):

```go
const (
	authHTTPSNone authModeKind = iota
	authHTTPSBearer
	authHTTPSBasic
	authHTTPSHeader
	authSSHKey
	authSSHAgent
)
```

- [ ] **Step 5: Select the mode** in `authMode` (after the `BasicUser` check, line 244). Order: ssh-like → token → basic → header → none:

```go
	if src.BasicUser != "" {
		return authHTTPSBasic
	}
	if src.Header.Name != "" {
		return authHTTPSHeader
	}
	return authHTTPSNone
```

- [ ] **Step 6: Add the clone case** in `buildCloneCommand` (after the `authHTTPSBasic` case, before `authSSHKey`, line 187). The header value rides in `GIT_CONFIG_*` env, not argv; the URL is untouched:

```go
	case authHTTPSHeader:
		// Header value (which may be a secret token) is passed via
		// git's GIT_CONFIG_* env vars so it never enters argv. Equivalent
		// to `git -c http.extraHeader="<Name>: <Value>"` but argv-safe.
		return cloneCommand{
			Args: []string{"git", "clone", "--quiet", src.URL, dest},
			Env: []string{
				"GIT_TERMINAL_PROMPT=0",
				"GIT_CONFIG_COUNT=1",
				"GIT_CONFIG_KEY_0=http.extraHeader",
				"GIT_CONFIG_VALUE_0=" + src.Header.Name + ": " + src.Header.Value,
			},
			Cleanup: noopCleanup,
		}, nil
```

- [ ] **Step 7: Redact the header value** in `redactSecrets` (after the `BasicUser` block, line 336):

```go
	if src.Header.Value != "" {
		msg = strings.ReplaceAll(msg, src.Header.Value, "<redacted-header>")
	}
	return msg
```

- [ ] **Step 8: Run tests to verify they pass.**

Run: `cd services/getter && go test ./internal/git/ -v`
Expected: PASS (all, including existing clone tests).

- [ ] **Step 9: Commit.**

```bash
git add services/getter/internal/git/clone.go services/getter/internal/git/clone_test.go
git commit -m "feat(getter): add HTTPS header auth clone mode (argv-safe via GIT_CONFIG_*)"
```

---

## Task 3: getter mapping — proto Basic/Header → RemoteSource

**Files:**
- Modify: `services/getter/cmd/getter/main.go` (`sourceFromProto`, ~line 219)
- Test: `services/getter/cmd/getter/main_test.go` (create if absent — same package `main`)

**Interfaces:**
- Consumes: `v1.RemoteRepo` accessors from Task 1; `git.RemoteSource{Header}` from Task 2.
- Produces: `sourceFromProto` populates `out.BasicUser/BasicPassword` from `rem.GetBasic()` and `out.Header.{Name,Value}` from `rem.GetHeader()`.

- [ ] **Step 1: Write the failing test** in `main_test.go`:

```go
package main

import (
	"testing"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/services/getter/internal/git"
)

func TestSourceFromProto_Basic(t *testing.T) {
	s := &v1.Source{Src: &v1.Source_Remote{Remote: &v1.RemoteRepo{
		Url:  "https://example.com/r.git",
		Auth: &v1.RemoteRepo_Basic{Basic: &v1.BasicAuth{Username: "alice", Password: "pw"}},
	}}}
	got, err := sourceFromProto(s)
	if err != nil {
		t.Fatalf("sourceFromProto: %v", err)
	}
	rs, ok := got.(git.RemoteSource)
	if !ok {
		t.Fatalf("want RemoteSource, got %T", got)
	}
	if rs.BasicUser != "alice" || rs.BasicPassword != "pw" {
		t.Fatalf("basic not mapped: %+v", rs)
	}
}

func TestSourceFromProto_Header(t *testing.T) {
	s := &v1.Source{Src: &v1.Source_Remote{Remote: &v1.RemoteRepo{
		Url:  "https://example.com/r.git",
		Auth: &v1.RemoteRepo_Header{Header: &v1.HeaderAuth{Name: "Authorization", Value: "Bearer x"}},
	}}}
	got, err := sourceFromProto(s)
	if err != nil {
		t.Fatalf("sourceFromProto: %v", err)
	}
	rs := got.(git.RemoteSource)
	if rs.Header.Name != "Authorization" || rs.Header.Value != "Bearer x" {
		t.Fatalf("header not mapped: %+v", rs)
	}
}
```

- [ ] **Step 2: Run to verify it fails.**

Run: `cd services/getter && go test ./cmd/getter/ -run SourceFromProto -v`
Expected: FAIL (basic/header fields empty — mapping missing).

- [ ] **Step 3: Add the mapping** in `sourceFromProto` (after the `GetSsh()` block, line 237):

```go
	if ssh := rem.GetSsh(); ssh != nil {
		out.SSHPrivateKeyPEM = []byte(ssh.PrivateKeyPem)
		out.SSHKnownHosts = []byte(ssh.KnownHosts)
	}
	if b := rem.GetBasic(); b != nil {
		out.BasicUser = b.GetUsername()
		out.BasicPassword = b.GetPassword()
	}
	if h := rem.GetHeader(); h != nil {
		out.Header.Name = h.GetName()
		out.Header.Value = h.GetValue()
	}
	return out, nil
```

- [ ] **Step 4: Run tests to verify they pass.**

Run: `cd services/getter && go test ./cmd/getter/ -v`
Expected: PASS.

- [ ] **Step 5: Commit.**

```bash
git add services/getter/cmd/getter/main.go services/getter/cmd/getter/main_test.go
git commit -m "feat(getter): map proto Basic/Header auth to RemoteSource"
```

---

## Task 4: getter config — per-host default_auth

**Files:**
- Modify: `services/getter/internal/config/config.go` (`GitConfig`, ~line 38)
- Modify: `services/getter/internal/config/validate.go` (`Validate`, ~line 8)
- Test: `services/getter/internal/config/validate_test.go`

**Interfaces:**
- Produces: `GitConfig.DefaultAuth []HostAuth`; `HostAuth{ Host string; Token string; Basic *BasicAuthCfg; Header *HeaderAuthCfg }`; `BasicAuthCfg{User, Password string}`; `HeaderAuthCfg{Name, Value string}`. `Validate` returns an error per entry that lacks a host or does not have exactly one auth method.

- [ ] **Step 1: Write the failing test** in `validate_test.go`. (Reuse the existing test's helper for a valid base `*Config` if one exists; otherwise construct a minimal valid config and only vary `Git.DefaultAuth`.)

```go
func TestValidate_DefaultAuth(t *testing.T) {
	base := validBaseConfig(t) // existing helper; if absent, build a minimal valid Config inline
	cases := []struct {
		name    string
		entries []HostAuth
		wantErr bool
	}{
		{"valid token entry", []HostAuth{{Host: "github.com", Token: "${T}"}}, false},
		{"valid header entry", []HostAuth{{Host: "gitlab.com", Header: &HeaderAuthCfg{Name: "PRIVATE-TOKEN", Value: "${T}"}}}, false},
		{"empty host", []HostAuth{{Token: "x"}}, true},
		{"no method", []HostAuth{{Host: "github.com"}}, true},
		{"two methods", []HostAuth{{Host: "github.com", Token: "x", Header: &HeaderAuthCfg{Name: "A", Value: "b"}}}, true},
		{"header missing name", []HostAuth{{Host: "github.com", Header: &HeaderAuthCfg{Value: "b"}}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := base
			c.Git.DefaultAuth = tc.entries
			err := Validate(&c)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate err=%v, wantErr=%v", err, tc.wantErr)
			}
		})
	}
}
```

- [ ] **Step 2: Run to verify it fails.**

Run: `cd services/getter && go test ./internal/config/ -run Validate_DefaultAuth -v`
Expected: build failure (`HostAuth`, `HeaderAuthCfg`, `Git.DefaultAuth` undefined).

- [ ] **Step 3: Add the config types** in `config.go`. Extend `GitConfig` and add the new types after it:

```go
type GitConfig struct {
	CloneTimeout         time.Duration `yaml:"-"`
	CloneTimeoutSeconds  int           `yaml:"clone_timeout_seconds"`
	CatFileBatchBufferKB int           `yaml:"cat_file_batch_buffer_kb"`
	DefaultAuth          []HostAuth    `yaml:"default_auth"`
}

// HostAuth is a per-host credential default, applied to any scan of a
// matching host that carries no per-scan auth. Exactly one of
// Token/Basic/Header must be set. Secrets should use ${VAR}.
type HostAuth struct {
	Host   string         `yaml:"host"`
	Token  string         `yaml:"token"`
	Basic  *BasicAuthCfg  `yaml:"basic"`
	Header *HeaderAuthCfg `yaml:"header"`
}

type BasicAuthCfg struct {
	User     string `yaml:"user"`
	Password string `yaml:"password"`
}

type HeaderAuthCfg struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}
```

- [ ] **Step 4: Add validation** in `validate.go`, before `return errors.Join(errs...)` (line 65):

```go
	for i, ha := range c.Git.DefaultAuth {
		if ha.Host == "" {
			errs = append(errs, fmt.Errorf("git.default_auth[%d].host: must not be empty", i))
		}
		n := 0
		if ha.Token != "" {
			n++
		}
		if ha.Basic != nil {
			n++
			if ha.Basic.User == "" || ha.Basic.Password == "" {
				errs = append(errs, fmt.Errorf("git.default_auth[%d].basic: user and password required", i))
			}
		}
		if ha.Header != nil {
			n++
			if ha.Header.Name == "" || ha.Header.Value == "" {
				errs = append(errs, fmt.Errorf("git.default_auth[%d].header: name and value required", i))
			}
		}
		if n != 1 {
			errs = append(errs, fmt.Errorf("git.default_auth[%d]: exactly one of token/basic/header required (got %d)", i, n))
		}
	}
```

- [ ] **Step 5: Run tests to verify they pass.**

Run: `cd services/getter && go test ./internal/config/ -v`
Expected: PASS. (If `validBaseConfig` did not exist, the new test builds its own minimal Config — confirm it still constructs a config that passes the unrelated checks.)

- [ ] **Step 6: Commit.**

```bash
git add services/getter/internal/config/config.go services/getter/internal/config/validate.go services/getter/internal/config/validate_test.go
git commit -m "feat(getter): config default_auth per-host credentials + validation"
```

---

## Task 5: getter precedence — resolveAuth

**Files:**
- Modify: `services/getter/cmd/getter/main.go` (`sourceFromProto` ~line 219; add `resolveAuth` helper)
- Test: `services/getter/cmd/getter/main_test.go`

**Interfaces:**
- Consumes: `git.RemoteSource` (Task 2/3), `config.HostAuth` (Task 4).
- Produces: `func resolveAuth(rawURL string, reqHasAuth bool, defaults []config.HostAuth) (token string, basicUser, basicPass, headerName, headerValue string)` — returns empty strings when `reqHasAuth` is true (caller already mapped per-scan auth) or no host matches; matches by exact host or dot-suffix; first match wins. `sourceFromProto` gains a `cfg *config.Config` parameter (update the call site at main.go ~line 144 to pass the loaded config).

- [ ] **Step 1: Write the failing test** in `main_test.go`:

```go
func TestResolveAuth_Precedence(t *testing.T) {
	defs := []config.HostAuth{
		{Host: "gitlab.mycompany.com", Header: &config.HeaderAuthCfg{Name: "PRIVATE-TOKEN", Value: "glt"}},
		{Host: "github.com", Token: "ght"},
	}
	t.Run("per-scan auth wins (defaults ignored)", func(t *testing.T) {
		tok, _, _, _, _ := resolveAuth("https://github.com/x.git", true, defs)
		if tok != "" {
			t.Fatalf("expected no default applied, got token %q", tok)
		}
	})
	t.Run("exact host match applies token", func(t *testing.T) {
		tok, _, _, _, _ := resolveAuth("https://github.com/x.git", false, defs)
		if tok != "ght" {
			t.Fatalf("token = %q, want ght", tok)
		}
	})
	t.Run("header default applies", func(t *testing.T) {
		_, _, _, hn, hv := resolveAuth("https://gitlab.mycompany.com/x.git", false, defs)
		if hn != "PRIVATE-TOKEN" || hv != "glt" {
			t.Fatalf("header = %q:%q", hn, hv)
		}
	})
	t.Run("dot-suffix host match", func(t *testing.T) {
		defs2 := []config.HostAuth{{Host: ".example.com", Token: "et"}}
		tok, _, _, _, _ := resolveAuth("https://git.example.com/x.git", false, defs2)
		if tok != "et" {
			t.Fatalf("suffix match failed, token=%q", tok)
		}
	})
	t.Run("no match → no auth", func(t *testing.T) {
		tok, _, _, _, _ := resolveAuth("https://other.com/x.git", false, defs)
		if tok != "" {
			t.Fatalf("expected empty, got %q", tok)
		}
	})
}
```

- [ ] **Step 2: Run to verify it fails.**

Run: `cd services/getter && go test ./cmd/getter/ -run ResolveAuth -v`
Expected: FAIL (`resolveAuth` undefined).

- [ ] **Step 3: Add `resolveAuth`** in `main.go` (after `sourceFromProto`). Add `"net/url"` and the `config` import if not already present:

```go
// resolveAuth returns the config-default credentials for rawURL's host
// when the scan request carried no per-scan auth. Empty return = no
// default applies (request already authed, or no host match). Host
// matches exactly, or as a dot-suffix entry (".example.com" matches
// "git.example.com"). First matching entry wins.
func resolveAuth(rawURL string, reqHasAuth bool, defaults []config.HostAuth) (token, basicUser, basicPass, headerName, headerValue string) {
	if reqHasAuth || len(defaults) == 0 {
		return
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return
	}
	host := u.Hostname()
	for _, ha := range defaults {
		match := ha.Host == host ||
			(strings.HasPrefix(ha.Host, ".") && strings.HasSuffix(host, ha.Host))
		if !match {
			continue
		}
		token = ha.Token
		if ha.Basic != nil {
			basicUser, basicPass = ha.Basic.User, ha.Basic.Password
		}
		if ha.Header != nil {
			headerName, headerValue = ha.Header.Name, ha.Header.Value
		}
		return
	}
	return
}
```

- [ ] **Step 4: Wire it into `sourceFromProto`.** Change the signature to accept config and apply defaults when the request set no auth. Replace the remote branch (lines 226–238):

```go
func sourceFromProto(s *v1.Source, cfg *config.Config) (git.Source, error) {
	if s == nil {
		return nil, fmt.Errorf("Source field is required")
	}
	if p := s.GetLocalPath(); p != "" {
		return git.LocalSource{Path: p}, nil
	}
	rem := s.GetRemote()
	if rem == nil || rem.Url == "" {
		return nil, fmt.Errorf("Source.remote.url is required when local_path is empty")
	}
	out := git.RemoteSource{URL: rem.Url}
	reqHasAuth := rem.GetToken() != "" || rem.GetSsh() != nil || rem.GetBasic() != nil || rem.GetHeader() != nil
	if tok := rem.GetToken(); tok != "" {
		out.Token = tok
	}
	if ssh := rem.GetSsh(); ssh != nil {
		out.SSHPrivateKeyPEM = []byte(ssh.PrivateKeyPem)
		out.SSHKnownHosts = []byte(ssh.KnownHosts)
	}
	if b := rem.GetBasic(); b != nil {
		out.BasicUser = b.GetUsername()
		out.BasicPassword = b.GetPassword()
	}
	if h := rem.GetHeader(); h != nil {
		out.Header.Name = h.GetName()
		out.Header.Value = h.GetValue()
	}
	if cfg != nil {
		tok, bu, bp, hn, hv := resolveAuth(rem.Url, reqHasAuth, cfg.Git.DefaultAuth)
		if tok != "" {
			out.Token = tok
		}
		if bu != "" {
			out.BasicUser, out.BasicPassword = bu, bp
		}
		if hn != "" {
			out.Header.Name, out.Header.Value = hn, hv
		}
	}
	return out, nil
}
```

- [ ] **Step 5: Update the call site** at main.go ~line 144 — pass the config that's already in scope (the handler that builds the source has access to the loaded `cfg`; pass it):

```go
		source, err := sourceFromProto(req.Source, cfg)
```

(If `cfg` is not in scope at that point, thread it from where `config.Load` is called — it is passed into the consumer/handler constructor. Add the parameter there.)

- [ ] **Step 6: Update the Task-3 mapping tests** to pass `nil` config: change `sourceFromProto(s)` → `sourceFromProto(s, nil)` in `TestSourceFromProto_Basic`/`_Header`.

- [ ] **Step 7: Run tests to verify they pass.**

Run: `cd services/getter && go test ./... `
Expected: PASS (mapping + precedence + everything else).

- [ ] **Step 8: Commit.**

```bash
git add services/getter/cmd/getter/main.go services/getter/cmd/getter/main_test.go
git commit -m "feat(getter): apply per-host default_auth with per-scan override (resolveAuth)"
```

---

## Task 6: CLI flags — Basic / Bearer / Header + env fallback

**Files:**
- Modify: `services/cli/internal/cmd/scan_source.go` (`buildSource`, end of file)
- Modify: `services/cli/internal/cmd/scan.go` (flag declarations ~line 157–160; call site ~line 83)
- Test: `services/cli/internal/cmd/scan_source_test.go`

**Interfaces:**
- Consumes: `v1.RemoteRepo_Basic`, `v1.RemoteRepo_Header`, `v1.BasicAuth`, `v1.HeaderAuth` from Task 1.
- Produces: `type remoteAuth struct{ Token, User, Password, Bearer, Header, SSHKey, KnownHosts string }`; `buildSource(local, remoteURL string, a remoteAuth) (*v1.Source, error)` — exactly one HTTPS method (token | user+password | bearer | header) or SSH; `--remote-bearer` builds `HeaderAuth{Name:"Authorization", Value:"Bearer "+token}`; `--remote-header` parses `Name: Value` (error if no colon); >1 method → error.

- [ ] **Step 1: Write the failing tests** in `scan_source_test.go`:

```go
func TestBuildSource_BasicAuth(t *testing.T) {
	src, err := buildSource("", "https://x/r.git", remoteAuth{User: "alice", Password: "pw"})
	if err != nil {
		t.Fatalf("buildSource: %v", err)
	}
	b := src.GetRemote().GetBasic()
	if b == nil || b.Username != "alice" || b.Password != "pw" {
		t.Fatalf("basic not built: %+v", src.GetRemote())
	}
}

func TestBuildSource_BearerBuildsAuthorizationHeader(t *testing.T) {
	src, err := buildSource("", "https://x/r.git", remoteAuth{Bearer: "jwt123"})
	if err != nil {
		t.Fatalf("buildSource: %v", err)
	}
	h := src.GetRemote().GetHeader()
	if h == nil || h.Name != "Authorization" || h.Value != "Bearer jwt123" {
		t.Fatalf("bearer header wrong: %+v", h)
	}
}

func TestBuildSource_HeaderParsesNameColonValue(t *testing.T) {
	src, err := buildSource("", "https://x/r.git", remoteAuth{Header: "PRIVATE-TOKEN: abc"})
	if err != nil {
		t.Fatalf("buildSource: %v", err)
	}
	h := src.GetRemote().GetHeader()
	if h == nil || h.Name != "PRIVATE-TOKEN" || h.Value != "abc" {
		t.Fatalf("header parse wrong: %+v", h)
	}
}

func TestBuildSource_HeaderWithoutColonErrors(t *testing.T) {
	if _, err := buildSource("", "https://x/r.git", remoteAuth{Header: "no-colon"}); err == nil {
		t.Fatal("expected error for malformed --remote-header")
	}
}

func TestBuildSource_MultipleMethodsError(t *testing.T) {
	if _, err := buildSource("", "https://x/r.git", remoteAuth{Token: "t", Bearer: "b"}); err == nil {
		t.Fatal("expected error for >1 auth method")
	}
}
```

- [ ] **Step 2: Run to verify it fails.**

Run: `cd services/cli && go test ./internal/cmd/ -run BuildSource -v`
Expected: build failure (`remoteAuth` undefined; `buildSource` arity mismatch).

- [ ] **Step 3: Rewrite `buildSource`** in `scan_source.go` to take the struct and handle all methods. Replace the whole `func buildSource(...)`:

```go
// remoteAuth groups the per-scan HTTPS/SSH credential flags. At most one
// HTTPS method (Token | User+Password | Bearer | Header) may be set; SSH
// is mutually exclusive with all of them.
type remoteAuth struct {
	Token      string
	User       string
	Password   string
	Bearer     string
	Header     string // raw "Name: Value"
	SSHKey     string
	KnownHosts string
}

func buildSource(local, remoteURL string, a remoteAuth) (*v1.Source, error) {
	if local != "" {
		if remoteURL != "" {
			return nil, errors.New("--local and --remote-url are mutually exclusive")
		}
		return &v1.Source{Src: &v1.Source_LocalPath{LocalPath: local}}, nil
	}
	if remoteURL == "" {
		return nil, errors.New("either --local or --remote-url is required")
	}
	rr := &v1.RemoteRepo{Url: remoteURL}

	// Count chosen methods so >1 is a clear error rather than silent win.
	chosen := 0
	for _, set := range []bool{a.Token != "", a.User != "" || a.Password != "", a.Bearer != "", a.Header != "", a.SSHKey != ""} {
		if set {
			chosen++
		}
	}
	if chosen > 1 {
		return nil, errors.New("pick exactly one remote auth method: --remote-token | --remote-user/--remote-password | --remote-bearer | --remote-header | --remote-ssh-key")
	}

	switch {
	case a.Token != "":
		rr.Auth = &v1.RemoteRepo_Token{Token: a.Token}
	case a.User != "" || a.Password != "":
		if a.User == "" || a.Password == "" {
			return nil, errors.New("--remote-user and --remote-password must be set together")
		}
		rr.Auth = &v1.RemoteRepo_Basic{Basic: &v1.BasicAuth{Username: a.User, Password: a.Password}}
	case a.Bearer != "":
		rr.Auth = &v1.RemoteRepo_Header{Header: &v1.HeaderAuth{Name: "Authorization", Value: "Bearer " + a.Bearer}}
	case a.Header != "":
		name, value, ok := strings.Cut(a.Header, ":")
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		if !ok || name == "" || value == "" {
			return nil, fmt.Errorf("--remote-header %q must be 'Name: Value'", a.Header)
		}
		rr.Auth = &v1.RemoteRepo_Header{Header: &v1.HeaderAuth{Name: name, Value: value}}
	case a.SSHKey != "":
		key, err := os.ReadFile(a.SSHKey)
		if err != nil {
			return nil, fmt.Errorf("read ssh key %s: %w", a.SSHKey, err)
		}
		ssh := &v1.SshAuth{PrivateKeyPem: string(key)}
		if a.KnownHosts != "" {
			kh, err := os.ReadFile(a.KnownHosts)
			if err != nil {
				return nil, fmt.Errorf("read known_hosts %s: %w", a.KnownHosts, err)
			}
			ssh.KnownHosts = string(kh)
		}
		rr.Auth = &v1.RemoteRepo_Ssh{Ssh: ssh}
	}
	return &v1.Source{Src: &v1.Source_Remote{Remote: rr}}, nil
}
```

- [ ] **Step 4: Run the builder tests to verify they pass.**

Run: `cd services/cli && go test ./internal/cmd/ -run BuildSource -v`
Expected: PASS.

- [ ] **Step 5: Add the flags + env fallback** in `scan.go`. Declare vars alongside the existing `token`/`sshKey` (near line 157) and add flags. Add env fallback just before the `buildSource` call (line 83):

```go
	// flag vars (add near existing remote flag vars)
	var remoteUser, remotePassword, remoteBearer, remoteHeader string

	// ... flag declarations (after line 160) ...
	c.Flags().StringVar(&remoteUser, "remote-user", "", "HTTPS Basic username (with --remote-password)")
	c.Flags().StringVar(&remotePassword, "remote-password", "", "HTTPS Basic password (env: HARPORIS_REMOTE_PASSWORD)")
	c.Flags().StringVar(&remoteBearer, "remote-bearer", "", "Bearer token → 'Authorization: Bearer <t>' (env: HARPORIS_REMOTE_BEARER)")
	c.Flags().StringVar(&remoteHeader, "remote-header", "", "raw auth header 'Name: Value', e.g. 'PRIVATE-TOKEN: x' (env: HARPORIS_REMOTE_HEADER)")
```

And replace the `buildSource` call (line 83) with env-fallback resolution:

```go
			if remotePassword == "" {
				remotePassword = os.Getenv("HARPORIS_REMOTE_PASSWORD")
			}
			if remoteBearer == "" {
				remoteBearer = os.Getenv("HARPORIS_REMOTE_BEARER")
			}
			if remoteHeader == "" {
				remoteHeader = os.Getenv("HARPORIS_REMOTE_HEADER")
			}
			if token == "" {
				token = os.Getenv("HARPORIS_REMOTE_TOKEN")
			}
			src, err := buildSource(translated, remoteURL, remoteAuth{
				Token: token, User: remoteUser, Password: remotePassword,
				Bearer: remoteBearer, Header: remoteHeader,
				SSHKey: sshKey, KnownHosts: knownHosts,
			})
			if err != nil {
				return err
			}
```

(Update the existing `--remote-token` flag help to mention `env: HARPORIS_REMOTE_TOKEN`.)

- [ ] **Step 6: Run the full CLI suite to verify nothing broke.**

Run: `cd services/cli && go build ./... && go test ./...`
Expected: PASS.

- [ ] **Step 7: Commit.**

```bash
git add services/cli/internal/cmd/scan.go services/cli/internal/cmd/scan_source.go services/cli/internal/cmd/scan_source_test.go
git commit -m "feat(cli): --remote-user/-password, --remote-bearer, --remote-header + env fallbacks"
```

---

## Task 7: Docs — getter.yaml example + e2e verification

**Files:**
- Modify: `services/getter/config/getter.yaml` (the `git:` block, ~line 1)
- Verify: live stack (depends on `fix/getter-redelivery` passwd fix being present — see Global Constraints)

**Interfaces:** none (config docs + manual verification).

- [ ] **Step 1: Document `default_auth`** in `getter.yaml` as a commented example under `git:`:

```yaml
git:
  clone_timeout_seconds: 600
  cat_file_batch_buffer_kb: 64
  # Per-host credential defaults, auto-applied to any scan of a matching
  # host that carries NO per-scan auth. Matched by URL host (exact, or a
  # ".example.com" dot-suffix). First match wins. Use ${VAR} for secrets
  # (plaintext works but is discouraged). Exactly one method per entry.
  # default_auth:
  #   - host: gitlab.mycompany.com
  #     header: { name: "PRIVATE-TOKEN", value: "${GITLAB_TOKEN}" }
  #   - host: github.com
  #     token: "${GITHUB_TOKEN}"
```

- [ ] **Step 2: Verify config still loads.**

Run: `cd services/getter && go test ./internal/config/ -v`
Expected: PASS (commented YAML doesn't activate `default_auth`).

- [ ] **Step 3: e2e — rebuild the getter image and scan a private HTTPS repo** (requires `fix/getter-redelivery` merged/rebased in). Pick the method you can credential:

```bash
# Header (GitLab PRIVATE-TOKEN example)
harporis scan --remote-url https://gitlab.com/<group>/<private>.git --remote-header "PRIVATE-TOKEN: $GL_TOKEN"
# Bearer
harporis scan --remote-url https://<host>/<private>.git --remote-bearer "$JWT"
# Basic
harporis scan --remote-url https://<host>/<private>.git --remote-user "$U" --remote-password "$P"
```

Expected: scan reaches a terminal `completed` state with chunks produced; no credential appears in getter logs (`docker logs harporis-getter-1 | grep -i <secret>` returns nothing — redaction holds).

- [ ] **Step 4: Commit.**

```bash
git add services/getter/config/getter.yaml
git commit -m "docs(getter): document git.default_auth per-host credentials"
```

---

## Self-Review

**Spec coverage** (spec §§ → tasks):
- §1 Proto contract → Task 1 ✓
- §2 clone.go (Basic already done; Header new) → Task 2 ✓ (Basic wiring exercised via Task 3 mapping test)
- §3 CLI flags (token/user+password/bearer/header + env fallback + mutual exclusivity) → Task 6 ✓
- §4 Config default_auth + validation + ${VAR} → Task 4 (${VAR} handled by existing LoadYAML) ✓
- §5 Mapping + precedence (resolveAuth) → Tasks 3 + 5 ✓
- §6 Security (askpass / GIT_CONFIG_* not argv; redaction) → Task 2 (Header redaction + env), existing askpass for Basic ✓
- §Testing (TDD bullets) → covered across Tasks 2,3,4,5,6 ✓
- §Dependency on fix/getter-redelivery → Global Constraints + Task 7 ✓

**Deviation from spec (documented):** spec §6 names `git -c http.extraHeader=...`; this plan uses `GIT_CONFIG_COUNT/KEY_0/VALUE_0` env vars instead, which actually satisfies the spec's own "secrets never in argv" rule (`-c key=value` is argv-visible). Same git behavior, stronger guarantee.

**Placeholder scan:** none — every code step shows full code; every run step shows command + expected result.

**Type consistency:** `remoteAuth` fields, `HostAuth`/`BasicAuthCfg`/`HeaderAuthCfg`, `resolveAuth` signature, and `RemoteSource.Header` are used identically across Tasks 2–6. `sourceFromProto` arity change (Task 5) is reflected back into Task 3's tests (Task 5 Step 6).
