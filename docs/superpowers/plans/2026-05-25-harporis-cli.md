# harporis CLI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the `harporis` CLI per [`docs/superpowers/specs/2026-05-25-harporis-cli-design.md`](../specs/2026-05-25-harporis-cli-design.md). One globally-installable binary that wraps scan operations, stack lifecycle, diagnostics, and history behind a cobra+lipgloss+bubble-tea interface.

**Architecture:** New `services/cli/` Go module. Cobra for the command tree, Lipgloss for styling, Bubble Tea for live panels (`watch`, `up`). Talks NATS via `kit/nats/wire` and proto via `contracts/gen/go/harporis/v1`. Stack lifecycle commands shell out to `docker compose`. Replaces `services/getter/cmd/getter-cli` entirely.

**Tech Stack:** Go 1.26, [cobra](https://github.com/spf13/cobra) v1.8+, [lipgloss](https://github.com/charmbracelet/lipgloss) v0.13+, [bubbletea](https://github.com/charmbracelet/bubbletea) v1.1+, [bubbles](https://github.com/charmbracelet/bubbles) v0.20+, [termenv](https://github.com/muesli/termenv) v0.15+, testify, `nats-server/v2` embedded for tests, [nfpm](https://nfpm.goreleaser.com/) v2.40+ for `.deb`/`.rpm`.

---

## File Structure

```
services/cli/                                   NEW module
├── go.mod                                      module github.com/Harporis/harporis/services/cli
├── go.sum
├── Makefile                                    build / install / deb / test / lint
├── README.md                                   install + command tour
├── Dockerfile                                  (optional, CI sanity build)
├── integration_test.go                         build tag `integration`, embedded NATS
│
├── cmd/harporis/main.go                        entry; calls cmd.Execute()
│
├── internal/
│   ├── cmd/
│   │   ├── root.go                             cobra root + global flags + persistent setup
│   │   ├── version.go                          prints version/commit/proto
│   │   ├── scan.go                             scan submit
│   │   ├── scan_source.go                      buildSource + scanTypeFromString helpers
│   │   ├── cancel.go
│   │   ├── watch.go                            picks line-based or TUI based on isatty/--json
│   │   ├── up.go
│   │   ├── down.go
│   │   ├── ps.go
│   │   ├── logs.go
│   │   ├── health.go
│   │   ├── history.go                          list + history show <id>
│   │   ├── metrics.go
│   │   ├── doctor.go
│   │   └── *_test.go
│   │
│   ├── ui/
│   │   ├── theme.go                            lipgloss palette + style helpers
│   │   ├── icons.go                            Unicode + ASCII fallback
│   │   ├── banner.go                           HARPORIS ASCII + dynamic bottom bar
│   │   ├── table.go                            simple table writer (used by ps/history/doctor)
│   │   └── *_test.go                           golden snapshots
│   │
│   ├── tui/
│   │   ├── watch_model.go                      bubble tea live dashboard
│   │   ├── up_model.go                         bubble tea startup checklist
│   │   └── *_test.go                           model-level tests (Init/Update/View)
│   │
│   ├── natscli/
│   │   ├── client.go                           Dial wrapper over kit/nats/wire
│   │   ├── history.go                          list past scans from status stream
│   │   └── *_test.go
│   │
│   ├── compose/
│   │   ├── compose.go                          Runner interface + DockerComposeRunner
│   │   └── compose_test.go                     stub Runner
│   │
│   ├── doctor/
│   │   ├── checks.go                           Check interface + concrete checks
│   │   └── checks_test.go
│   │
│   ├── config/
│   │   ├── config.go                           ~/.config/harporis/config.yaml loader
│   │   └── config_test.go
│   │
│   └── version/
│       └── version.go                          ldflags-injected vars (Version/Commit/ProtoVersion)
│
└── packaging/
    └── nfpm.yaml                               .deb / .rpm config

services/getter/cmd/getter-cli/                 REMOVED (Task 16)
services/getter/Makefile                        `build-cli` target removed (Task 16)
services/getter/QUICKSTART.md                   getter-cli refs → harporis (Task 16)
services/getter/README.md                       getter-cli refs → harporis (Task 16)
docker-compose.yml                              header comments updated (Task 16)
README.md (root)                                Install section (Task 16)
Makefile (root, NEW)                            proxy targets (Task 16)
```

---

## Task 1: Bootstrap `services/cli/` module + cobra skeleton + `version`

**Files:**
- Create: `services/cli/go.mod`
- Create: `services/cli/Makefile`
- Create: `services/cli/cmd/harporis/main.go`
- Create: `services/cli/internal/cmd/root.go`
- Create: `services/cli/internal/cmd/version.go`
- Create: `services/cli/internal/cmd/version_test.go`
- Create: `services/cli/internal/version/version.go`

- [ ] **Step 1: Create module skeleton**

Run from repo root:

```bash
mkdir -p services/cli/cmd/harporis
mkdir -p services/cli/internal/cmd
mkdir -p services/cli/internal/version
cd services/cli && go mod init github.com/Harporis/harporis/services/cli
```

- [ ] **Step 2: Add cobra; defer contracts/kit until Task 3**

contracts and kit get wired in Task 3 with their `replace` directives when we actually import them — Task 1 only needs cobra to bring up the skeleton.

```bash
cd services/cli
go get github.com/spf13/cobra@latest
go mod tidy
```

- [ ] **Step 3: Write the failing version test**

Create `services/cli/internal/cmd/version_test.go`:

```go
package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Harporis/harporis/services/cli/internal/version"
)

func TestVersionCommand(t *testing.T) {
	version.Version = "v9.9.9-test"
	version.Commit = "abcd123"
	version.ProtoVersion = "v1"
	t.Cleanup(func() {
		version.Version, version.Commit, version.ProtoVersion = "dev", "unknown", "v1"
	})

	var buf bytes.Buffer
	root := NewRootCmd()
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"version"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := buf.String()
	for _, want := range []string{"v9.9.9-test", "abcd123", "v1"} {
		if !strings.Contains(got, want) {
			t.Errorf("version output missing %q: %s", want, got)
		}
	}
}
```

- [ ] **Step 4: Run test to verify it fails**

```bash
cd services/cli && go test ./internal/cmd/...
```

Expected: build error — `cmd` package does not exist.

- [ ] **Step 5: Create version vars file**

Create `services/cli/internal/version/version.go`:

```go
// Package version holds build-time identity injected via -ldflags.
package version

var (
	// Version is the semver tag at build time, or "dev-<sha>" for untagged builds.
	Version = "dev"
	// Commit is the short git SHA.
	Commit = "unknown"
	// ProtoVersion is the major version of the harporis proto contract this binary speaks.
	ProtoVersion = "v1"
)
```

- [ ] **Step 6: Create root cobra command**

Create `services/cli/internal/cmd/root.go`:

```go
// Package cmd is the cobra command tree for the harporis CLI.
package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

// NewRootCmd builds a fresh root command. Used by main and by tests.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:          "harporis",
		Short:        "git-aware secret hunter — operator CLI",
		SilenceUsage: true,
	}
	root.PersistentFlags().String("nats", defaultNATSURL(), "NATS server URL (env NATS_URL)")
	root.PersistentFlags().Bool("no-color", false, "disable ANSI styling (env NO_COLOR)")
	root.PersistentFlags().Bool("json", false, "machine-readable JSON output on read commands")
	root.PersistentFlags().BoolP("quiet", "q", false, "suppress banner and secondary output")

	root.AddCommand(newVersionCmd())
	return root
}

// Execute is the package-level entry point used by main.
func Execute() {
	if err := NewRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func defaultNATSURL() string {
	if v := os.Getenv("NATS_URL"); v != "" {
		return v
	}
	return "nats://localhost:4222"
}
```

- [ ] **Step 7: Create version command**

Create `services/cli/internal/cmd/version.go`:

```go
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Harporis/harporis/services/cli/internal/version"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "print version, commit, and proto contract version",
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintf(cmd.OutOrStdout(),
				"harporis %s (commit %s, proto %s)\n",
				version.Version, version.Commit, version.ProtoVersion)
			return nil
		},
	}
}
```

- [ ] **Step 8: Create main.go**

Create `services/cli/cmd/harporis/main.go`:

```go
package main

import "github.com/Harporis/harporis/services/cli/internal/cmd"

func main() { cmd.Execute() }
```

- [ ] **Step 9: Run test to verify it passes**

```bash
cd services/cli && go test ./internal/cmd/... -v
```

Expected: `PASS: TestVersionCommand`.

- [ ] **Step 10: Create Makefile**

Create `services/cli/Makefile`:

```make
.PHONY: build install test test-integration lint clean

PREFIX ?= /usr/local
BIN     := bin/harporis
PKG     := github.com/Harporis/harporis/services/cli

VERSION := $(shell git describe --tags --dirty --always 2>/dev/null || echo dev)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS := -s -w \
	-X '$(PKG)/internal/version.Version=$(VERSION)' \
	-X '$(PKG)/internal/version.Commit=$(COMMIT)' \
	-X '$(PKG)/internal/version.ProtoVersion=v1'

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/harporis

install: build
	install -d $(PREFIX)/bin
	install -m 0755 $(BIN) $(PREFIX)/bin/harporis

test:
	go test ./... -race -timeout 90s

test-integration:
	go test ./... -race -timeout 180s -tags integration

lint:
	go vet ./...

clean:
	rm -rf bin/ dist/
```

- [ ] **Step 11: Verify build and binary works**

```bash
cd services/cli && make build && ./bin/harporis version && ./bin/harporis --help
```

Expected: `harporis dev (commit <sha>, proto v1)` then the help output listing the `version` subcommand.

- [ ] **Step 12: Commit**

```bash
git add services/cli/
git commit -m "feat(cli): bootstrap harporis module with cobra skeleton and version cmd"
```

---

## Task 2: UI primitives — theme, icons, banner

**Files:**
- Create: `services/cli/internal/ui/theme.go`
- Create: `services/cli/internal/ui/icons.go`
- Create: `services/cli/internal/ui/banner.go`
- Create: `services/cli/internal/ui/theme_test.go`
- Create: `services/cli/internal/ui/banner_test.go`
- Create: `services/cli/internal/ui/testdata/banner.golden`

- [ ] **Step 1: Add lipgloss and termenv deps**

```bash
cd services/cli
go get github.com/charmbracelet/lipgloss@latest
go get github.com/muesli/termenv@latest
go mod tidy
```

- [ ] **Step 2: Write the failing theme test**

Create `services/cli/internal/ui/theme_test.go`:

```go
package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestStateStyleHasColorWhenProfileSupports(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.Ascii) })

	got := StateStyle("RUNNING").Render("RUN")
	if !strings.Contains(got, "RUN") {
		t.Fatalf("style erased text: %q", got)
	}
	if !strings.Contains(got, "\x1b[") {
		t.Fatalf("expected ANSI escape on truecolor profile, got %q", got)
	}
}

func TestStateStyleNoColorInAsciiProfile(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	got := StateStyle("RUNNING").Render("RUN")
	if got != "RUN" {
		t.Fatalf("expected raw text in ascii profile, got %q", got)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

```bash
cd services/cli && go test ./internal/ui/...
```

Expected: build error — `StateStyle` undefined.

- [ ] **Step 4: Implement theme**

Create `services/cli/internal/ui/theme.go`:

```go
// Package ui holds presentational primitives shared by all commands.
package ui

import "github.com/charmbracelet/lipgloss"

// Palette. Tuned for red-team / blue-team / synthesis brand.
var (
	ColorRed    = lipgloss.Color("#FF3B3B")
	ColorBlue   = lipgloss.Color("#2D8CFF")
	ColorPurple = lipgloss.Color("#B14AED")
	ColorGreen  = lipgloss.Color("#3DD68C")
	ColorAmber  = lipgloss.Color("#F2A93B")
	ColorGrey   = lipgloss.Color("#6E7681")
)

// Re-usable styles.
var (
	BrandStyle    = lipgloss.NewStyle().Foreground(ColorPurple).Bold(true)
	OKStyle       = lipgloss.NewStyle().Foreground(ColorGreen).Bold(true)
	WarnStyle     = lipgloss.NewStyle().Foreground(ColorAmber).Bold(true)
	ErrStyle      = lipgloss.NewStyle().Foreground(ColorRed).Bold(true)
	InfoStyle     = lipgloss.NewStyle().Foreground(ColorBlue)
	DimStyle      = lipgloss.NewStyle().Foreground(ColorGrey)
	BoxStyle      = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
)

// StateStyle picks a style by scan state string ("RUNNING", "COMPLETED", …).
// Unknown states fall back to InfoStyle.
func StateStyle(state string) lipgloss.Style {
	switch state {
	case "COMPLETED":
		return OKStyle
	case "FAILED", "CANCELLED":
		return ErrStyle
	case "PARTIAL":
		return WarnStyle
	case "RUNNING", "PENDING":
		return InfoStyle
	}
	return InfoStyle
}
```

- [ ] **Step 5: Write the failing icons test**

Create `services/cli/internal/ui/icons_test.go`:

```go
package ui

import "testing"

func TestIconsUnicode(t *testing.T) {
	set := NewIcons(false)
	if set.OK != "✓" || set.Fail != "✗" || set.Run != "⚡" {
		t.Fatalf("unicode set: %+v", set)
	}
}

func TestIconsAsciiFallback(t *testing.T) {
	set := NewIcons(true)
	if set.OK != "[+]" || set.Fail != "[-]" || set.Run != "[*]" {
		t.Fatalf("ascii set: %+v", set)
	}
}
```

- [ ] **Step 6: Implement icons**

Create `services/cli/internal/ui/icons.go`:

```go
package ui

// Icons is the per-render icon set. Switched between Unicode and ASCII
// based on the active output profile.
type Icons struct {
	OK     string
	Fail   string
	Run    string
	Shield string
	Step   string
	Bullet string
	Rule   string
}

// NewIcons returns the appropriate set. asciiOnly=true is for dumb
// terminals (NO_COLOR or termenv.Ascii profile).
func NewIcons(asciiOnly bool) Icons {
	if asciiOnly {
		return Icons{
			OK:     "[+]",
			Fail:   "[-]",
			Run:    "[*]",
			Shield: "[#]",
			Step:   "->",
			Bullet: "o",
			Rule:   "-",
		}
	}
	return Icons{
		OK:     "✓",
		Fail:   "✗",
		Run:    "⚡",
		Shield: "🛡",
		Step:   "▸",
		Bullet: "●",
		Rule:   "─",
	}
}
```

- [ ] **Step 7: Write the failing banner test**

Create `services/cli/internal/ui/banner_test.go`:

```go
package ui

import (
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestBannerASCIIGolden(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.Ascii) })

	got := Banner("v1.0.0", "v1", "nats://localhost:4222")
	want, err := os.ReadFile("testdata/banner.golden")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if got != string(want) {
		t.Fatalf("banner mismatch.\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestBannerContainsDynamicFields(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	got := Banner("v9.9.9", "v2", "nats://example:4222")
	for _, want := range []string{"v9.9.9", "v2", "nats://example:4222", "HARPORIS"} {
		if !strings.Contains(got, want) {
			t.Errorf("banner missing %q", want)
		}
	}
}
```

- [ ] **Step 8: Implement banner**

Create `services/cli/internal/ui/banner.go`:

```go
package ui

import (
	"fmt"
	"strings"
)

const wordmark = `     ██╗  ██╗ █████╗ ██████╗ ██████╗  ██████╗ ██████╗ ██╗███████╗
     ██║  ██║██╔══██╗██╔══██╗██╔══██╗██╔═══██╗██╔══██╗██║██╔════╝
     ███████║███████║██████╔╝██████╔╝██║   ██║██████╔╝██║███████╗
     ██╔══██║██╔══██║██╔══██╗██╔═══╝ ██║   ██║██╔══██╗██║╚════██║
     ██║  ██║██║  ██║██║  ██║██║     ╚██████╔╝██║  ██║██║███████║
     ╚═╝  ╚═╝╚═╝  ╚═╝╚═╝  ╚═╝╚═╝      ╚═════╝ ╚═╝  ╚═╝╚═╝╚══════╝`

// Banner renders the brand wordmark and a dynamic bottom bar with
// version, proto contract version, and the configured NATS URL.
func Banner(version, proto, natsURL string) string {
	tagline := "git-aware secret hunter ▸ red ∙ blue ∙ purple"
	bottom := fmt.Sprintf("%s · proto %s · %s", version, proto, natsURL)
	var b strings.Builder
	b.WriteString(BrandStyle.Render(wordmark))
	b.WriteString("\n")
	b.WriteString(DimStyle.Render("          " + tagline))
	b.WriteString("\n")
	b.WriteString(DimStyle.Render("          " + bottom))
	b.WriteString("\n")
	return b.String()
}
```

- [ ] **Step 9: Generate the golden file**

Run from `services/cli/`:

```bash
mkdir -p internal/ui/testdata
go run ./cmd/harporis version > /dev/null  # warm modules
go test ./internal/ui/... -run TestBannerContainsDynamicFields -v
# now write the golden by running the renderer once and capturing output
cat > /tmp/gen_banner.go <<'EOF'
package main

import (
	"fmt"
	"os"

	"github.com/Harporis/harporis/services/cli/internal/ui"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func main() {
	lipgloss.SetColorProfile(termenv.Ascii)
	fmt.Print(ui.Banner("v1.0.0", "v1", "nats://localhost:4222"))
	_ = os.Stdout.Sync()
}
EOF
go run /tmp/gen_banner.go > internal/ui/testdata/banner.golden
rm /tmp/gen_banner.go
```

- [ ] **Step 10: Verify all UI tests pass**

```bash
cd services/cli && go test ./internal/ui/... -v
```

Expected: all PASS (theme, icons, banner golden, banner dynamic).

- [ ] **Step 11: Wire banner into `harporis` no-args and into `harporis version`**

Edit `services/cli/internal/cmd/root.go` — add a `Run` to root that prints the banner when called with no subcommand:

```go
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:          "harporis",
		Short:        "git-aware secret hunter — operator CLI",
		SilenceUsage: true,
		Run: func(cmd *cobra.Command, _ []string) {
			quiet, _ := cmd.Flags().GetBool("quiet")
			if !quiet {
				natsURL, _ := cmd.Flags().GetString("nats")
				fmt.Fprint(cmd.OutOrStdout(), ui.Banner(version.Version, version.ProtoVersion, natsURL))
			}
			_ = cmd.Help()
		},
	}
	// … existing flags + AddCommand calls …
}
```

Add `import "fmt"` and the ui + version package imports. Edit `version.go` similarly to print the banner above the version line.

- [ ] **Step 12: Verify visually**

```bash
cd services/cli && make build && ./bin/harporis && ./bin/harporis version
```

Expected: banner appears with bottom-bar values.

- [ ] **Step 13: Commit**

```bash
git add services/cli/internal/ui/ services/cli/internal/cmd/root.go services/cli/internal/cmd/version.go services/cli/go.mod services/cli/go.sum
git commit -m "feat(cli): ui primitives — theme, icons, ASCII banner with golden tests"
```

---

## Task 3: `natscli` wrapper around `kit/nats/wire`

**Files:**
- Create: `services/cli/internal/natscli/client.go`
- Create: `services/cli/internal/natscli/client_test.go`

- [ ] **Step 1: Write the failing test**

Create `services/cli/internal/natscli/client_test.go`:

```go
package natscli

import (
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats-server/v2/test"
)

func runJetstream(t *testing.T) *natsserver.Server {
	t.Helper()
	opts := test.DefaultTestOptions
	opts.Port = -1
	opts.JetStream = true
	opts.StoreDir = t.TempDir()
	s := test.RunServer(&opts)
	t.Cleanup(s.Shutdown)
	return s
}

func TestDialAndEnsureStreams(t *testing.T) {
	srv := runJetstream(t)
	cl, err := Dial(srv.ClientURL(), "test-cli")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer cl.Close()

	if err := cl.EnsureStreams(); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	// Round-trip ping.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cl.NC.IsConnected() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("nats not connected within deadline")
}
```

- [ ] **Step 2: Add replace directives + nats-server test dep**

Append to `services/cli/go.mod`:

```go
require (
	github.com/Harporis/harporis/contracts v0.0.0
	github.com/Harporis/harporis/kit v0.0.0
)

replace (
	github.com/Harporis/harporis/contracts => ../../contracts
	github.com/Harporis/harporis/kit       => ../../kit
)
```

Then:

```bash
cd services/cli
go get github.com/nats-io/nats-server/v2@latest
go get github.com/nats-io/nats.go@latest
go mod tidy
```

`go mod tidy` won't strip the `replace` because the require lines pin local modules.

- [ ] **Step 3: Run test to verify it fails**

```bash
cd services/cli && go test ./internal/natscli/...
```

Expected: build error — `natscli` package undefined.

- [ ] **Step 4: Implement the wrapper**

Create `services/cli/internal/natscli/client.go`:

```go
// Package natscli is a thin CLI-side wrapper around kit/nats/wire that
// also owns NATS-specific niceties (default URL, consumer-name
// sanitization) so command files stay focused on UX.
package natscli

import (
	"strings"

	"github.com/Harporis/harporis/kit/nats/wire"
)

// Client wraps wire.Client and exposes helpers for cli use.
type Client struct{ *wire.Client }

// Dial connects to NATS and returns a Client.
func Dial(url, clientName string) (*Client, error) {
	c, err := wire.Dial(wire.DialConfig{URL: url, ClientName: clientName})
	if err != nil {
		return nil, err
	}
	return &Client{Client: c}, nil
}

// EnsureStreams creates the four canonical streams if they don't exist.
func (c *Client) EnsureStreams() error { return wire.EnsureStreams(c.JS) }

// SanitizeConsumerName makes any scan-id safe to use as a JetStream
// consumer name (alphanumeric, dash, underscore only).
func SanitizeConsumerName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z',
			r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}
```

- [ ] **Step 5: Run test to verify it passes**

```bash
cd services/cli && go test ./internal/natscli/... -v
```

Expected: `PASS: TestDialAndEnsureStreams`.

- [ ] **Step 6: Commit**

```bash
git add services/cli/internal/natscli/ services/cli/go.mod services/cli/go.sum
git commit -m "feat(cli): natscli wrapper over kit/nats/wire with sanitize helper"
```

---

## Task 4: `scan` command

**Files:**
- Create: `services/cli/internal/cmd/scan.go`
- Create: `services/cli/internal/cmd/scan_source.go`
- Create: `services/cli/internal/cmd/scan_source_test.go`
- Create: `services/cli/integration_test.go`
- Modify: `services/cli/internal/cmd/root.go` (register scan)

- [ ] **Step 1: Add proto + uuid deps**

```bash
cd services/cli
go get github.com/google/uuid@latest
go get google.golang.org/protobuf@latest
go mod tidy
```

- [ ] **Step 2: Write the failing unit test for source builder**

Create `services/cli/internal/cmd/scan_source_test.go`:

```go
package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildSourceLocal(t *testing.T) {
	s, err := buildSource("/repos/demo", "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := s.GetLocalPath(); got != "/repos/demo" {
		t.Fatalf("local: %s", got)
	}
}

func TestBuildSourceMutualExclusion(t *testing.T) {
	if _, err := buildSource("/x", "https://y", "", "", ""); err == nil {
		t.Fatal("expected error on local + remote")
	}
}

func TestBuildSourceRemoteToken(t *testing.T) {
	s, err := buildSource("", "https://github.com/x/y.git", "ghp_xxx", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if s.GetRemote().GetToken() != "ghp_xxx" {
		t.Fatalf("token not set")
	}
}

func TestBuildSourceRemoteSSH(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(key, []byte("PEM-DATA"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := buildSource("", "git@github.com:x/y.git", "", key, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.GetRemote().GetSsh().GetPrivateKeyPem(), "PEM-DATA") {
		t.Fatal("ssh key not loaded")
	}
}

func TestScanTypeFromString(t *testing.T) {
	cases := map[string]bool{
		"current_state": true, "full_history": true, "branch_full": true,
		"commit_range": true, "branch_diff": true, "head_diff": true, "staged": true,
		"bogus": false,
	}
	for in, ok := range cases {
		_, got := scanTypeFromString(in)
		if got != ok {
			t.Errorf("%s: want %v got %v", in, ok, got)
		}
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

```bash
cd services/cli && go test ./internal/cmd/...
```

Expected: build error — `buildSource` / `scanTypeFromString` undefined.

- [ ] **Step 4: Implement source helpers**

Create `services/cli/internal/cmd/scan_source.go`:

```go
package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
)

func scanTypeFromString(s string) (v1.ScanType, bool) {
	switch strings.ToLower(s) {
	case "current_state":
		return v1.ScanType_CURRENT_STATE, true
	case "full_history":
		return v1.ScanType_FULL_HISTORY, true
	case "branch_full":
		return v1.ScanType_BRANCH_FULL, true
	case "commit_range":
		return v1.ScanType_COMMIT_RANGE, true
	case "branch_diff":
		return v1.ScanType_BRANCH_DIFF, true
	case "head_diff":
		return v1.ScanType_HEAD_DIFF, true
	case "staged":
		return v1.ScanType_STAGED, true
	}
	return 0, false
}

func buildSource(local, remoteURL, token, sshKey, knownHosts string) (*v1.Source, error) {
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
	switch {
	case token != "" && sshKey != "":
		return nil, errors.New("--remote-token and --remote-ssh-key are mutually exclusive")
	case token != "":
		rr.Auth = &v1.RemoteRepo_Token{Token: token}
	case sshKey != "":
		key, err := os.ReadFile(sshKey)
		if err != nil {
			return nil, fmt.Errorf("read ssh key %s: %w", sshKey, err)
		}
		ssh := &v1.SshAuth{PrivateKeyPem: string(key)}
		if knownHosts != "" {
			kh, err := os.ReadFile(knownHosts)
			if err != nil {
				return nil, fmt.Errorf("read known_hosts %s: %w", knownHosts, err)
			}
			ssh.KnownHosts = string(kh)
		}
		rr.Auth = &v1.RemoteRepo_Ssh{Ssh: ssh}
	}
	return &v1.Source{Src: &v1.Source_Remote{Remote: rr}}, nil
}
```

- [ ] **Step 5: Verify unit tests pass**

```bash
cd services/cli && go test ./internal/cmd/... -run TestBuildSource -v
cd services/cli && go test ./internal/cmd/... -run TestScanTypeFromString -v
```

Expected: PASS.

- [ ] **Step 6: Implement scan command (line-based output, no TUI yet)**

Create `services/cli/internal/cmd/scan.go`:

```go
package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/kit/nats/wire"
	"github.com/Harporis/harporis/services/cli/internal/natscli"
)

func newScanCmd() *cobra.Command {
	var (
		scanID, scanType, local, remoteURL                string
		token, sshKey, knownHosts                         string
		branch, baseBranch, commitFrom, commitTo          string
		noWait                                            bool
		idleTimeout                                       time.Duration
	)
	c := &cobra.Command{
		Use:   "scan",
		Short: "submit a scan request to NATS (waits for terminal state by default)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if scanID == "" {
				scanID = uuid.NewString()
			}
			typ, ok := scanTypeFromString(scanType)
			if !ok {
				return fmt.Errorf("invalid --type %q", scanType)
			}
			src, err := buildSource(local, remoteURL, token, sshKey, knownHosts)
			if err != nil {
				return err
			}
			req := &v1.ScanRequest{ScanId: scanID, Type: typ, Source: src}
			if branch != "" || baseBranch != "" || commitFrom != "" || commitTo != "" {
				req.Range = &v1.ScanRange{
					Branch: branch, BaseBranch: baseBranch,
					CommitFrom: commitFrom, CommitTo: commitTo,
				}
			}

			natsURL, _ := cmd.Root().PersistentFlags().GetString("nats")
			cl, err := natscli.Dial(natsURL, "harporis-cli")
			if err != nil {
				return fmt.Errorf("nats dial: %w", err)
			}
			defer cl.Close()
			if err := cl.EnsureStreams(); err != nil {
				return fmt.Errorf("ensure streams: %w", err)
			}
			data, err := proto.Marshal(req)
			if err != nil {
				return err
			}
			if _, err := cl.JS.Publish(wire.ScansRequestsSubject, data); err != nil {
				return fmt.Errorf("publish: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "submitted scan_id=%s type=%s\n", req.ScanId, typ.String())
			if noWait {
				return nil
			}
			return streamStatusLines(cmd.OutOrStdout(), cl, req.ScanId, idleTimeout)
		},
	}
	c.Flags().StringVar(&scanID, "scan-id", "", "scan id (default: generated UUID)")
	c.Flags().StringVar(&scanType, "type", "current_state", "scan type: current_state|full_history|branch_full|commit_range|branch_diff|head_diff|staged")
	c.Flags().StringVar(&local, "local", "", "local repo path (inside the getter container/host)")
	c.Flags().StringVar(&remoteURL, "remote-url", "", "remote repo URL (https:// or git@host:repo.git)")
	c.Flags().StringVar(&token, "remote-token", "", "Bearer / PAT token for HTTPS remotes")
	c.Flags().StringVar(&sshKey, "remote-ssh-key", "", "path to SSH private key file (PEM)")
	c.Flags().StringVar(&knownHosts, "remote-known-hosts", "", "path to known_hosts file")
	c.Flags().StringVar(&branch, "branch", "", "branch name (branch_full / branch_diff)")
	c.Flags().StringVar(&baseBranch, "base-branch", "", "base branch (branch_diff)")
	c.Flags().StringVar(&commitFrom, "from", "", "commit from (commit_range, exclusive)")
	c.Flags().StringVar(&commitTo, "to", "", "commit to (commit_range, inclusive)")
	c.Flags().BoolVar(&noWait, "no-wait", false, "do not block on status events; submit and return")
	c.Flags().DurationVar(&idleTimeout, "timeout", 30*time.Minute, "give up if no status events arrive for this long")
	return c
}

// streamStatusLines is the simple line-based status watcher used by `scan`
// when --no-wait is off and when bubble-tea is unavailable. `watch` also
// uses this as its fallback.
func streamStatusLines(out interface{ Write(p []byte) (int, error) }, cl *natscli.Client, scanID string, idle time.Duration) error {
	// Implementation moved to watch.go in Task 6 — for now, stub:
	_ = out
	_ = cl
	_ = scanID
	_ = idle
	_ = os.Stderr
	return nil
}
```

- [ ] **Step 7: Register scan in root**

Edit `services/cli/internal/cmd/root.go` — inside `NewRootCmd`, add after `root.AddCommand(newVersionCmd())`:

```go
	root.AddCommand(newScanCmd())
```

- [ ] **Step 8: Write the failing integration test**

Create `services/cli/integration_test.go`:

```go
//go:build integration

package cli_test

import (
	"context"
	"os/exec"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats-server/v2/test"
	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/kit/nats/wire"
)

func runJSServer(t *testing.T) *natsserver.Server {
	t.Helper()
	opts := test.DefaultTestOptions
	opts.Port = -1
	opts.JetStream = true
	opts.StoreDir = t.TempDir()
	s := test.RunServer(&opts)
	t.Cleanup(s.Shutdown)
	return s
}

func buildBinary(t *testing.T) string {
	t.Helper()
	bin := t.TempDir() + "/harporis"
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/harporis")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

func TestScanPublishesRequest(t *testing.T) {
	srv := runJSServer(t)
	bin := buildBinary(t)

	nc, err := nats.Connect(srv.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, err := nc.JetStream()
	if err != nil {
		t.Fatal(err)
	}
	if err := wire.EnsureStreams(js); err != nil {
		t.Fatal(err)
	}
	sub, err := js.PullSubscribe(wire.ScansRequestsSubject, "ittest", nats.BindStream(wire.RequestsStream))
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin,
		"--nats", srv.ClientURL(),
		"scan",
		"--local", "/repos/demo",
		"--scan-id", "it-1",
		"--no-wait",
	).CombinedOutput()
	if err != nil {
		t.Fatalf("scan exec: %v\n%s", err, out)
	}

	msgs, err := sub.Fetch(1, nats.MaxWait(2*time.Second))
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	var req v1.ScanRequest
	if err := proto.Unmarshal(msgs[0].Data, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.ScanId != "it-1" || req.GetSource().GetLocalPath() != "/repos/demo" {
		t.Fatalf("unexpected: %+v", &req)
	}
}
```

- [ ] **Step 9: Run integration test**

```bash
cd services/cli && go test -tags integration -run TestScanPublishesRequest -v
```

Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add services/cli/internal/cmd/scan.go services/cli/internal/cmd/scan_source.go services/cli/internal/cmd/scan_source_test.go services/cli/internal/cmd/root.go services/cli/integration_test.go services/cli/go.mod services/cli/go.sum
git commit -m "feat(cli): scan command with source builder, integration test"
```

---

## Task 5: `cancel` command

**Files:**
- Create: `services/cli/internal/cmd/cancel.go`
- Modify: `services/cli/internal/cmd/root.go`
- Modify: `services/cli/integration_test.go` (add cancel test)

- [ ] **Step 1: Write the failing integration test**

Append to `services/cli/integration_test.go`:

```go
func TestCancelPublishesRequest(t *testing.T) {
	srv := runJSServer(t)
	bin := buildBinary(t)

	nc, err := nats.Connect(srv.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	sub, err := nc.SubscribeSync(wire.ScansCancelSubject)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin,
		"--nats", srv.ClientURL(),
		"cancel", "scan-xyz",
		"--reason", "operator changed mind",
	).CombinedOutput()
	if err != nil {
		t.Fatalf("cancel exec: %v\n%s", err, out)
	}

	msg, err := sub.NextMsg(2 * time.Second)
	if err != nil {
		t.Fatalf("recv: %v", err)
	}
	var req v1.CancelScanRequest
	if err := proto.Unmarshal(msg.Data, &req); err != nil {
		t.Fatal(err)
	}
	if req.ScanId != "scan-xyz" || req.Reason != "operator changed mind" {
		t.Fatalf("unexpected: %+v", &req)
	}
}
```

- [ ] **Step 2: Run integration test to verify it fails**

```bash
cd services/cli && go test -tags integration -run TestCancelPublishesRequest -v
```

Expected: fail — `unknown command "cancel"`.

- [ ] **Step 3: Implement cancel command**

Create `services/cli/internal/cmd/cancel.go`:

```go
package cmd

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/kit/nats/wire"
	"github.com/Harporis/harporis/services/cli/internal/natscli"
)

func newCancelCmd() *cobra.Command {
	var reason string
	c := &cobra.Command{
		Use:   "cancel <scan-id>",
		Short: "ask the getter to cancel an active scan",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			scanID := args[0]
			if scanID == "" {
				return errors.New("scan-id is required")
			}
			natsURL, _ := cmd.Root().PersistentFlags().GetString("nats")
			cl, err := natscli.Dial(natsURL, "harporis-cli-cancel")
			if err != nil {
				return fmt.Errorf("nats dial: %w", err)
			}
			defer cl.Close()

			data, err := proto.Marshal(&v1.CancelScanRequest{ScanId: scanID, Reason: reason})
			if err != nil {
				return err
			}
			if err := cl.NC.Publish(wire.ScansCancelSubject, data); err != nil {
				return fmt.Errorf("publish cancel: %w", err)
			}
			if err := cl.NC.Flush(); err != nil {
				return fmt.Errorf("flush: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "cancel sent scan_id=%s reason=%q\n", scanID, reason)
			return nil
		},
	}
	c.Flags().StringVar(&reason, "reason", "operator cancelled", "free-form reason shown in the final status event")
	return c
}
```

- [ ] **Step 4: Register cancel in root**

Edit `services/cli/internal/cmd/root.go`, add after scan:

```go
	root.AddCommand(newCancelCmd())
```

- [ ] **Step 5: Run integration test to verify it passes**

```bash
cd services/cli && go test -tags integration -run TestCancelPublishesRequest -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add services/cli/internal/cmd/cancel.go services/cli/internal/cmd/root.go services/cli/integration_test.go
git commit -m "feat(cli): cancel command"
```

---

## Task 6: `watch` command (line-based, used as TUI fallback too)

**Files:**
- Create: `services/cli/internal/cmd/watch.go`
- Modify: `services/cli/internal/cmd/scan.go` (replace stub `streamStatusLines`)
- Modify: `services/cli/internal/cmd/root.go`
- Modify: `services/cli/integration_test.go`

- [ ] **Step 1: Write the failing integration test**

Append to `services/cli/integration_test.go`:

```go
func TestWatchReceivesTerminalStatus(t *testing.T) {
	srv := runJSServer(t)
	bin := buildBinary(t)

	nc, err := nats.Connect(srv.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, err := nc.JetStream()
	if err != nil {
		t.Fatal(err)
	}
	if err := wire.EnsureStreams(js); err != nil {
		t.Fatal(err)
	}

	scanID := "watch-it-1"
	// Publish a terminal status event slightly delayed.
	go func() {
		time.Sleep(300 * time.Millisecond)
		data, _ := proto.Marshal(&v1.StatusEvent{
			ScanId:    scanID,
			State:     v1.ScanState_COMPLETED,
			Message:   "scan finished",
			Timestamp: time.Now().Unix(),
		})
		_, _ = js.Publish(wire.StatusSubject(scanID), data)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin,
		"--nats", srv.ClientURL(),
		"--json",                  // forces line-based output
		"watch", scanID,
	).CombinedOutput()
	if err != nil {
		t.Fatalf("watch exec: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "COMPLETED") {
		t.Fatalf("watch output missing COMPLETED:\n%s", out)
	}
}
```

Add `"strings"` import to the file if not present.

- [ ] **Step 2: Implement watch helper and command**

Replace the stub in `services/cli/internal/cmd/scan.go` — remove the local `streamStatusLines` function entirely.

Create `services/cli/internal/cmd/watch.go`:

```go
package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	natsclient "github.com/nats-io/nats.go"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/kit/nats/wire"
	"github.com/Harporis/harporis/services/cli/internal/natscli"
	"github.com/Harporis/harporis/services/cli/internal/ui"
)

func newWatchCmd() *cobra.Command {
	var idle time.Duration
	c := &cobra.Command{
		Use:   "watch <scan-id>",
		Short: "follow status events for a scan",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			scanID := args[0]
			natsURL, _ := cmd.Root().PersistentFlags().GetString("nats")
			cl, err := natscli.Dial(natsURL, "harporis-cli-watch")
			if err != nil {
				return fmt.Errorf("nats dial: %w", err)
			}
			defer cl.Close()
			if err := cl.EnsureStreams(); err != nil {
				return fmt.Errorf("ensure streams: %w", err)
			}
			return StreamStatusLines(cmd.OutOrStdout(), cl, scanID, idle)
		},
	}
	c.Flags().DurationVar(&idle, "timeout", 30*time.Minute, "give up if no status events arrive for this long")
	return c
}

// StreamStatusLines is the line-based status follower. Used as the
// fallback when TUI is not appropriate (--json, non-tty) and also by
// `scan` after submission when --no-wait is off (Task 7 swaps `scan`
// onto the TUI when isatty; this remains the fallback).
func StreamStatusLines(out io.Writer, cl *natscli.Client, scanID string, idleTimeout time.Duration) error {
	consumer := "cli-watch-" + natscli.SanitizeConsumerName(scanID)
	sub, err := cl.JS.PullSubscribe(wire.StatusSubject(scanID), consumer,
		natsclient.BindStream(wire.StatusStream))
	if err != nil {
		return fmt.Errorf("subscribe status: %w", err)
	}
	defer func() {
		_ = sub.Unsubscribe()
		_ = cl.JS.DeleteConsumer(wire.StatusStream, consumer)
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	lastSeen := time.Now()
	for ctx.Err() == nil {
		if time.Since(lastSeen) > idleTimeout {
			return fmt.Errorf("idle timeout (%s) — no status events for %s", idleTimeout, scanID)
		}
		msgs, err := sub.Fetch(8, natsclient.MaxWait(2*time.Second))
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, natsclient.ErrTimeout) {
				continue
			}
			return fmt.Errorf("watch fetch: %w", err)
		}
		for _, m := range msgs {
			lastSeen = time.Now()
			var ev v1.StatusEvent
			if err := proto.Unmarshal(m.Data, &ev); err != nil {
				fmt.Fprintf(out, "watch unmarshal: %v\n", err)
				_ = m.Ack()
				continue
			}
			printStatusLine(out, &ev)
			_ = m.Ack()
			if isTerminal(ev.State) {
				return terminalExitCode(ev.State)
			}
		}
	}
	return nil
}

func printStatusLine(out io.Writer, ev *v1.StatusEvent) {
	ts := time.Unix(ev.Timestamp, 0).UTC().Format(time.RFC3339)
	state := ui.StateStyle(ev.State.String()).Render(ev.State.String())
	m := ev.GetMetrics()
	fmt.Fprintf(out, "[%s] %-9s | %s | scanned=%d skipped=%d chunks=%d bytes=%d errors=%d\n",
		ts, state, ev.Message,
		m.GetBlobsScanned(), m.GetBlobsSkipped(),
		m.GetChunksPublished(), m.GetBytesPublished(), m.GetErrorsTotal())
}

func isTerminal(s v1.ScanState) bool {
	switch s {
	case v1.ScanState_COMPLETED, v1.ScanState_FAILED,
		v1.ScanState_CANCELLED, v1.ScanState_PARTIAL:
		return true
	}
	return false
}

// terminalExitCode returns a typed error encoding the exit code so the
// cobra layer can translate it. nil for success states.
func terminalExitCode(s v1.ScanState) error {
	switch s {
	case v1.ScanState_FAILED, v1.ScanState_CANCELLED:
		return &exitError{code: 3, msg: s.String()}
	}
	return nil
}

type exitError struct {
	code int
	msg  string
}

func (e *exitError) Error() string { return e.msg }
func (e *exitError) ExitCode() int { return e.code }
```

- [ ] **Step 3: Wire scan to use StreamStatusLines and exit codes**

Edit `services/cli/internal/cmd/scan.go`:

- Remove the in-file stub `streamStatusLines` (lowercase).
- Replace the `return streamStatusLines(...)` call with `return StreamStatusLines(cmd.OutOrStdout(), cl, req.ScanId, idleTimeout)`.

- [ ] **Step 4: Translate exit code from cobra Execute**

Edit `services/cli/internal/cmd/root.go`'s `Execute`:

```go
func Execute() {
	if err := NewRootCmd().Execute(); err != nil {
		var ex interface{ ExitCode() int }
		if errors.As(err, &ex) {
			os.Exit(ex.ExitCode())
		}
		os.Exit(1)
	}
}
```

Add `"errors"` import.

- [ ] **Step 5: Register watch in root**

Edit `services/cli/internal/cmd/root.go`, add:

```go
	root.AddCommand(newWatchCmd())
```

- [ ] **Step 6: Run all integration tests**

```bash
cd services/cli && go test -tags integration -v -timeout 60s
```

Expected: scan + cancel + watch tests all PASS.

- [ ] **Step 7: Run unit tests too (catch regressions)**

```bash
cd services/cli && go test ./... -race -timeout 90s
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add services/cli/internal/cmd/ services/cli/integration_test.go
git commit -m "feat(cli): watch command + line-based streamer reused by scan, typed exit codes"
```

---

## Task 7: Bubble Tea `watch` TUI

**Files:**
- Create: `services/cli/internal/tui/watch_model.go`
- Create: `services/cli/internal/tui/watch_model_test.go`
- Modify: `services/cli/internal/cmd/watch.go` (pick TUI when isatty + no --json)

- [ ] **Step 1: Add bubbletea + bubbles + go-isatty deps**

```bash
cd services/cli
go get github.com/charmbracelet/bubbletea@latest
go get github.com/charmbracelet/bubbles@latest
go get github.com/mattn/go-isatty@latest
go mod tidy
```

- [ ] **Step 2: Write the failing model tests**

Create `services/cli/internal/tui/watch_model_test.go`:

```go
package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
)

func TestWatchModelInitialView(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	m := NewWatchModel("scan-abc")
	out := m.View()
	if !strings.Contains(out, "scan-abc") {
		t.Fatalf("view missing scan id: %s", out)
	}
	if !strings.Contains(out, "PENDING") {
		t.Fatalf("view missing initial state: %s", out)
	}
}

func TestWatchModelTransitionsToRunning(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	m := NewWatchModel("scan-abc")
	mi, _ := m.Update(StatusEventMsg{Ev: &v1.StatusEvent{
		ScanId:  "scan-abc",
		State:   v1.ScanState_RUNNING,
		Message: "scan started",
	}})
	out := mi.View()
	if !strings.Contains(out, "RUNNING") {
		t.Fatalf("did not transition: %s", out)
	}
}

func TestWatchModelTerminalQuits(t *testing.T) {
	m := NewWatchModel("scan-abc")
	_, cmd := m.Update(StatusEventMsg{Ev: &v1.StatusEvent{
		ScanId: "scan-abc",
		State:  v1.ScanState_COMPLETED,
	}})
	if cmd == nil {
		t.Fatal("expected tea.Quit command on terminal state")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

```bash
cd services/cli && go test ./internal/tui/...
```

Expected: build error — `NewWatchModel` undefined.

- [ ] **Step 4: Implement the model**

Create `services/cli/internal/tui/watch_model.go`:

```go
// Package tui contains bubble tea models for live commands.
package tui

import (
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/services/cli/internal/ui"
)

// StatusEventMsg is delivered by the NATS pump (see watch.go RunTUI).
type StatusEventMsg struct{ Ev *v1.StatusEvent }

// SubscribeErrMsg is delivered on subscription/fetch failures so the
// model can render and quit cleanly.
type SubscribeErrMsg struct{ Err error }

// WatchModel is the live scan dashboard.
type WatchModel struct {
	scanID     string
	state      v1.ScanState
	message    string
	startedAt  time.Time
	lastEvent  time.Time
	source     string
	walker     progress.Model
	publish    progress.Model
	spinner    spinner.Model
	events     []*v1.StatusEvent
	done       bool
	exitCode   int
	width      int
}

// NewWatchModel creates a freshly-initialized model.
func NewWatchModel(scanID string) WatchModel {
	walker := progress.New(progress.WithDefaultGradient())
	pub := progress.New(progress.WithDefaultGradient())
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	return WatchModel{
		scanID:    scanID,
		state:     v1.ScanState_PENDING,
		startedAt: time.Now(),
		lastEvent: time.Now(),
		walker:    walker,
		publish:   pub,
		spinner:   sp,
	}
}

// Init satisfies tea.Model.
func (m WatchModel) Init() tea.Cmd { return m.spinner.Tick }

// Update advances the model.
func (m WatchModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = v.Width
		m.walker.Width = max(20, v.Width-30)
		m.publish.Width = max(20, v.Width-30)
		return m, nil
	case tea.KeyMsg:
		if v.String() == "ctrl+c" || v.String() == "q" {
			return m, tea.Quit
		}
		return m, nil
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case StatusEventMsg:
		m.state = v.Ev.State
		m.message = v.Ev.Message
		m.lastEvent = time.Now()
		if v.Ev.GetMetrics() != nil {
			// crude % of "expected" — we don't know totals up front, so
			// we use a moving target: bar reaches 100% only when scan
			// reports a terminal state with chunks > 0. Before that we
			// show 0-95% based on a log scale of metric growth.
			m.walker.SetPercent(scaledPct(v.Ev.GetMetrics().GetBlobsScanned()))
			m.publish.SetPercent(scaledPct(v.Ev.GetMetrics().GetChunksPublished()))
		}
		m.events = appendCap(m.events, v.Ev, 10)
		if isTerminal(v.Ev.State) {
			m.done = true
			if v.Ev.State == v1.ScanState_FAILED || v.Ev.State == v1.ScanState_CANCELLED {
				m.exitCode = 3
			}
			m.walker.SetPercent(1.0)
			m.publish.SetPercent(1.0)
			return m, tea.Quit
		}
		return m, nil
	case SubscribeErrMsg:
		m.message = "error: " + v.Err.Error()
		m.exitCode = 2
		return m, tea.Quit
	}
	return m, nil
}

// View renders the model.
func (m WatchModel) View() string {
	header := fmt.Sprintf("scan %s ── %s ── %s",
		m.scanID,
		ui.StateStyle(m.state.String()).Render(m.state.String()),
		time.Since(m.startedAt).Truncate(time.Second))
	body := fmt.Sprintf(
		"source   %s\nstate    %s %s\nwalker   %s\npublish  %s\n",
		ui.DimStyle.Render(m.source),
		m.spinner.View(),
		m.message,
		m.walker.View(),
		m.publish.View(),
	)
	log := "log\n"
	for _, e := range m.events {
		log += fmt.Sprintf("  %s  %-9s  %s\n",
			time.Unix(e.Timestamp, 0).UTC().Format("15:04:05"),
			e.State.String(), e.Message)
	}
	footer := ui.DimStyle.Render("ctrl+c stop · q quit")
	box := lipgloss.JoinVertical(lipgloss.Left, header, body, log, footer)
	if m.done {
		return ui.BoxStyle.Render(box) + "\n"
	}
	return ui.BoxStyle.Render(box)
}

// ExitCode returns the suggested exit code after the program quits.
func (m WatchModel) ExitCode() int { return m.exitCode }

func isTerminal(s v1.ScanState) bool {
	switch s {
	case v1.ScanState_COMPLETED, v1.ScanState_FAILED,
		v1.ScanState_CANCELLED, v1.ScanState_PARTIAL:
		return true
	}
	return false
}

func appendCap[T any](xs []T, x T, cap int) []T {
	xs = append(xs, x)
	if len(xs) > cap {
		xs = xs[len(xs)-cap:]
	}
	return xs
}

// scaledPct turns a raw count into a 0..0.95 progress percentage on a
// log curve. Terminal state always sets 1.0 (see Update).
func scaledPct(n uint64) float64 {
	if n == 0 {
		return 0
	}
	// 0.95 / (1 + log10(1 + n/100)) — flatter approach to 1.0.
	const cap = 0.95
	x := float64(n) / 100.0
	if x < 1 {
		return cap * x / (1 + x)
	}
	// Asymptote-ish to 0.95.
	return cap * (1 - 1/(1+x/10))
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
```

- [ ] **Step 5: Run model tests to verify they pass**

```bash
cd services/cli && go test ./internal/tui/... -v
```

Expected: 3 PASS.

- [ ] **Step 6: Wire `watch` to pick TUI when appropriate**

Edit `services/cli/internal/cmd/watch.go`. Replace the `RunE` body:

```go
		RunE: func(cmd *cobra.Command, args []string) error {
			scanID := args[0]
			natsURL, _ := cmd.Root().PersistentFlags().GetString("nats")
			jsonOut, _ := cmd.Root().PersistentFlags().GetBool("json")
			cl, err := natscli.Dial(natsURL, "harporis-cli-watch")
			if err != nil {
				return fmt.Errorf("nats dial: %w", err)
			}
			defer cl.Close()
			if err := cl.EnsureStreams(); err != nil {
				return fmt.Errorf("ensure streams: %w", err)
			}
			if !jsonOut && isatty.IsTerminal(os.Stdout.Fd()) {
				return RunWatchTUI(cl, scanID, idle)
			}
			return StreamStatusLines(cmd.OutOrStdout(), cl, scanID, idle)
		},
```

Add imports: `"github.com/mattn/go-isatty"`.

- [ ] **Step 7: Add `RunWatchTUI` to wire NATS pump into the bubble tea program**

Append to `services/cli/internal/cmd/watch.go`:

```go
// RunWatchTUI runs the bubble tea watch panel until terminal state or
// ctrl+c. Returns nil on success, a typed *exitError on FAILED/CANCELLED.
func RunWatchTUI(cl *natscli.Client, scanID string, idle time.Duration) error {
	consumer := "cli-watch-" + natscli.SanitizeConsumerName(scanID)
	sub, err := cl.JS.PullSubscribe(wire.StatusSubject(scanID), consumer,
		natsclient.BindStream(wire.StatusStream))
	if err != nil {
		return fmt.Errorf("subscribe status: %w", err)
	}
	defer func() {
		_ = sub.Unsubscribe()
		_ = cl.JS.DeleteConsumer(wire.StatusStream, consumer)
	}()

	p := tea.NewProgram(tui.NewWatchModel(scanID), tea.WithAltScreen())
	// pump events into the program in the background
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	go func() {
		lastSeen := time.Now()
		for ctx.Err() == nil {
			if time.Since(lastSeen) > idle {
				p.Send(tui.SubscribeErrMsg{Err: fmt.Errorf("idle timeout %s", idle)})
				return
			}
			msgs, err := sub.Fetch(8, natsclient.MaxWait(2*time.Second))
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, natsclient.ErrTimeout) {
					continue
				}
				p.Send(tui.SubscribeErrMsg{Err: err})
				return
			}
			for _, m := range msgs {
				lastSeen = time.Now()
				var ev v1.StatusEvent
				if err := proto.Unmarshal(m.Data, &ev); err != nil {
					_ = m.Ack()
					continue
				}
				p.Send(tui.StatusEventMsg{Ev: &ev})
				_ = m.Ack()
			}
		}
	}()

	finalModel, err := p.Run()
	if err != nil {
		return err
	}
	if wm, ok := finalModel.(tui.WatchModel); ok && wm.ExitCode() != 0 {
		return &exitError{code: wm.ExitCode(), msg: "scan terminal state non-zero"}
	}
	return nil
}
```

Add new imports as needed: `tea "github.com/charmbracelet/bubbletea"`, `"github.com/Harporis/harporis/services/cli/internal/tui"`.

- [ ] **Step 8: Run all tests**

```bash
cd services/cli && go test ./... -race -timeout 90s
cd services/cli && go test -tags integration -v -timeout 60s
```

Expected: all PASS. The integration `TestWatchReceivesTerminalStatus` already uses `--json` so it stays on the line-based path.

- [ ] **Step 9: Manual visual check**

```bash
cd services/cli && make build
# in one shell:
./bin/harporis --nats nats://localhost:4222 watch some-fake-id
# (will hang waiting; ctrl+c to exit) — verify the dashboard appears
```

- [ ] **Step 10: Commit**

```bash
git add services/cli/internal/tui/ services/cli/internal/cmd/watch.go services/cli/go.mod services/cli/go.sum
git commit -m "feat(cli): bubble tea live dashboard for watch (tty + non-json)"
```

---

## Task 8: `history` command (list + show)

**Files:**
- Create: `services/cli/internal/natscli/history.go`
- Create: `services/cli/internal/natscli/history_test.go`
- Create: `services/cli/internal/ui/table.go`
- Create: `services/cli/internal/cmd/history.go`
- Modify: `services/cli/internal/cmd/root.go`

- [ ] **Step 1: Write the failing history reader test**

Create `services/cli/internal/natscli/history_test.go`:

```go
package natscli

import (
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/kit/nats/wire"
)

func TestListHistoryLastEventPerScan(t *testing.T) {
	srv := runJetstream(t)
	cl, err := Dial(srv.ClientURL(), "history-test")
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	if err := cl.EnsureStreams(); err != nil {
		t.Fatal(err)
	}

	pub := func(id string, state v1.ScanState, ts int64) {
		data, _ := proto.Marshal(&v1.StatusEvent{
			ScanId: id, State: state, Timestamp: ts, Message: "msg",
		})
		_, err := cl.JS.Publish(wire.StatusSubject(id), data, nats.MsgId(id+":"+state.String()))
		if err != nil {
			t.Fatal(err)
		}
	}

	pub("a", v1.ScanState_RUNNING, 100)
	pub("a", v1.ScanState_COMPLETED, 200)
	pub("b", v1.ScanState_FAILED, 150)

	got, err := cl.ListHistory(5, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 scans, got %d", len(got))
	}
	for _, e := range got {
		switch e.ScanId {
		case "a":
			if e.State != v1.ScanState_COMPLETED {
				t.Errorf("a state %s", e.State)
			}
		case "b":
			if e.State != v1.ScanState_FAILED {
				t.Errorf("b state %s", e.State)
			}
		default:
			t.Errorf("unexpected scan %q", e.ScanId)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd services/cli && go test ./internal/natscli/... -run TestListHistory
```

Expected: `ListHistory` undefined.

- [ ] **Step 3: Implement ListHistory + ShowHistory**

Create `services/cli/internal/natscli/history.go`:

```go
package natscli

import (
	"errors"
	"fmt"
	"time"

	natsclient "github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/kit/nats/wire"
)

// ListHistory walks the status stream and returns the latest status
// event per scan id, newest-first. `maxScans` caps the returned slice.
// `wait` is a per-fetch deadline; it does not bound the total time.
func (c *Client) ListHistory(maxScans int, wait time.Duration) ([]*v1.StatusEvent, error) {
	consumer := "cli-history-" + fmt.Sprintf("%d", time.Now().UnixNano())
	sub, err := c.JS.PullSubscribe("harporis.status.>", consumer,
		natsclient.BindStream(wire.StatusStream),
		natsclient.DeliverAll(),
		natsclient.OrderedConsumer())
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = sub.Unsubscribe()
		_ = c.JS.DeleteConsumer(wire.StatusStream, consumer)
	}()

	latest := map[string]*v1.StatusEvent{}
	for {
		msgs, err := sub.Fetch(64, natsclient.MaxWait(wait))
		if err != nil {
			if errors.Is(err, natsclient.ErrTimeout) {
				break
			}
			return nil, err
		}
		for _, m := range msgs {
			var ev v1.StatusEvent
			if err := proto.Unmarshal(m.Data, &ev); err != nil {
				continue
			}
			prev, ok := latest[ev.ScanId]
			if !ok || ev.Timestamp >= prev.Timestamp {
				latest[ev.ScanId] = &ev
			}
		}
		if len(msgs) < 64 {
			break
		}
	}
	out := make([]*v1.StatusEvent, 0, len(latest))
	for _, ev := range latest {
		out = append(out, ev)
	}
	// newest first
	sortByTimestampDesc(out)
	if maxScans > 0 && len(out) > maxScans {
		out = out[:maxScans]
	}
	return out, nil
}

// ShowHistory returns every status event for a single scan, oldest-first.
func (c *Client) ShowHistory(scanID string, wait time.Duration) ([]*v1.StatusEvent, error) {
	consumer := "cli-history-show-" + SanitizeConsumerName(scanID) + "-" + fmt.Sprintf("%d", time.Now().UnixNano())
	sub, err := c.JS.PullSubscribe(wire.StatusSubject(scanID), consumer,
		natsclient.BindStream(wire.StatusStream),
		natsclient.DeliverAll(),
		natsclient.OrderedConsumer())
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = sub.Unsubscribe()
		_ = c.JS.DeleteConsumer(wire.StatusStream, consumer)
	}()

	var out []*v1.StatusEvent
	for {
		msgs, err := sub.Fetch(64, natsclient.MaxWait(wait))
		if err != nil {
			if errors.Is(err, natsclient.ErrTimeout) {
				break
			}
			return nil, err
		}
		for _, m := range msgs {
			var ev v1.StatusEvent
			if err := proto.Unmarshal(m.Data, &ev); err == nil {
				out = append(out, &ev)
			}
		}
		if len(msgs) < 64 {
			break
		}
	}
	sortByTimestampAsc(out)
	return out, nil
}

func sortByTimestampDesc(xs []*v1.StatusEvent) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j].Timestamp > xs[j-1].Timestamp; j-- {
			xs[j], xs[j-1] = xs[j-1], xs[j]
		}
	}
}
func sortByTimestampAsc(xs []*v1.StatusEvent) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j].Timestamp < xs[j-1].Timestamp; j-- {
			xs[j], xs[j-1] = xs[j-1], xs[j]
		}
	}
}
```

- [ ] **Step 4: Verify history reader test passes**

```bash
cd services/cli && go test ./internal/natscli/... -run TestListHistory -v
```

Expected: PASS.

- [ ] **Step 5: Implement table helper**

Create `services/cli/internal/ui/table.go`:

```go
package ui

import (
	"fmt"
	"io"
	"strings"
)

// Table is a minimal column writer. We don't need lipgloss tables — these
// rows are short and we want pipe-friendly output.
type Table struct {
	headers []string
	rows    [][]string
}

// NewTable creates a table with the given column headers.
func NewTable(headers ...string) *Table { return &Table{headers: headers} }

// Row adds a row. Excess columns are dropped; missing ones get "".
func (t *Table) Row(cols ...string) {
	row := make([]string, len(t.headers))
	for i := range row {
		if i < len(cols) {
			row[i] = cols[i]
		}
	}
	t.rows = append(t.rows, row)
}

// WriteTo renders the table.
func (t *Table) WriteTo(w io.Writer) (int64, error) {
	widths := make([]int, len(t.headers))
	for i, h := range t.headers {
		widths[i] = len(h)
	}
	for _, r := range t.rows {
		for i, c := range r {
			if len(c) > widths[i] {
				widths[i] = len(c)
			}
		}
	}
	var b strings.Builder
	for i, h := range t.headers {
		if i > 0 {
			b.WriteString("  ")
		}
		b.WriteString(DimStyle.Render(padRight(h, widths[i])))
	}
	b.WriteString("\n")
	for _, r := range t.rows {
		for i, c := range r {
			if i > 0 {
				b.WriteString("  ")
			}
			b.WriteString(padRight(c, widths[i]))
		}
		b.WriteString("\n")
	}
	n, err := fmt.Fprint(w, b.String())
	return int64(n), err
}

func padRight(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}
```

- [ ] **Step 6: Implement history command**

Create `services/cli/internal/cmd/history.go`:

```go
package cmd

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/services/cli/internal/natscli"
	"github.com/Harporis/harporis/services/cli/internal/ui"
)

func newHistoryCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "history",
		Short: "list past scans (latest state per scan from the status stream)",
	}
	c.AddCommand(newHistoryListCmd())
	c.AddCommand(newHistoryShowCmd())
	// `harporis history` with no sub = list with defaults
	c.RunE = newHistoryListCmd().RunE
	return c
}

func newHistoryListCmd() *cobra.Command {
	var limit int
	c := &cobra.Command{
		Use:   "list",
		Short: "list past scans (newest first)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			natsURL, _ := cmd.Root().PersistentFlags().GetString("nats")
			cl, err := natscli.Dial(natsURL, "harporis-cli-history")
			if err != nil {
				return fmt.Errorf("nats dial: %w", err)
			}
			defer cl.Close()
			if err := cl.EnsureStreams(); err != nil {
				return fmt.Errorf("ensure streams: %w", err)
			}
			evs, err := cl.ListHistory(limit, 2*time.Second)
			if err != nil {
				return err
			}
			if len(evs) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), ui.DimStyle.Render("(no scans found in status stream)"))
				return nil
			}
			t := ui.NewTable("SCAN_ID", "STATE", "UPDATED", "CHUNKS", "BYTES", "ERRORS")
			for _, e := range evs {
				m := e.GetMetrics()
				t.Row(
					e.ScanId,
					ui.StateStyle(e.State.String()).Render(e.State.String()),
					time.Unix(e.Timestamp, 0).UTC().Format(time.RFC3339),
					fmt.Sprintf("%d", m.GetChunksPublished()),
					fmt.Sprintf("%d", m.GetBytesPublished()),
					fmt.Sprintf("%d", m.GetErrorsTotal()),
				)
			}
			_, err = t.WriteTo(cmd.OutOrStdout())
			return err
		},
	}
	c.Flags().IntVar(&limit, "limit", 25, "max scans to list")
	return c
}

func newHistoryShowCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "show <scan-id>",
		Short: "print the full status timeline of one scan",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			natsURL, _ := cmd.Root().PersistentFlags().GetString("nats")
			cl, err := natscli.Dial(natsURL, "harporis-cli-history-show")
			if err != nil {
				return err
			}
			defer cl.Close()
			evs, err := cl.ShowHistory(args[0], 2*time.Second)
			if err != nil {
				return err
			}
			if len(evs) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), ui.DimStyle.Render("(no events found for "+args[0]+")"))
				return nil
			}
			for _, e := range evs {
				printStatusLine(cmd.OutOrStdout(), e)
			}
			_ = v1.ScanState_PENDING // pull v1 into scope for build stability if unused
			return nil
		},
	}
	return c
}
```

- [ ] **Step 7: Register history**

Edit `services/cli/internal/cmd/root.go`, add:

```go
	root.AddCommand(newHistoryCmd())
```

- [ ] **Step 8: Run tests**

```bash
cd services/cli && go test ./... -race -timeout 90s
```

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add services/cli/internal/natscli/ services/cli/internal/ui/table.go services/cli/internal/cmd/history.go services/cli/internal/cmd/root.go
git commit -m "feat(cli): history list/show from JetStream status stream"
```

---

## Task 9: Compose wrapper + `up`/`down`/`ps`/`logs` (line-based)

**Files:**
- Create: `services/cli/internal/compose/compose.go`
- Create: `services/cli/internal/compose/compose_test.go`
- Create: `services/cli/internal/cmd/up.go`
- Create: `services/cli/internal/cmd/down.go`
- Create: `services/cli/internal/cmd/ps.go`
- Create: `services/cli/internal/cmd/logs.go`
- Modify: `services/cli/internal/cmd/root.go`

- [ ] **Step 1: Write the failing compose test**

Create `services/cli/internal/compose/compose_test.go`:

```go
package compose

import (
	"context"
	"strings"
	"testing"
)

type fakeRunner struct {
	calls [][]string
	out   string
	err   error
}

func (f *fakeRunner) Run(_ context.Context, args ...string) (string, error) {
	f.calls = append(f.calls, args)
	return f.out, f.err
}

func TestUpDetached(t *testing.T) {
	r := &fakeRunner{out: "ok"}
	c := New(r)
	if _, err := c.Up(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if !equalSlice(r.calls[0], []string{"up", "-d"}) {
		t.Fatalf("got %v", r.calls[0])
	}
}

func TestUpWithBuild(t *testing.T) {
	r := &fakeRunner{out: "ok"}
	c := New(r)
	if _, err := c.Up(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if !equalSlice(r.calls[0], []string{"up", "-d", "--build"}) {
		t.Fatalf("got %v", r.calls[0])
	}
}

func TestPSPassesPSCommand(t *testing.T) {
	r := &fakeRunner{out: "Name State"}
	c := New(r)
	out, _ := c.PS(context.Background())
	if !strings.Contains(out, "Name") {
		t.Fatal("output not surfaced")
	}
	if !equalSlice(r.calls[0], []string{"ps"}) {
		t.Fatalf("got %v", r.calls[0])
	}
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd services/cli && go test ./internal/compose/...
```

Expected: build error.

- [ ] **Step 3: Implement compose wrapper**

Create `services/cli/internal/compose/compose.go`:

```go
// Package compose is a thin wrapper around `docker compose` (or, as a
// fallback, the legacy `docker-compose` binary). Commands are dispatched
// through a Runner interface so tests can stub them.
package compose

import (
	"context"
	"errors"
	"os/exec"
	"strings"
)

// Runner executes one compose invocation. Production uses ExecRunner;
// tests use a stub.
type Runner interface {
	Run(ctx context.Context, args ...string) (string, error)
}

// Compose is the high-level surface used by cli commands.
type Compose struct{ r Runner }

// New wraps any Runner.
func New(r Runner) *Compose { return &Compose{r: r} }

// NewDefault picks the right docker compose flavour at runtime and
// returns a wired Compose.
func NewDefault() (*Compose, error) {
	r, err := DetectExecRunner()
	if err != nil {
		return nil, err
	}
	return New(r), nil
}

// Up is `docker compose up -d`, optionally with --build.
func (c *Compose) Up(ctx context.Context, build bool) (string, error) {
	args := []string{"up", "-d"}
	if build {
		args = append(args, "--build")
	}
	return c.r.Run(ctx, args...)
}

// Down is `docker compose down`, optionally with -v.
func (c *Compose) Down(ctx context.Context, volumes bool) (string, error) {
	args := []string{"down"}
	if volumes {
		args = append(args, "-v")
	}
	return c.r.Run(ctx, args...)
}

// PS is `docker compose ps`.
func (c *Compose) PS(ctx context.Context) (string, error) {
	return c.r.Run(ctx, "ps")
}

// Logs is `docker compose logs <svc?> [-f]`.
func (c *Compose) Logs(ctx context.Context, service string, follow bool) (string, error) {
	args := []string{"logs"}
	if follow {
		args = append(args, "-f")
	}
	if service != "" {
		args = append(args, service)
	}
	return c.r.Run(ctx, args...)
}

// ExecRunner runs `docker compose …` via os/exec.
type ExecRunner struct {
	binary []string // e.g. {"docker","compose"} or {"docker-compose"}
}

// DetectExecRunner finds either `docker compose` or `docker-compose`.
func DetectExecRunner() (*ExecRunner, error) {
	if _, err := exec.LookPath("docker"); err == nil {
		// Verify subcommand exists.
		if err := exec.Command("docker", "compose", "version").Run(); err == nil {
			return &ExecRunner{binary: []string{"docker", "compose"}}, nil
		}
	}
	if _, err := exec.LookPath("docker-compose"); err == nil {
		return &ExecRunner{binary: []string{"docker-compose"}}, nil
	}
	return nil, errors.New("docker compose v2 not found (neither `docker compose` nor `docker-compose`)")
}

// Run executes the compose command and returns merged stdout/stderr.
func (e *ExecRunner) Run(ctx context.Context, args ...string) (string, error) {
	full := append(append([]string{}, e.binary[1:]...), args...)
	cmd := exec.CommandContext(ctx, e.binary[0], full...)
	out, err := cmd.CombinedOutput()
	return strings.TrimRight(string(out), "\n"), err
}
```

- [ ] **Step 4: Verify compose tests pass**

```bash
cd services/cli && go test ./internal/compose/... -v
```

Expected: 3 PASS.

- [ ] **Step 5: Implement up command (line-based; TUI in Task 10)**

Create `services/cli/internal/cmd/up.go`:

```go
package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Harporis/harporis/services/cli/internal/compose"
	"github.com/Harporis/harporis/services/cli/internal/ui"
)

func newUpCmd() *cobra.Command {
	var build bool
	return &cobra.Command{
		Use:   "up",
		Short: "start the stack (docker compose up -d)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cp, err := compose.NewDefault()
			if err != nil {
				return err
			}
			out, err := cp.Up(context.Background(), build)
			fmt.Fprintln(cmd.OutOrStdout(), out)
			if err != nil {
				return fmt.Errorf("compose up failed: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), ui.OKStyle.Render("stack started"))
			return nil
		},
	}
}
```

(`build` flag wired in next:)

Update the function:

```go
func newUpCmd() *cobra.Command {
	var build bool
	c := &cobra.Command{
		Use:   "up",
		Short: "start the stack (docker compose up -d)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cp, err := compose.NewDefault()
			if err != nil {
				return err
			}
			out, err := cp.Up(context.Background(), build)
			fmt.Fprintln(cmd.OutOrStdout(), out)
			if err != nil {
				return fmt.Errorf("compose up failed: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), ui.OKStyle.Render("stack started"))
			return nil
		},
	}
	c.Flags().BoolVar(&build, "build", false, "rebuild images before starting")
	return c
}
```

- [ ] **Step 6: Implement down, ps, logs**

Create `services/cli/internal/cmd/down.go`:

```go
package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Harporis/harporis/services/cli/internal/compose"
)

func newDownCmd() *cobra.Command {
	var vols bool
	c := &cobra.Command{
		Use:   "down",
		Short: "stop the stack (docker compose down)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cp, err := compose.NewDefault()
			if err != nil {
				return err
			}
			out, err := cp.Down(context.Background(), vols)
			fmt.Fprintln(cmd.OutOrStdout(), out)
			return err
		},
	}
	c.Flags().BoolVarP(&vols, "volumes", "v", false, "also remove named volumes")
	return c
}
```

Create `services/cli/internal/cmd/ps.go`:

```go
package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Harporis/harporis/services/cli/internal/compose"
)

func newPSCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ps",
		Short: "show stack container status",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cp, err := compose.NewDefault()
			if err != nil {
				return err
			}
			out, err := cp.PS(context.Background())
			fmt.Fprintln(cmd.OutOrStdout(), out)
			return err
		},
	}
}
```

Create `services/cli/internal/cmd/logs.go`:

```go
package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Harporis/harporis/services/cli/internal/compose"
)

func newLogsCmd() *cobra.Command {
	var follow bool
	c := &cobra.Command{
		Use:   "logs [service]",
		Short: "stream container logs",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cp, err := compose.NewDefault()
			if err != nil {
				return err
			}
			svc := ""
			if len(args) == 1 {
				svc = args[0]
			}
			out, err := cp.Logs(context.Background(), svc, follow)
			fmt.Fprintln(cmd.OutOrStdout(), out)
			return err
		},
	}
	c.Flags().BoolVarP(&follow, "follow", "f", false, "follow log output")
	return c
}
```

- [ ] **Step 7: Register all four in root**

Edit `services/cli/internal/cmd/root.go`, add:

```go
	root.AddCommand(newUpCmd())
	root.AddCommand(newDownCmd())
	root.AddCommand(newPSCmd())
	root.AddCommand(newLogsCmd())
```

- [ ] **Step 8: Verify build and unit tests**

```bash
cd services/cli && go test ./... -race -timeout 90s
cd services/cli && make build && ./bin/harporis --help
```

Expected: PASS. Help shows up/down/ps/logs.

- [ ] **Step 9: Commit**

```bash
git add services/cli/internal/compose/ services/cli/internal/cmd/{up,down,ps,logs}.go services/cli/internal/cmd/root.go
git commit -m "feat(cli): compose wrapper + up/down/ps/logs commands"
```

---

## Task 10: Bubble Tea `up` checklist TUI

**Files:**
- Create: `services/cli/internal/tui/up_model.go`
- Create: `services/cli/internal/tui/up_model_test.go`
- Modify: `services/cli/internal/cmd/up.go` (TUI when tty + not --quiet)

- [ ] **Step 1: Write the failing model test**

Create `services/cli/internal/tui/up_model_test.go`:

```go
package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestUpModelStepProgression(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	steps := []string{"compose up", "nats container", "nats healthz", "getter container", "getter health"}
	m := NewUpModel(steps)
	out := m.View()
	for _, s := range steps {
		if !strings.Contains(out, s) {
			t.Errorf("initial view missing step %q", s)
		}
	}
	mi, _ := m.Update(StepDoneMsg{Index: 0, OK: true, Took: "1.2s"})
	if !strings.Contains(mi.View(), "1.2s") {
		t.Fatal("step done not reflected")
	}
}

func TestUpModelFailedStepQuits(t *testing.T) {
	m := NewUpModel([]string{"a", "b"})
	_, cmd := m.Update(StepDoneMsg{Index: 0, OK: false, Took: "0s", Err: "boom"})
	if cmd == nil {
		t.Fatal("expected tea.Quit on failed step")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd services/cli && go test ./internal/tui/... -run TestUpModel
```

Expected: `NewUpModel` undefined.

- [ ] **Step 3: Implement up model**

Create `services/cli/internal/tui/up_model.go`:

```go
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Harporis/harporis/services/cli/internal/ui"
)

// StepDoneMsg is sent by the orchestrator when a step resolves.
type StepDoneMsg struct {
	Index int
	OK    bool
	Took  string // "1.2s"
	Err   string // populated when OK=false
}

type stepStatus int

const (
	stepPending stepStatus = iota
	stepRunning
	stepDone
	stepFailed
)

// UpModel is the live startup checklist.
type UpModel struct {
	steps    []string
	status   []stepStatus
	took     []string
	errs     []string
	spinner  spinner.Model
	current  int
	finished bool
}

// NewUpModel creates the checklist with all steps pending and step 0 running.
func NewUpModel(steps []string) UpModel {
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	status := make([]stepStatus, len(steps))
	if len(steps) > 0 {
		status[0] = stepRunning
	}
	return UpModel{
		steps:   steps,
		status:  status,
		took:    make([]string, len(steps)),
		errs:    make([]string, len(steps)),
		spinner: sp,
	}
}

// Init starts the spinner.
func (m UpModel) Init() tea.Cmd { return m.spinner.Tick }

// Update handles step transitions.
func (m UpModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.KeyMsg:
		if v.String() == "ctrl+c" {
			return m, tea.Quit
		}
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case StepDoneMsg:
		if v.Index < 0 || v.Index >= len(m.steps) {
			return m, nil
		}
		m.took[v.Index] = v.Took
		if v.OK {
			m.status[v.Index] = stepDone
		} else {
			m.status[v.Index] = stepFailed
			m.errs[v.Index] = v.Err
			m.finished = true
			return m, tea.Quit
		}
		if v.Index+1 < len(m.steps) {
			m.status[v.Index+1] = stepRunning
			m.current = v.Index + 1
		} else {
			m.finished = true
			return m, tea.Quit
		}
	}
	return m, nil
}

// View renders the checklist.
func (m UpModel) View() string {
	icons := ui.NewIcons(false)
	var b strings.Builder
	b.WriteString("starting stack…\n")
	for i, name := range m.steps {
		marker := "○"
		extra := ""
		switch m.status[i] {
		case stepDone:
			marker = ui.OKStyle.Render(icons.OK)
			extra = ui.DimStyle.Render(" (" + m.took[i] + ")")
		case stepFailed:
			marker = ui.ErrStyle.Render(icons.Fail)
			extra = ui.ErrStyle.Render(" — " + m.errs[i])
		case stepRunning:
			marker = ui.WarnStyle.Render(m.spinner.View())
		}
		b.WriteString(fmt.Sprintf(" %s %s%s\n", marker, name, extra))
	}
	if !m.finished {
		b.WriteString(ui.DimStyle.Render("\n ctrl+c abort\n"))
	}
	return b.String()
}
```

- [ ] **Step 4: Verify tests pass**

```bash
cd services/cli && go test ./internal/tui/... -run TestUpModel -v
```

Expected: PASS.

- [ ] **Step 5: Wire `up` to use TUI when tty**

Replace `services/cli/internal/cmd/up.go` with:

```go
package cmd

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/Harporis/harporis/services/cli/internal/compose"
	"github.com/Harporis/harporis/services/cli/internal/natscli"
	"github.com/Harporis/harporis/services/cli/internal/tui"
	"github.com/Harporis/harporis/services/cli/internal/ui"
	"github.com/Harporis/harporis/services/cli/internal/version"
)

func newUpCmd() *cobra.Command {
	var build bool
	c := &cobra.Command{
		Use:   "up",
		Short: "start the stack (docker compose up -d) and wait for health",
		RunE: func(cmd *cobra.Command, _ []string) error {
			quiet, _ := cmd.Root().PersistentFlags().GetBool("quiet")
			natsURL, _ := cmd.Root().PersistentFlags().GetString("nats")
			useTUI := !quiet && isatty.IsTerminal(os.Stdout.Fd())

			cp, err := compose.NewDefault()
			if err != nil {
				return err
			}
			steps := []string{
				"docker compose up",
				"NATS container started",
				"NATS /healthz",
				"getter container",
				"getter NATS connection",
			}
			if useTUI {
				fmt.Fprint(cmd.OutOrStdout(), ui.Banner(version.Version, version.ProtoVersion, natsURL))
			}
			runner := func(p tui.StepNotifier) {
				started := time.Now()
				_, err := cp.Up(context.Background(), build)
				p.Done(0, err == nil, took(started), errStr(err))
				if err != nil {
					return
				}
				p.Done(1, true, "0.2s", "")
				started = time.Now()
				err = waitHTTPOK("http://localhost:8222/healthz", 30*time.Second)
				p.Done(2, err == nil, took(started), errStr(err))
				if err != nil {
					return
				}
				p.Done(3, true, "0.2s", "")
				started = time.Now()
				err = waitNATSReachable(natsURL, 15*time.Second)
				p.Done(4, err == nil, took(started), errStr(err))
			}
			if useTUI {
				model := tui.NewUpModel(steps)
				p := tea.NewProgram(model)
				go runner(programNotifier{p: p})
				if _, err := p.Run(); err != nil {
					return err
				}
				return nil
			}
			// non-tty fallback
			runner(stdoutNotifier{w: cmd.OutOrStdout(), steps: steps})
			return nil
		},
	}
	c.Flags().BoolVar(&build, "build", false, "rebuild images before starting")
	return c
}

type programNotifier struct{ p *tea.Program }

func (n programNotifier) Done(i int, ok bool, took, err string) {
	n.p.Send(tui.StepDoneMsg{Index: i, OK: ok, Took: took, Err: err})
}

type stdoutNotifier struct {
	w     interface{ Write(p []byte) (int, error) }
	steps []string
}

func (n stdoutNotifier) Done(i int, ok bool, took, err string) {
	mark := "[+]"
	if !ok {
		mark = "[-]"
	}
	fmt.Fprintf(n.w, "%s %s (%s) %s\n", mark, n.steps[i], took, err)
}

func took(t time.Time) string { return time.Since(t).Round(time.Millisecond).String() }

func errStr(e error) string {
	if e == nil {
		return ""
	}
	return e.Error()
}

func waitHTTPOK(url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for %s", url)
}

func waitNATSReachable(url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		cl, err := natscli.Dial(url, "harporis-cli-up")
		if err == nil {
			cl.Close()
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("timeout connecting to %s", url)
}
```

- [ ] **Step 6: Add the StepNotifier interface in tui**

Append to `services/cli/internal/tui/up_model.go`:

```go
// StepNotifier is the callback shape used by the up orchestrator to push
// step results into either the tea program or stdout fallback.
type StepNotifier interface {
	Done(index int, ok bool, took, err string)
}
```

- [ ] **Step 7: Run tests**

```bash
cd services/cli && go test ./... -race -timeout 90s
cd services/cli && go test ./internal/tui/...  -run TestUpModel -v
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add services/cli/internal/tui/up_model.go services/cli/internal/tui/up_model_test.go services/cli/internal/cmd/up.go
git commit -m "feat(cli): bubble tea checklist for harporis up + tty/non-tty paths"
```

---

## Task 11: `health` command

**Files:**
- Create: `services/cli/internal/cmd/health.go`
- Modify: `services/cli/internal/cmd/root.go`

- [ ] **Step 1: Implement health command**

Create `services/cli/internal/cmd/health.go`:

```go
package cmd

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"

	"github.com/Harporis/harporis/services/cli/internal/natscli"
	"github.com/Harporis/harporis/services/cli/internal/ui"
)

func newHealthCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "health",
		Short: "quick liveness check: NATS RTT + getter /metrics",
		RunE: func(cmd *cobra.Command, _ []string) error {
			natsURL, _ := cmd.Root().PersistentFlags().GetString("nats")
			t := ui.NewTable("COMPONENT", "STATUS", "DETAIL")

			start := time.Now()
			cl, err := natscli.Dial(natsURL, "harporis-cli-health")
			natsRTT := time.Since(start)
			if err != nil {
				t.Row("nats", ui.ErrStyle.Render("DOWN"), err.Error())
			} else {
				cl.Close()
				t.Row("nats", ui.OKStyle.Render("UP"), fmt.Sprintf("connect in %s", natsRTT.Round(time.Millisecond)))
			}

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost:9100/metrics", nil)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Row("getter /metrics", ui.ErrStyle.Render("DOWN"), err.Error())
			} else {
				resp.Body.Close()
				if resp.StatusCode == 200 {
					t.Row("getter /metrics", ui.OKStyle.Render("UP"), "HTTP 200")
				} else {
					t.Row("getter /metrics", ui.WarnStyle.Render("DEGRADED"), fmt.Sprintf("HTTP %d", resp.StatusCode))
				}
			}
			_, werr := t.WriteTo(cmd.OutOrStdout())
			return werr
		},
	}
}
```

- [ ] **Step 2: Register in root**

Edit `services/cli/internal/cmd/root.go`, add:

```go
	root.AddCommand(newHealthCmd())
```

- [ ] **Step 3: Verify build**

```bash
cd services/cli && make build && ./bin/harporis health
```

Expected: a 3-row table; rows may be DOWN if stack isn't running — that's fine.

- [ ] **Step 4: Commit**

```bash
git add services/cli/internal/cmd/health.go services/cli/internal/cmd/root.go
git commit -m "feat(cli): health command"
```

---

## Task 12: `doctor` command + checks framework

**Files:**
- Create: `services/cli/internal/doctor/checks.go`
- Create: `services/cli/internal/doctor/checks_test.go`
- Create: `services/cli/internal/cmd/doctor.go`
- Modify: `services/cli/internal/cmd/root.go`

- [ ] **Step 1: Write the failing checks test**

Create `services/cli/internal/doctor/checks_test.go`:

```go
package doctor

import "testing"

func TestRunAllCollectsResults(t *testing.T) {
	checks := []Check{
		StaticCheck("always-ok", true, "no detail"),
		StaticCheck("always-bad", false, "broken"),
	}
	results := RunAll(checks)
	if len(results) != 2 {
		t.Fatalf("got %d", len(results))
	}
	if results[0].OK != true || results[1].OK != false {
		t.Fatalf("unexpected: %+v", results)
	}
}
```

- [ ] **Step 2: Implement checks framework**

Create `services/cli/internal/doctor/checks.go`:

```go
// Package doctor defines diagnostic checks and a runner.
package doctor

import (
	"context"
	"errors"
	"net/http"
	"os/exec"
	"time"

	"github.com/Harporis/harporis/services/cli/internal/natscli"
)

// Check is a single diagnostic. Name is shown to the user; Run returns
// (ok, detail, err). detail is shown on both success and failure.
type Check interface {
	Name() string
	Run(ctx context.Context) Result
}

// Result is the outcome of one Check.
type Result struct {
	Name   string
	OK     bool
	Detail string
}

// RunAll invokes each check sequentially and returns their results.
func RunAll(checks []Check) []Result {
	out := make([]Result, 0, len(checks))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, c := range checks {
		out = append(out, c.Run(ctx))
	}
	return out
}

// StaticCheck returns a Check that always reports the given values.
// Used in tests.
func StaticCheck(name string, ok bool, detail string) Check {
	return staticCheck{name: name, ok: ok, detail: detail}
}

type staticCheck struct {
	name   string
	ok     bool
	detail string
}

func (s staticCheck) Name() string                     { return s.name }
func (s staticCheck) Run(_ context.Context) Result    { return Result{Name: s.name, OK: s.ok, Detail: s.detail} }

// ----- concrete checks used by `harporis doctor` -----

// DockerCheck verifies docker is installed and responsive.
type DockerCheck struct{}

func (DockerCheck) Name() string { return "docker" }
func (DockerCheck) Run(ctx context.Context) Result {
	cmd := exec.CommandContext(ctx, "docker", "version", "--format", "{{.Server.Version}}")
	out, err := cmd.Output()
	if err != nil {
		return Result{Name: "docker", OK: false, Detail: "docker not running or not installed"}
	}
	return Result{Name: "docker", OK: true, Detail: "server " + string(out)}
}

// ComposeCheck verifies `docker compose` v2 is available.
type ComposeCheck struct{}

func (ComposeCheck) Name() string { return "docker compose v2" }
func (ComposeCheck) Run(ctx context.Context) Result {
	cmd := exec.CommandContext(ctx, "docker", "compose", "version", "--short")
	out, err := cmd.Output()
	if err != nil {
		return Result{Name: "docker compose v2", OK: false, Detail: "missing or pre-v2"}
	}
	return Result{Name: "docker compose v2", OK: true, Detail: "v" + string(out)}
}

// NATSCheck pings the configured NATS URL.
type NATSCheck struct{ URL string }

func (n NATSCheck) Name() string { return "nats reachable" }
func (n NATSCheck) Run(_ context.Context) Result {
	cl, err := natscli.Dial(n.URL, "harporis-cli-doctor")
	if err != nil {
		return Result{Name: n.Name(), OK: false, Detail: err.Error()}
	}
	cl.Close()
	return Result{Name: n.Name(), OK: true, Detail: n.URL}
}

// GetterHealthCheck hits /metrics on localhost:9100.
type GetterHealthCheck struct{}

func (GetterHealthCheck) Name() string { return "getter /metrics" }
func (GetterHealthCheck) Run(ctx context.Context) Result {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost:9100/metrics", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Result{Name: "getter /metrics", OK: false, Detail: err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return Result{Name: "getter /metrics", OK: false, Detail: "non-200 status"}
	}
	return Result{Name: "getter /metrics", OK: true, Detail: "HTTP 200"}
}

var _ = errors.New // keep errors import if unused above
```

- [ ] **Step 3: Verify checks test passes**

```bash
cd services/cli && go test ./internal/doctor/... -v
```

Expected: PASS.

- [ ] **Step 4: Implement doctor command**

Create `services/cli/internal/cmd/doctor.go`:

```go
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Harporis/harporis/services/cli/internal/doctor"
	"github.com/Harporis/harporis/services/cli/internal/ui"
	"github.com/Harporis/harporis/services/cli/internal/version"
)

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "run environment checks and print a verdict",
		RunE: func(cmd *cobra.Command, _ []string) error {
			natsURL, _ := cmd.Root().PersistentFlags().GetString("nats")
			quiet, _ := cmd.Root().PersistentFlags().GetBool("quiet")
			if !quiet {
				fmt.Fprint(cmd.OutOrStdout(), ui.Banner(version.Version, version.ProtoVersion, natsURL))
			}
			checks := []doctor.Check{
				doctor.DockerCheck{},
				doctor.ComposeCheck{},
				doctor.NATSCheck{URL: natsURL},
				doctor.GetterHealthCheck{},
			}
			results := doctor.RunAll(checks)
			t := ui.NewTable("CHECK", "RESULT", "DETAIL")
			allOK := true
			for _, r := range results {
				badge := ui.OKStyle.Render("OK")
				if !r.OK {
					badge = ui.ErrStyle.Render("FAIL")
					allOK = false
				}
				t.Row(r.Name, badge, r.Detail)
			}
			if _, err := t.WriteTo(cmd.OutOrStdout()); err != nil {
				return err
			}
			if !allOK {
				return &exitError{code: 2, msg: "one or more doctor checks failed"}
			}
			return nil
		},
	}
}
```

- [ ] **Step 5: Register in root**

Edit `services/cli/internal/cmd/root.go`, add:

```go
	root.AddCommand(newDoctorCmd())
```

- [ ] **Step 6: Run tests + manual check**

```bash
cd services/cli && go test ./... -race -timeout 90s
cd services/cli && make build && ./bin/harporis doctor
```

Expected: tests PASS. Doctor prints banner + table; may be all-FAIL if stack is down — that's fine.

- [ ] **Step 7: Commit**

```bash
git add services/cli/internal/doctor/ services/cli/internal/cmd/doctor.go services/cli/internal/cmd/root.go
git commit -m "feat(cli): doctor command with pluggable checks framework"
```

---

## Task 13: `metrics` command

**Files:**
- Create: `services/cli/internal/cmd/metrics.go`
- Create: `services/cli/internal/cmd/metrics_test.go`
- Modify: `services/cli/internal/cmd/root.go`

- [ ] **Step 1: Write the failing test**

Create `services/cli/internal/cmd/metrics_test.go`:

```go
package cmd

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const fakeMetrics = `# HELP foo
foo_total 42
harporis_blobs_scanned 100
harporis_chunks_published 7
unrelated_metric 1
`

func TestFetchAndFilterPrintsMatches(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(fakeMetrics))
	}))
	defer srv.Close()

	var buf bytes.Buffer
	if err := fetchAndPrintMetrics(srv.URL, "^harporis_", &buf); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	for _, want := range []string{"harporis_blobs_scanned 100", "harporis_chunks_published 7"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "unrelated_metric") {
		t.Errorf("filter leaked unrelated: %s", got)
	}
}
```

- [ ] **Step 2: Implement metrics command**

Create `services/cli/internal/cmd/metrics.go`:

```go
package cmd

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"time"

	"github.com/spf13/cobra"
)

func newMetricsCmd() *cobra.Command {
	var (
		url    string
		filter string
		watch  bool
	)
	c := &cobra.Command{
		Use:   "metrics",
		Short: "fetch and filter the getter's Prometheus /metrics",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !watch {
				return fetchAndPrintMetrics(url, filter, cmd.OutOrStdout())
			}
			for {
				if err := fetchAndPrintMetrics(url, filter, cmd.OutOrStdout()); err != nil {
					fmt.Fprintln(cmd.ErrOrStderr(), err)
				}
				time.Sleep(2 * time.Second)
			}
		},
	}
	c.Flags().StringVar(&url, "url", "http://localhost:9100/metrics", "metrics endpoint URL")
	c.Flags().StringVar(&filter, "filter", "^harporis_", "regex applied to metric line; default: harporis_*")
	c.Flags().BoolVar(&watch, "watch", false, "refresh every 2 seconds")
	return c
}

func fetchAndPrintMetrics(url, filter string, w io.Writer) error {
	re, err := regexp.Compile(filter)
	if err != nil {
		return fmt.Errorf("bad --filter: %w", err)
	}
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("get %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("%s returned HTTP %d", url, resp.StatusCode)
	}
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || line[0] == '#' {
			continue
		}
		if re.MatchString(line) {
			fmt.Fprintln(w, line)
		}
	}
	return scanner.Err()
}
```

- [ ] **Step 3: Register in root**

Edit `services/cli/internal/cmd/root.go`, add:

```go
	root.AddCommand(newMetricsCmd())
```

- [ ] **Step 4: Run tests**

```bash
cd services/cli && go test ./internal/cmd/... -run TestFetchAndFilter -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add services/cli/internal/cmd/metrics.go services/cli/internal/cmd/metrics_test.go services/cli/internal/cmd/root.go
git commit -m "feat(cli): metrics command — fetch, filter, watch"
```

---

## Task 14: Optional config file (`~/.config/harporis/config.yaml`)

**Files:**
- Create: `services/cli/internal/config/config.go`
- Create: `services/cli/internal/config/config_test.go`
- Modify: `services/cli/internal/cmd/root.go`

- [ ] **Step 1: Add yaml dep**

```bash
cd services/cli && go get gopkg.in/yaml.v3@latest && go mod tidy
```

- [ ] **Step 2: Write the failing test**

Create `services/cli/internal/config/config_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaultsWhenMissing(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.NATSURL == "" {
		t.Fatal("default not applied")
	}
}

func TestLoadParsesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("nats_url: nats://example:4222\ncolor: never\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.NATSURL != "nats://example:4222" || c.Color != "never" {
		t.Fatalf("unexpected: %+v", c)
	}
}
```

- [ ] **Step 3: Implement loader**

Create `services/cli/internal/config/config.go`:

```go
// Package config loads the optional ~/.config/harporis/config.yaml file.
package config

import (
	"errors"
	"io/fs"
	"os"

	"gopkg.in/yaml.v3"
)

// Config is the on-disk shape.
type Config struct {
	NATSURL          string `yaml:"nats_url"`
	Color            string `yaml:"color"`             // auto|always|never
	DefaultScanType  string `yaml:"default_scan_type"` // current_state|...
}

// Defaults applied when nothing is set.
const (
	defaultNATSURL  = "nats://localhost:4222"
	defaultColor    = "auto"
	defaultScanType = "current_state"
)

// Load reads the config file. Missing file is not an error — defaults
// are returned. Returns the merged config.
func Load(path string) (Config, error) {
	c := Config{NATSURL: defaultNATSURL, Color: defaultColor, DefaultScanType: defaultScanType}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return c, nil
		}
		return c, err
	}
	if err := yaml.Unmarshal(data, &c); err != nil {
		return c, err
	}
	if c.NATSURL == "" {
		c.NATSURL = defaultNATSURL
	}
	if c.Color == "" {
		c.Color = defaultColor
	}
	if c.DefaultScanType == "" {
		c.DefaultScanType = defaultScanType
	}
	return c, nil
}

// DefaultPath returns ~/.config/harporis/config.yaml or "" if HOME unset.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home + "/.config/harporis/config.yaml"
}
```

- [ ] **Step 4: Wire config into root**

Edit `services/cli/internal/cmd/root.go`. Inside `NewRootCmd`, after the persistent flags are declared, add a `PersistentPreRunE`:

```go
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		cfgPath, _ := cmd.Flags().GetString("config")
		if cfgPath == "" {
			cfgPath = config.DefaultPath()
		}
		cfg, err := config.Load(cfgPath)
		if err != nil {
			return err
		}
		// Only override flag defaults when user did NOT pass them explicitly.
		if !cmd.PersistentFlags().Changed("nats") {
			_ = cmd.PersistentFlags().Set("nats", cfg.NATSURL)
		}
		return nil
	}
```

Add the `--config` flag:

```go
	root.PersistentFlags().String("config", "", "config file path (default: ~/.config/harporis/config.yaml)")
```

Add `"github.com/Harporis/harporis/services/cli/internal/config"` import.

- [ ] **Step 5: Run tests**

```bash
cd services/cli && go test ./... -race -timeout 90s
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add services/cli/internal/config/ services/cli/internal/cmd/root.go services/cli/go.mod services/cli/go.sum
git commit -m "feat(cli): optional config file (~/.config/harporis/config.yaml)"
```

---

## Task 15: Packaging — `make install`, `make deb`, completions

**Files:**
- Create: `services/cli/packaging/nfpm.yaml`
- Modify: `services/cli/Makefile`
- Create: `services/cli/internal/cmd/completion.go` (cobra `completion`)
- Modify: `services/cli/internal/cmd/root.go`

- [ ] **Step 1: Add completion command**

Create `services/cli/internal/cmd/completion.go`:

```go
package cmd

import "github.com/spf13/cobra"

// newCompletionCmd is the standard cobra completion generator wrapped
// with a deterministic name.
func newCompletionCmd(root *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:                   "completion [bash|zsh|fish|powershell]",
		Short:                 "generate shell completion script for harporis",
		DisableFlagsInUseLine: true,
		ValidArgs:             []string{"bash", "zsh", "fish", "powershell"},
		Args:                  cobra.ExactValidArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return root.GenBashCompletion(cmd.OutOrStdout())
			case "zsh":
				return root.GenZshCompletion(cmd.OutOrStdout())
			case "fish":
				return root.GenFishCompletion(cmd.OutOrStdout(), true)
			case "powershell":
				return root.GenPowerShellCompletion(cmd.OutOrStdout())
			}
			return nil
		},
	}
}
```

Edit `services/cli/internal/cmd/root.go` so the completion command receives the root:

```go
	root.AddCommand(newCompletionCmd(root))
```

- [ ] **Step 2: Verify completion command works**

```bash
cd services/cli && make build && ./bin/harporis completion bash | head -5
```

Expected: bash completion script.

- [ ] **Step 3: Create nfpm.yaml**

Create `services/cli/packaging/nfpm.yaml`:

```yaml
name: harporis
version: ${HARPORIS_VERSION}
section: utils
priority: optional
maintainer: Harporis Team <maintainers@harporis.local>
description: |
  Git-aware secret hunter — operator CLI.
  Submits scan requests to a Harporis NATS cluster, watches status events,
  manages the local docker compose stack, runs diagnostics.
vendor: Harporis
homepage: https://github.com/Harporis/harporis
license: Apache-2.0

contents:
  - src: ./bin/harporis
    dst: /usr/bin/harporis
    file_info:
      mode: 0755
  - src: ./completions/harporis.bash
    dst: /usr/share/bash-completion/completions/harporis
  - src: ./completions/_harporis
    dst: /usr/share/zsh/vendor-completions/_harporis
  - src: ./completions/harporis.fish
    dst: /usr/share/fish/vendor_completions.d/harporis.fish

deb:
  fields:
    Recommends: docker.io
```

- [ ] **Step 4: Add Makefile targets for `deb`, `completions`, and `install` polish**

Replace `services/cli/Makefile` with:

```make
.PHONY: build install uninstall completions deb rpm test test-integration lint clean

PREFIX        ?= /usr/local
BIN           := bin/harporis
PKG           := github.com/Harporis/harporis/services/cli
NFPM_VERSION  ?= 2.40.0
NFPM_BIN      := bin/nfpm

VERSION := $(shell git describe --tags --dirty --always 2>/dev/null || echo dev)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS := -s -w \
	-X '$(PKG)/internal/version.Version=$(VERSION)' \
	-X '$(PKG)/internal/version.Commit=$(COMMIT)' \
	-X '$(PKG)/internal/version.ProtoVersion=v1'

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/harporis

completions: build
	mkdir -p completions
	./$(BIN) completion bash       > completions/harporis.bash
	./$(BIN) completion zsh        > completions/_harporis
	./$(BIN) completion fish       > completions/harporis.fish

install: build completions
	install -d $(PREFIX)/bin
	install -m 0755 $(BIN) $(PREFIX)/bin/harporis
	install -d $(PREFIX)/share/bash-completion/completions
	install -m 0644 completions/harporis.bash $(PREFIX)/share/bash-completion/completions/harporis

uninstall:
	rm -f $(PREFIX)/bin/harporis $(PREFIX)/share/bash-completion/completions/harporis

$(NFPM_BIN):
	mkdir -p bin
	curl -sSL https://github.com/goreleaser/nfpm/releases/download/v$(NFPM_VERSION)/nfpm_$(NFPM_VERSION)_Linux_x86_64.tar.gz \
		| tar xz -C bin nfpm

deb: build completions $(NFPM_BIN)
	mkdir -p dist
	HARPORIS_VERSION=$(VERSION) $(NFPM_BIN) pkg --packager deb --config packaging/nfpm.yaml --target dist/

rpm: build completions $(NFPM_BIN)
	mkdir -p dist
	HARPORIS_VERSION=$(VERSION) $(NFPM_BIN) pkg --packager rpm --config packaging/nfpm.yaml --target dist/

test:
	go test ./... -race -timeout 90s

test-integration:
	go test ./... -race -timeout 180s -tags integration

lint:
	go vet ./...

clean:
	rm -rf bin/ dist/ completions/
```

- [ ] **Step 5: Verify `make install PREFIX=$(mktemp -d)` works**

```bash
cd services/cli
TMPDIR=$(mktemp -d)
make install PREFIX=$TMPDIR
test -x $TMPDIR/bin/harporis && echo OK
$TMPDIR/bin/harporis version
rm -rf $TMPDIR
```

Expected: `OK` printed and version line.

- [ ] **Step 6: Verify `make deb` builds a package**

```bash
cd services/cli && make deb && ls dist/*.deb
```

Expected: `dist/harporis_<version>_amd64.deb` exists.

- [ ] **Step 7: Commit**

```bash
git add services/cli/Makefile services/cli/packaging/ services/cli/internal/cmd/completion.go services/cli/internal/cmd/root.go
git commit -m "feat(cli): packaging — install/deb/rpm via nfpm + shell completions"
```

---

## Task 16: Migration — remove `getter-cli`, update docs, root Makefile

**Files:**
- Delete: `services/getter/cmd/getter-cli/`
- Modify: `services/getter/Makefile`
- Modify: `services/getter/QUICKSTART.md`
- Modify: `services/getter/README.md`
- Modify: `docker-compose.yml`
- Modify: `README.md` (root)
- Create: `Makefile` (root)
- Create: `services/cli/README.md`

- [ ] **Step 1: Delete getter-cli**

```bash
rm -rf services/getter/cmd/getter-cli
```

- [ ] **Step 2: Update getter Makefile**

Edit `services/getter/Makefile`. Remove the `build-cli` target and the `build-cli` dependency from `build`:

```make
.PHONY: build build-getter test lint run clean

build: build-getter

build-getter:
	go build -o bin/getter ./cmd/getter

test:
	go test ./... -race -timeout 90s

lint:
	go vet ./...

run: build-getter
	./bin/getter --config config/getter.yaml

clean:
	rm -rf bin/
```

- [ ] **Step 3: Update QUICKSTART.md**

Edit `services/getter/QUICKSTART.md`. Replace every occurrence of `./services/getter/bin/getter-cli` with `harporis` and the build-step `cd services/getter && make build-cli` with:

```bash
cd services/cli && make install   # or: sudo dpkg -i dist/harporis_*.deb
```

Also update the title section "Управлять getter'ом удобно через `getter-cli` …" to "Управлять getter'ом удобно через `harporis` CLI на хосте — это отдельный пакет в services/cli."

- [ ] **Step 4: Update getter README.md**

Edit `services/getter/README.md`. Search for "getter-cli" and "getter cli" and replace with "harporis CLI", linking to `../cli/README.md`.

- [ ] **Step 5: Update docker-compose.yml comments**

Edit `docker-compose.yml`. The header comment block currently mentions `./services/getter/bin/getter-cli scan`. Update to:

```yaml
#   2) submit a scan from the host:
#        harporis scan --local /repos/my-repo
#      (path is what the getter sees inside the container, NOT the host path).
```

- [ ] **Step 6: Write services/cli/README.md**

Create `services/cli/README.md`:

```markdown
# harporis CLI

Host-side operator CLI for the Harporis stack. Submits scan requests to
NATS, watches status events in a live dashboard, manages the local
docker compose stack, and runs environment diagnostics.

## Install

### From source (recommended for development)

```bash
git clone https://github.com/Harporis/harporis.git
cd harporis/services/cli
make install                  # /usr/local/bin/harporis (sudo)
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

## Quick start

```bash
harporis up                       # start the local stack
harporis doctor                   # verify environment
harporis scan --local /repos/demo # submit a scan, watch live
harporis history                  # see past scans
harporis down                     # stop the stack
```

## Commands

See `harporis --help` for the full tree. The interesting ones:

| Command       | What it does                                     |
|---------------|--------------------------------------------------|
| `scan`        | submit a `ScanRequest` to NATS, wait by default  |
| `cancel <id>` | publish a `CancelScanRequest`                    |
| `watch <id>`  | bubble tea live dashboard (line-based on non-tty)|
| `up`          | docker compose up + wait for health              |
| `down`        | docker compose down                              |
| `ps` / `logs` | passthrough to docker compose                    |
| `doctor`      | environment checks                               |
| `health`      | quick liveness probe                             |
| `metrics`     | fetch and filter the getter Prometheus output    |
| `history`     | list past scans / show one scan's timeline       |

## Global flags

| Flag         | Env         | Default                       |
|--------------|-------------|-------------------------------|
| `--nats`     | `NATS_URL`  | `nats://localhost:4222`       |
| `--no-color` | `NO_COLOR`  | auto (tty + termenv)          |
| `--json`     | —           | off                           |
| `--quiet,-q` | —           | off                           |
| `--config`   | —           | `~/.config/harporis/config.yaml` |

## Exit codes

| Code | Meaning                                |
|------|----------------------------------------|
| 0    | success                                |
| 1    | user / flag error                      |
| 2    | NATS unreachable / doctor failures     |
| 3    | scan terminated in FAILED / CANCELLED  |
| 124  | --timeout reached                      |
```

- [ ] **Step 7: Create root Makefile**

Create `Makefile` (in repo root):

```make
.PHONY: cli cli-install cli-deb cli-test stack-up stack-down getter-test all-test

cli:
	$(MAKE) -C services/cli build

cli-install:
	$(MAKE) -C services/cli install

cli-deb:
	$(MAKE) -C services/cli deb

cli-test:
	$(MAKE) -C services/cli test

getter-test:
	$(MAKE) -C services/getter test

all-test: cli-test getter-test

stack-up:
	docker compose up -d --build

stack-down:
	docker compose down
```

- [ ] **Step 8: Update root README.md**

Replace `README.md` content with:

```markdown
# Harporis

Git-aware secret hunter. A small set of services that consume git
repositories, normalize them into chunks, and (eventually) detect
secrets and other sensitive patterns at scale.

## Architecture

```
+-----------+        +-----------+        +-----------+
| harporis  | -----> |   NATS    | -----> |  getter   |
|   (CLI,   | <----- | (JetStream)| <----- | (container)|
|   host)   |        +-----------+        +-----------+
+-----------+
```

- `getter` (in container) — consumes `ScanRequest` from NATS, emits
  chunk + status events. See `services/getter/`.
- `nats` (in container) — JetStream message broker.
- `harporis` (on host) — operator CLI. See `services/cli/`.

## Quick start

```bash
make stack-up         # docker compose up -d (NATS + getter)
make cli-install      # install harporis CLI to /usr/local/bin

harporis doctor                   # verify environment
harporis scan --local /repos/demo # run a scan with live dashboard
harporis ps                       # check stack
```

For a hands-on walkthrough, see [`services/getter/QUICKSTART.md`](services/getter/QUICKSTART.md).

## Repo layout

| Path             | What                                              |
|------------------|---------------------------------------------------|
| `services/getter`| Git → NATS pipeline (server-side, containerized)  |
| `services/cli`   | `harporis` operator CLI (host-side)               |
| `services/scanner`| (planned) secret detection consumer              |
| `kit/`           | Cross-service Go primitives (`kit/nats/wire`)     |
| `contracts/`     | Proto definitions and generated Go                |

## License

Apache-2.0 — see [`LICENSE`](LICENSE).
```

- [ ] **Step 9: Run full test suite**

```bash
make all-test
```

Expected: PASS (getter unaffected, cli passes).

- [ ] **Step 10: Verify nothing references `getter-cli`**

```bash
grep -rn getter-cli services/ docker-compose.yml README.md
```

Expected: no matches (or only those in `.git/` history).

- [ ] **Step 11: Commit**

```bash
git add -A
git commit -m "refactor: replace getter-cli with harporis CLI; update docs"
```

---

## Self-Review

Spec coverage check against `docs/superpowers/specs/2026-05-25-harporis-cli-design.md`:

- **Architecture (services/cli/ separate module, NATS-only deps)** — Task 1 (bootstrap), Task 3 (natscli over kit/nats/wire). ✓
- **Command tree (scan/cancel/watch/up/down/ps/logs/health/history/metrics/doctor/version)** — Tasks 1, 4, 5, 6, 7, 9, 10, 11, 8, 13, 12 collectively cover all 12 commands. ✓
- **Global flags (--nats/--no-color/--json/--quiet/--config)** — Task 1 (--nats/--no-color/--json/--quiet), Task 14 (--config). ✓
- **Exit codes table (0/1/2/3/124)** — Task 6 introduces `exitError` and terminal-state codes (3); Task 12 uses code 2 for doctor failures. Code 1 is the cobra default. Code 124 is documented in README — implemented via the `--timeout` flag returning an error in Task 6 step 2 (`idle timeout`). ✓
- **Visual: palette/banner/icons/StateStyle** — Task 2. ✓
- **Bubble Tea watch panel** — Task 7. ✓
- **Bubble Tea up checklist** — Task 10. ✓
- **Plain commands stay pipeable** — `--json` flag is consumed by `watch` (Task 7) and `history` outputs raw tables. ✓
- **Install paths (make install / go install / .deb)** — Task 15. ✓
- **Shell completions and ldflags-injected version** — Task 1 (ldflags), Task 15 (completions). ✓
- **Migration: delete getter-cli, update Makefile/QUICKSTART/README/docker-compose** — Task 16. ✓
- **Tests: unit (UI/TUI/natscli/compose/doctor/config) + integration (build tag, embedded NATS) + e2e (manual)** — covered across Tasks 2, 3, 4, 7, 9, 10, 12, 14. Integration tests in Task 4, 5, 6. E2E is documented in spec; the Makefile already supports `test-integration` (Task 15). ✓
- **YAGNI list explicitly skipped (Snap/Brew/AUR/auto-updater/signed deb/auth)** — none of these are tasks. ✓

Open spec items intentionally deferred:
- **history via JetStream KV vs status stream walk** — Task 8 walks the status stream (the simpler option), matching the spec's "decide in implementation". If perf becomes an issue, the open question allows a follow-up.

Placeholder scan — no TBD/TODO; every step contains the code or command an engineer needs.

Type consistency — `Client` is the same wrapper type across `natscli/*`; `exitError` is defined once in `watch.go` (Task 6) and reused by `doctor.go` (Task 12). `StateStyle` signature `string -> lipgloss.Style` is consistent. `StatusEventMsg`/`StepDoneMsg` are the only `tea.Msg` types in `tui/`.
