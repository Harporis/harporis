# harporis watch — Fleet View + Source Attribution Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `harporis watch` (no-arg) live fleet dashboard over the shared NATS STATUS stream, plus a `source` field on `StatusEvent` stamped by getter and scanner.

**Architecture:** The STATUS stream (`harporis.status.>`) is the fleet-wide source of truth. A new CLI fleet mode seeds from `ListHistory()` then tails a wildcard `DeliverNew` subscription, folding events into a `map[scanID]*StatusEvent` rendered as a live TUI table (TTY) or plain lines / protojson (non-TTY / `--json`). Getter and scanner stamp `source = "<service>-<hostname>"` on every status event.

**Tech Stack:** Go, protobuf (protoc), NATS JetStream (`nats.go`), Bubble Tea / lipgloss, `protojson`.

**Spec:** `docs/superpowers/specs/2026-06-14-harporis-watch-design.md`

---

## File Structure

| File | Responsibility |
|---|---|
| `contracts/proto/harporis/v1/events.proto` | add `source` field to `StatusEvent` |
| `contracts/gen/go/harporis/v1/events.pb.go` | regenerated (do not hand-edit) |
| `services/getter/internal/nats/publisher.go` | hold `source`, stamp in `PublishStatus` |
| `services/getter/cmd/getter/main.go` | resolve hostname, pass to `NewPublisher` |
| `services/scanner/internal/nats/publisher.go` | hold `source`, stamp in `PublishStatusSecretsFound` |
| `services/scanner/cmd/scanner/main.go` | resolve hostname, pass to `NewPublisher` |
| `services/cli/internal/natscli/status_stream.go` | `SubscribeStatusAll()` wildcard sub |
| `services/cli/internal/ui/status.go` | `SOURCE` in line output |
| `services/cli/internal/tui/fleet_model.go` | **NEW** `FleetModel` multi-row dashboard |
| `services/cli/internal/cmd/watch.go` | no-arg routing, fleet TUI runner, line streamer, protojson |

---

## Task 1: Add `source` field to StatusEvent proto

**Files:**
- Modify: `contracts/proto/harporis/v1/events.proto`
- Regenerate: `contracts/gen/go/harporis/v1/events.pb.go`

- [ ] **Step 1: Add the field**

In `contracts/proto/harporis/v1/events.proto`, change the `StatusEvent` message to:

```proto
message StatusEvent {
  string       scan_id       = 1;
  ScanState    state         = 2;
  int64        timestamp     = 3;
  string       message       = 4;
  ScanMetrics  metrics       = 5;
  OutputConfig output_config = 6;
  string       source        = 7;
}
```

- [ ] **Step 2: Regenerate Go**

Run: `make -C contracts gen`
Expected: exit 0; `git diff --stat contracts/gen` shows `events.pb.go` modified.

- [ ] **Step 3: Verify the field exists**

Run: `grep -n "Source " contracts/gen/go/harporis/v1/events.pb.go | head`
Expected: a line like `Source string \`protobuf:"bytes,7,opt,name=source,proto3" json:"source,omitempty"\``

- [ ] **Step 4: Build contracts**

Run: `cd contracts && go build ./...`
Expected: exit 0.

- [ ] **Step 5: Commit**

```bash
git add contracts/proto/harporis/v1/events.proto contracts/gen/go/harporis/v1/events.pb.go
git commit -m "feat(contracts): add StatusEvent.source for replica attribution"
```

---

## Task 2: Stamp `source` in getter + scanner publishers

**Files:**
- Modify: `services/getter/internal/nats/publisher.go`
- Modify: `services/getter/cmd/getter/main.go:68`
- Modify: `services/scanner/internal/nats/publisher.go`
- Modify: `services/scanner/cmd/scanner/main.go:91`
- Test: `services/getter/internal/nats/publisher_test.go`, `services/scanner/internal/nats/publisher_test.go`

- [ ] **Step 1: Write the failing test (getter)**

Add to `services/getter/internal/nats/publisher_test.go`:

```go
func TestPublishStatusStampsSource(t *testing.T) {
	p := &Publisher{source: "getter-testhost"}
	ev := &v1.StatusEvent{ScanId: "s1", State: v1.ScanState_RUNNING}
	p.stampSource(ev)
	if ev.Source != "getter-testhost" {
		t.Fatalf("source = %q, want getter-testhost", ev.Source)
	}
}
```

- [ ] **Step 2: Run it — expect FAIL**

Run: `go test ./internal/nats/ -run TestPublishStatusStampsSource` (from `services/getter`)
Expected: FAIL — `p.stampSource undefined` and `Publisher has no field source`.

- [ ] **Step 3: Add field + stamp helper + constructor param (getter)**

In `services/getter/internal/nats/publisher.go`, change the struct, constructor, and `PublishStatus`:

```go
type Publisher struct {
	js      nats.JetStreamContext
	ackWait time.Duration
	source  string
}

func NewPublisher(js nats.JetStreamContext, ackWaitSeconds int, source string) *Publisher {
	return &Publisher{js: js, ackWait: time.Duration(ackWaitSeconds) * time.Second, source: source}
}

// stampSource sets the replica identity on an outgoing status event.
func (p *Publisher) stampSource(ev *v1.StatusEvent) { ev.Source = p.source }
```

In the same file, add `p.stampSource(ev)` as the first line of `PublishStatus`:

```go
func (p *Publisher) PublishStatus(ctx context.Context, ev *v1.StatusEvent) error {
	p.stampSource(ev)
	data, err := proto.Marshal(ev)
	// ... unchanged ...
```

- [ ] **Step 4: Update getter main wiring**

In `services/getter/cmd/getter/main.go`, above line 68 add hostname resolution and pass it:

```go
host, _ := os.Hostname()
if host == "" {
	host = "getter"
}
publisher := getnats.NewPublisher(cl.JS, cfg.NATS.JetStream.PublishAckWaitSeconds, "getter-"+host)
```

Ensure `"os"` is imported in that file.

- [ ] **Step 5: Run getter test — expect PASS**

Run: `go test ./internal/nats/ -run TestPublishStatusStampsSource` (from `services/getter`)
Expected: PASS.

- [ ] **Step 6: Write the failing test (scanner)**

Add to `services/scanner/internal/nats/publisher_test.go`:

```go
func TestStatusSecretsFoundCarriesSource(t *testing.T) {
	p := &Publisher{source: "scanner-testhost"}
	ev := p.buildSecretsFoundEvent("s1", 4)
	if ev.Source != "scanner-testhost" {
		t.Fatalf("source = %q, want scanner-testhost", ev.Source)
	}
	if ev.GetMetrics().GetSecretsFound() != 4 {
		t.Fatalf("secrets = %d, want 4", ev.GetMetrics().GetSecretsFound())
	}
}
```

- [ ] **Step 7: Run it — expect FAIL**

Run: `go test ./internal/nats/ -run TestStatusSecretsFoundCarriesSource` (from `services/scanner`)
Expected: FAIL — `buildSecretsFoundEvent undefined` and `Publisher has no field source`.

- [ ] **Step 8: Add field + builder + constructor param (scanner)**

In `services/scanner/internal/nats/publisher.go`, change the struct, constructor, and refactor the event construction into a builder used by `PublishStatusSecretsFound`:

```go
type Publisher struct {
	js          natsclient.JetStreamContext
	publishWait time.Duration
	source      string
}

func NewPublisher(js natsclient.JetStreamContext, publishWait time.Duration, source string) *Publisher {
	return &Publisher{js: js, publishWait: publishWait, source: source}
}

// buildSecretsFoundEvent constructs the RUNNING status event carrying the
// running secrets_found counter, stamped with this replica's source.
func (p *Publisher) buildSecretsFoundEvent(scanID string, count int64) *v1.StatusEvent {
	return &v1.StatusEvent{
		ScanId:    scanID,
		State:     v1.ScanState_RUNNING,
		Timestamp: time.Now().Unix(),
		Metrics:   &v1.ScanMetrics{SecretsFound: count},
		Source:    p.source,
	}
}
```

Then change `PublishStatusSecretsFound` to use it:

```go
func (p *Publisher) PublishStatusSecretsFound(ctx context.Context, scanID string, count int64) error {
	ev := p.buildSecretsFoundEvent(scanID, count)
	body, err := proto.Marshal(ev)
	// ... unchanged ...
```

- [ ] **Step 9: Update scanner main wiring**

In `services/scanner/cmd/scanner/main.go`, above line 91 add:

```go
host, _ := os.Hostname()
if host == "" {
	host = "scanner"
}
pub := scannernats.NewPublisher(cl.JS, time.Duration(cfg.PublishAckWaitSeconds)*time.Second, "scanner-"+host)
```

Ensure `"os"` is imported.

- [ ] **Step 10: Run scanner test — expect PASS, then build both services**

Run: `go test ./internal/nats/ -run TestStatusSecretsFoundCarriesSource` (from `services/scanner`)
Expected: PASS.
Run: `cd services/getter && go build ./... && cd ../scanner && go build ./...`
Expected: exit 0 (call sites updated).

- [ ] **Step 11: Commit**

```bash
git add services/getter services/scanner
git commit -m "feat(getter,scanner): stamp StatusEvent.source with <service>-<hostname>"
```

---

## Task 3: Add `SubscribeStatusAll` to natscli

**Files:**
- Modify: `services/cli/internal/natscli/status_stream.go`
- Test: `services/cli/internal/natscli/status_stream_test.go` (new)

- [ ] **Step 1: Write the failing test**

Create `services/cli/internal/natscli/status_stream_test.go`:

```go
package natscli

import (
	"testing"
	"time"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/kit/nats/wire"
	"google.golang.org/protobuf/proto"
)

func TestSubscribeStatusAllTailsNewEvents(t *testing.T) {
	srv := runJetstream(t)
	cl, err := Dial(srv.ClientURL(), "test-cli")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer cl.Close()
	if err := cl.EnsureStreams(); err != nil {
		t.Fatalf("ensure: %v", err)
	}

	sub, cleanup, err := cl.SubscribeStatusAll()
	if err != nil {
		t.Fatalf("subscribe all: %v", err)
	}
	defer cleanup()

	ev := &v1.StatusEvent{ScanId: "scan-x", State: v1.ScanState_RUNNING, Source: "getter-h1"}
	body, _ := proto.Marshal(ev)
	if _, err := cl.JS.Publish(wire.StatusSubject("scan-x"), body); err != nil {
		t.Fatalf("publish: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		evs, err := FetchStatusEvents(sub, 200*time.Millisecond)
		if err != nil {
			t.Fatalf("fetch: %v", err)
		}
		for _, got := range evs {
			if got.ScanId == "scan-x" && got.Source == "getter-h1" {
				return
			}
		}
	}
	t.Fatal("did not observe published event within deadline")
}
```

- [ ] **Step 2: Run it — expect FAIL**

Run: `go test ./internal/natscli/ -run TestSubscribeStatusAllTailsNewEvents` (from `services/cli`)
Expected: FAIL — `cl.SubscribeStatusAll undefined`.

- [ ] **Step 3: Implement SubscribeStatusAll**

Append to `services/cli/internal/natscli/status_stream.go`:

```go
// SubscribeStatusAll returns a pull subscription over EVERY scan's status
// subject (wildcard) plus a cleanup func. DeliverNew so it tails only
// events arriving after subscription; callers seed historical state
// separately via ListHistory. InactiveThreshold reaps the ephemeral
// consumer server-side if the CLI is killed.
func (c *Client) SubscribeStatusAll() (*natsclient.Subscription, func(), error) {
	consumer := fmt.Sprintf("cli-watch-all-%d", time.Now().UnixNano())
	sub, err := c.JS.PullSubscribe(wire.StatusWildcardSubject, consumer,
		natsclient.BindStream(wire.StatusStream),
		natsclient.DeliverNew(),
		natsclient.InactiveThreshold(30*time.Second))
	if err != nil {
		return nil, nil, fmt.Errorf("subscribe status all: %w", err)
	}
	cleanup := func() {
		_ = sub.Unsubscribe()
		_ = c.JS.DeleteConsumer(wire.StatusStream, consumer)
	}
	return sub, cleanup, nil
}
```

- [ ] **Step 4: Run test — expect PASS**

Run: `go test ./internal/natscli/ -run TestSubscribeStatusAllTailsNewEvents` (from `services/cli`)
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add services/cli/internal/natscli/status_stream.go services/cli/internal/natscli/status_stream_test.go
git commit -m "feat(cli): SubscribeStatusAll — wildcard DeliverNew tail of the status stream"
```

---

## Task 4: Add SOURCE to line output

**Files:**
- Modify: `services/cli/internal/ui/status.go`
- Test: `services/cli/internal/ui/status_test.go` (new)

- [ ] **Step 1: Write the failing test**

Create `services/cli/internal/ui/status_test.go`:

```go
package ui

import (
	"bytes"
	"strings"
	"testing"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
)

func TestPrintStatusLineIncludesSource(t *testing.T) {
	var b bytes.Buffer
	PrintStatusLine(&b, &v1.StatusEvent{
		ScanId: "s1", State: v1.ScanState_RUNNING, Source: "scanner-7f2a",
		Metrics: &v1.ScanMetrics{ChunksPublished: 12},
	})
	if !strings.Contains(b.String(), "src=scanner-7f2a") {
		t.Fatalf("output missing source: %q", b.String())
	}
}
```

- [ ] **Step 2: Run it — expect FAIL**

Run: `go test ./internal/ui/ -run TestPrintStatusLineIncludesSource` (from `services/cli`)
Expected: FAIL — `src=scanner-7f2a` not present.

- [ ] **Step 3: Add source to the format**

In `services/cli/internal/ui/status.go`, change the `fmt.Fprintf` in `PrintStatusLine` to append `src=`:

```go
	fmt.Fprintf(out, "[%s] %-9s | src=%s | %s | scanned=%d skipped=%d chunks=%d bytes=%d errors=%d\n",
		ts, state, ev.GetSource(), ev.Message,
		m.GetBlobsScanned(), m.GetBlobsSkipped(),
		m.GetChunksPublished(), m.GetBytesPublished(), m.GetErrorsTotal())
```

- [ ] **Step 4: Run test — expect PASS**

Run: `go test ./internal/ui/ -run TestPrintStatusLineIncludesSource` (from `services/cli`)
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add services/cli/internal/ui/status.go services/cli/internal/ui/status_test.go
git commit -m "feat(cli): show src=<replica> in status line output"
```

---

## Task 5: FleetModel TUI

**Files:**
- Create: `services/cli/internal/tui/fleet_model.go`
- Test: `services/cli/internal/tui/fleet_model_test.go` (new)

- [ ] **Step 1: Write the failing test**

Create `services/cli/internal/tui/fleet_model_test.go`:

```go
package tui

import (
	"testing"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
)

func TestFleetModelFoldsLatestPerScan(t *testing.T) {
	m := NewFleetModel()
	m2, _ := m.Update(StatusEventMsg{Ev: &v1.StatusEvent{ScanId: "a", State: v1.ScanState_RUNNING, Timestamp: 1}})
	fm := m2.(FleetModel)
	m3, _ := fm.Update(StatusEventMsg{Ev: &v1.StatusEvent{ScanId: "a", State: v1.ScanState_COMPLETED, Timestamp: 2}})
	fm = m3.(FleetModel)

	scans := fm.Scans()
	if len(scans) != 1 {
		t.Fatalf("want 1 scan, got %d", len(scans))
	}
	if scans["a"].State != v1.ScanState_COMPLETED {
		t.Fatalf("want latest COMPLETED, got %v", scans["a"].State)
	}
}

func TestFleetModelOlderTimestampIgnored(t *testing.T) {
	m := NewFleetModel()
	m2, _ := m.Update(StatusEventMsg{Ev: &v1.StatusEvent{ScanId: "a", State: v1.ScanState_COMPLETED, Timestamp: 5}})
	fm := m2.(FleetModel)
	m3, _ := fm.Update(StatusEventMsg{Ev: &v1.StatusEvent{ScanId: "a", State: v1.ScanState_RUNNING, Timestamp: 2}})
	fm = m3.(FleetModel)

	if fm.Scans()["a"].State != v1.ScanState_COMPLETED {
		t.Fatalf("older event must not overwrite newer; got %v", fm.Scans()["a"].State)
	}
}
```

- [ ] **Step 2: Run it — expect FAIL**

Run: `go test ./internal/tui/ -run TestFleetModel` (from `services/cli`)
Expected: FAIL — `NewFleetModel undefined`.

- [ ] **Step 3: Implement FleetModel**

Create `services/cli/internal/tui/fleet_model.go`:

```go
package tui

import (
	"fmt"
	"sort"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/services/cli/internal/ui"
)

// fleetTickMsg drives the "x ago" column refresh.
type fleetTickMsg struct{}

func fleetTick() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return fleetTickMsg{} })
}

// FleetModel is the live multi-scan dashboard. It folds StatusEventMsg
// into latest-per-scan and renders a sorted table. No idle timeout — it
// runs until the user quits.
type FleetModel struct {
	scans       map[string]*v1.StatusEvent
	activeOnly  bool
	natsURL     string
	startedAt   time.Time
	err         error
}

// NewFleetModel returns an empty dashboard.
func NewFleetModel() FleetModel {
	return FleetModel{scans: map[string]*v1.StatusEvent{}, startedAt: time.Now()}
}

// WithNATSURL sets the header URL label.
func (m FleetModel) WithNATSURL(u string) FleetModel { m.natsURL = u; return m }

// Scans exposes the folded state for tests.
func (m FleetModel) Scans() map[string]*v1.StatusEvent { return m.scans }

// Init starts the refresh tick.
func (m FleetModel) Init() tea.Cmd { return fleetTick() }

// Update folds events and handles keys.
func (m FleetModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.KeyMsg:
		switch v.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "f":
			m.activeOnly = !m.activeOnly
			return m, nil
		}
		return m, nil
	case fleetTickMsg:
		return m, fleetTick()
	case StatusEventMsg:
		ev := v.Ev
		if prev, ok := m.scans[ev.ScanId]; !ok || ev.Timestamp >= prev.Timestamp {
			m.scans[ev.ScanId] = ev
		}
		return m, nil
	case SubscribeErrMsg:
		m.err = v.Err
		return m, tea.Quit
	}
	return m, nil
}

// sorted returns scans ordered active-first, then by most-recent update.
func (m FleetModel) sorted() []*v1.StatusEvent {
	out := make([]*v1.StatusEvent, 0, len(m.scans))
	for _, ev := range m.scans {
		if m.activeOnly && IsTerminal(ev.State) {
			continue
		}
		out = append(out, ev)
	}
	sort.Slice(out, func(i, j int) bool {
		ai, aj := IsTerminal(out[i].State), IsTerminal(out[j].State)
		if ai != aj {
			return !ai // active (non-terminal) first
		}
		return out[i].Timestamp > out[j].Timestamp
	})
	return out
}

// View renders the dashboard.
func (m FleetModel) View() string {
	if m.err != nil {
		return ui.BoxStyle.Render("error: " + m.err.Error())
	}
	rows := m.sorted()
	header := fmt.Sprintf("harporis watch — %d scans   %s   %s",
		len(rows), ui.DimStyle.Render(m.natsURL),
		time.Now().UTC().Format("15:04:05"))
	if len(rows) == 0 {
		body := ui.DimStyle.Render("(no scans yet, waiting…)")
		footer := ui.DimStyle.Render("q quit · f filter active")
		return ui.BoxStyle.Render(lipgloss.JoinVertical(lipgloss.Left, header, body, footer))
	}
	t := ui.NewTable("SCAN_ID", "STATE", "SOURCE", "CHUNKS", "SECRETS", "UPDATED")
	for _, e := range rows {
		mtr := e.GetMetrics()
		t.Row(
			e.ScanId,
			ui.StateStyle(e.State.String()).Render(e.State.String()),
			e.GetSource(),
			fmt.Sprintf("%d", mtr.GetChunksPublished()),
			fmt.Sprintf("%d", mtr.GetSecretsFound()),
			agoString(e.Timestamp),
		)
	}
	var tb lipgloss.Style = ui.BoxStyle
	var sb strings.Builder
	sb.WriteString(header + "\n")
	_, _ = t.WriteTo(&sb)
	sb.WriteString(ui.DimStyle.Render("q quit · f filter active"))
	return tb.Render(sb.String())
}

// agoString renders a compact relative age like "2s ago".
func agoString(unix int64) string {
	d := time.Since(time.Unix(unix, 0)).Truncate(time.Second)
	if d < 0 {
		d = 0
	}
	return d.String() + " ago"
}
```

Add `"strings"` to the import block (used by `sb strings.Builder`).

- [ ] **Step 4: Run tests — expect PASS**

Run: `go test ./internal/tui/ -run TestFleetModel` (from `services/cli`)
Expected: PASS (both subtests).

- [ ] **Step 5: Commit**

```bash
git add services/cli/internal/tui/fleet_model.go services/cli/internal/tui/fleet_model_test.go
git commit -m "feat(cli): FleetModel — live multi-scan dashboard model"
```

---

## Task 6: Wire no-arg fleet mode into the watch command

**Files:**
- Modify: `services/cli/internal/cmd/watch.go`
- Test: `services/cli/internal/cmd/watch_test.go` (new)

- [ ] **Step 1: Write the failing test (json encoder helper)**

Create `services/cli/internal/cmd/watch_test.go`:

```go
package cmd

import (
	"bytes"
	"strings"
	"testing"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
)

func TestWriteStatusJSONEmitsProtojson(t *testing.T) {
	var b bytes.Buffer
	writeStatusJSON(&b, &v1.StatusEvent{ScanId: "s1", State: v1.ScanState_RUNNING, Source: "getter-h1"})
	out := b.String()
	if !strings.Contains(out, `"scanId":"s1"`) || !strings.Contains(out, `"source":"getter-h1"`) {
		t.Fatalf("not protojson: %q", out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Fatalf("want newline-terminated json line: %q", out)
	}
}
```

- [ ] **Step 2: Run it — expect FAIL**

Run: `go test ./internal/cmd/ -run TestWriteStatusJSONEmitsProtojson` (from `services/cli`)
Expected: FAIL — `writeStatusJSON undefined`.

- [ ] **Step 3: Add the json helper + change command routing**

In `services/cli/internal/cmd/watch.go`, add imports `"io"` (already present) and `"google.golang.org/protobuf/encoding/protojson"`, then add:

```go
// writeStatusJSON emits one protojson-encoded StatusEvent per line.
func writeStatusJSON(out io.Writer, ev *v1.StatusEvent) {
	b, err := protojson.Marshal(ev)
	if err != nil {
		return
	}
	_, _ = out.Write(append(b, '\n'))
}
```

Change `newWatchCmd` so the argument is optional and routes to fleet mode when absent:

```go
func newWatchCmd() *cobra.Command {
	var idle time.Duration
	c := &cobra.Command{
		Use:   "watch [scan-id]",
		Short: "follow status — one scan, or the whole fleet when no id is given",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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
			// Fleet mode: no scan-id.
			if len(args) == 0 {
				if !jsonOut && isatty.IsTerminal(os.Stdout.Fd()) {
					return RunFleetTUI(cl, natsURL)
				}
				return StreamStatusLinesAll(cmd.OutOrStdout(), cl, jsonOut)
			}
			// Single-scan mode (unchanged behaviour).
			scanID := args[0]
			if !jsonOut && isatty.IsTerminal(os.Stdout.Fd()) {
				return RunWatchTUI(cl, scanID, idle)
			}
			return StreamStatusLines(cmd.OutOrStdout(), cl, scanID, idle)
		},
	}
	c.Flags().DurationVar(&idle, "timeout", 30*time.Minute, "give up if no status events arrive for this long (single-scan mode)")
	return c
}
```

- [ ] **Step 4: Add RunFleetTUI + StreamStatusLinesAll**

Append to `services/cli/internal/cmd/watch.go`:

```go
// RunFleetTUI runs the live multi-scan dashboard until ctrl+c. It seeds
// the table from ListHistory then tails the wildcard status stream.
func RunFleetTUI(cl *natscli.Client, natsURL string) error {
	sub, cleanup, err := cl.SubscribeStatusAll()
	if err != nil {
		return err
	}
	defer cleanup()

	p := tea.NewProgram(tui.NewFleetModel().WithNATSURL(natsURL), tea.WithAltScreen())
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Seed snapshot (newest state per scan) before tailing live.
	if seed, err := cl.ListHistory(0, 1*time.Second); err == nil {
		for _, ev := range seed {
			p.Send(tui.StatusEventMsg{Ev: ev})
		}
	}

	go func() {
		for ctx.Err() == nil {
			events, err := natscli.FetchStatusEvents(sub, 2*time.Second)
			if err != nil {
				p.Send(tui.SubscribeErrMsg{Err: err})
				return
			}
			for _, ev := range events {
				p.Send(tui.StatusEventMsg{Ev: ev})
			}
		}
	}()

	_, err = p.Run()
	return err
}

// StreamStatusLinesAll tails every scan's status and prints one line (or
// one protojson object) per event. Runs until ctrl+c. No idle timeout.
func StreamStatusLinesAll(out io.Writer, cl *natscli.Client, jsonOut bool) error {
	sub, cleanup, err := cl.SubscribeStatusAll()
	if err != nil {
		return err
	}
	defer cleanup()

	// Seed snapshot first so a piped consumer sees current state.
	if seed, err := cl.ListHistory(0, 1*time.Second); err == nil {
		for _, ev := range seed {
			if jsonOut {
				writeStatusJSON(out, ev)
			} else {
				ui.PrintStatusLine(out, ev)
			}
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	for ctx.Err() == nil {
		events, err := natscli.FetchStatusEvents(sub, 2*time.Second)
		if err != nil {
			return fmt.Errorf("watch-all fetch: %w", err)
		}
		for _, ev := range events {
			if jsonOut {
				writeStatusJSON(out, ev)
			} else {
				ui.PrintStatusLine(out, ev)
			}
		}
	}
	return nil
}
```

- [ ] **Step 5: Run test — expect PASS, then build the CLI**

Run: `go test ./internal/cmd/ -run TestWriteStatusJSONEmitsProtojson` (from `services/cli`)
Expected: PASS.
Run: `cd services/cli && go build ./...`
Expected: exit 0.

- [ ] **Step 6: Commit**

```bash
git add services/cli/internal/cmd/watch.go services/cli/internal/cmd/watch_test.go
git commit -m "feat(cli): harporis watch fleet mode (no-arg) + protojson --json"
```

---

## Task 7: Full build + test sweep

- [ ] **Step 1: Build everything**

Run (from repo root): `go build ./... 2>&1 | head` for each module, or:
```bash
for d in contracts kit services/getter services/scanner services/writer services/cli; do (cd $d && go build ./...) || echo "BUILD FAIL: $d"; done
```
Expected: no `BUILD FAIL` lines.

- [ ] **Step 2: Run unit tests for touched modules**

```bash
(cd services/getter && go test ./internal/nats/...)
(cd services/scanner && go test ./internal/nats/...)
(cd services/cli && go test ./internal/...)
```
Expected: all PASS.

- [ ] **Step 3: Live smoke (requires docker stack)**

```bash
docker compose up -d --build
harporis scan --local ~/scanner-fixtures --scan-id watch-smoke &
harporis watch          # observe watch-smoke row appear, progress, go terminal
```
Expected: the fleet table shows `watch-smoke` with a `SOURCE` like `scanner-<host>`, updating live, then COMPLETED. `q` quits.

- [ ] **Step 4: Non-TTY + json checks**

```bash
harporis watch | head -5            # plain lines incl. src=
harporis --json watch | head -3     # protojson objects incl. "source"
```
Expected: line output contains `src=`; json output is one `{...}` per line containing `"source"`.

- [ ] **Step 5: Commit any smoke-driven fixes (if needed)**

```bash
git add -A && git commit -m "fix(cli): watch fleet smoke adjustments"
```

---

## Self-Review notes (resolved)

- **Spec coverage:** command surface (T6), source attribution (T1, T2), seed-then-tail (T6 RunFleetTUI/StreamStatusLinesAll), TUI table (T5), line output SOURCE (T4), protojson `--json` both modes (T6), error/empty handling (T5 View empty-state + SubscribeErrMsg). All covered.
- **Type consistency:** `NewPublisher` gains a `source string` param in both services (call sites updated T2). `SubscribeStatusAll` returns `(*nats.Subscription, func(), error)` matching `SubscribeStatus`. `FleetModel` reuses existing `tui.StatusEventMsg` / `tui.SubscribeErrMsg` / `tui.IsTerminal`.
- **Note for implementer:** writer is intentionally untouched — it consumes STATUS for finalization and never publishes, so it stamps no source.
