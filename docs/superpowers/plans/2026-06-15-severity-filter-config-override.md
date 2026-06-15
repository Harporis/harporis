# Severity Filter + Config-Override Scaffold Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an operator-controllable severity filter to Harporis reports — a writer-time config default (`severities` in writer.yaml) that gates all 7 sinks, plus a read-time `findings show --severity` CLI override that works for every format (text formats filtered locally, binary formats regenerated from NDJSON).

**Architecture:** A shared severity-set parser lives in `contracts/severity` (next to the `Severity` enum, mirroring `contracts/scanstate`). The writer gates findings before any sink sees them, so the config default uniformly affects all formats. The CLI read-time filter reuses the existing `writer-rebuild` NDJSON-replay path for binary formats. No proto/getter/scanner changes; the dead `ConfigOverride`/`allow_request_overrides` skeleton is not used.

**Tech Stack:** Go (multi-module repo with `replace` directives — no go.work), protobuf-generated `v1.Severity` enum, cobra CLI, Prometheus client_golang, docker compose exec.

**Spec:** `docs/superpowers/specs/2026-06-15-severity-filter-config-override-design.md`

---

## File Structure

| File | Responsibility |
|------|----------------|
| `contracts/severity/severity.go` (create) | `Set` type + `ParseSet`/`ParseCSV` + `Contains`; single string↔enum mapping |
| `contracts/severity/severity_test.go` (create) | parser unit tests |
| `services/writer/internal/config/load.go` (modify) | `Severities []string` field; `SeveritySet()` method; validate in `Load` |
| `services/writer/internal/config/load_test.go` (modify) | config parse + validation tests |
| `services/writer/config/writer.yaml` (modify) | document `severities: []` key |
| `services/writer/internal/metrics/metrics.go` (modify) | `SinkSeverityDropped` counter |
| `services/writer/cmd/writer/main.go` (modify) | writer-time severity gate before sinks |
| `services/writer/cmd/rebuild/main.go` (modify) | `--severity` flag; filter in `replay()` |
| `services/writer/cmd/rebuild/main_test.go` (create or modify) | replay filter test |
| `services/cli/internal/cmd/findings.go` (modify) | `show --severity`: local ndjson filter (text) + rebuild regen (binary) |
| `services/cli/internal/cmd/findings_test.go` (modify) | ndjson-filter unit test |

Module test commands (run from the module root, since this is a multi-module repo):
- contracts: `cd contracts && go test ./severity/...`
- writer: `cd services/writer && go test ./...`
- cli: `cd services/cli && go test ./...`

Commit convention for this repo: **no `Co-Authored-By` trailer.**

---

## Task 1: Shared severity-set parser (`contracts/severity`)

**Files:**
- Create: `contracts/severity/severity.go`
- Create: `contracts/severity/severity_test.go`

- [ ] **Step 1: Write the failing test**

Create `contracts/severity/severity_test.go`:

```go
package severity

import (
	"testing"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
)

func TestParseCSV_ValidLevels(t *testing.T) {
	set, err := ParseCSV("CRITICAL,high")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !set.Contains(v1.Severity_CRITICAL) || !set.Contains(v1.Severity_HIGH) {
		t.Fatalf("expected CRITICAL and HIGH in set")
	}
	if set.Contains(v1.Severity_LOW) {
		t.Fatalf("LOW should not be in set")
	}
}

func TestParseCSV_Empty_IsNoFilter(t *testing.T) {
	set, err := ParseCSV("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Empty set = "no filter": Contains is true for every level.
	for _, s := range []v1.Severity{v1.Severity_LOW, v1.Severity_MEDIUM, v1.Severity_HIGH, v1.Severity_CRITICAL} {
		if !set.Contains(s) {
			t.Fatalf("empty set should contain %v", s)
		}
	}
	if len(set) != 0 {
		t.Fatalf("empty CSV should yield empty set, got %d", len(set))
	}
}

func TestParseCSV_WhitespaceAndCase(t *testing.T) {
	set, err := ParseCSV("  Low , MEDIUM ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !set.Contains(v1.Severity_LOW) || !set.Contains(v1.Severity_MEDIUM) {
		t.Fatalf("expected LOW and MEDIUM")
	}
}

func TestParseSet_UnknownLevel(t *testing.T) {
	_, err := ParseSet([]string{"HIGH", "BOGUS"})
	if err == nil {
		t.Fatalf("expected error for unknown level")
	}
}

func TestParseSet_RejectsUnspecified(t *testing.T) {
	_, err := ParseSet([]string{"SEVERITY_UNSPECIFIED"})
	if err == nil {
		t.Fatalf("SEVERITY_UNSPECIFIED is not a selectable level")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd contracts && go test ./severity/...`
Expected: FAIL (package/functions not defined — build error).

- [ ] **Step 3: Write minimal implementation**

Create `contracts/severity/severity.go`:

```go
// Package severity provides the single, shared mapping between severity
// level names and the v1.Severity enum, plus a Set type used by the
// writer (config default) and the CLI (read-time --severity filter) to
// decide which findings reach reports. Mirrors contracts/scanstate:
// one place owns the string<->enum knowledge.
package severity

import (
	"fmt"
	"sort"
	"strings"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
)

// Set is a set of selectable severity levels. An EMPTY set means
// "no filter" — Contains returns true for every level. This lets call
// sites write `if !set.Contains(f.Severity) { drop }` with empty = pass-all.
type Set map[v1.Severity]bool

// Contains reports whether sev passes the filter. An empty set passes all.
func (s Set) Contains(sev v1.Severity) bool {
	if len(s) == 0 {
		return true
	}
	return s[sev]
}

// validLevels is the set of operator-selectable levels (UNSPECIFIED is
// excluded — it is the zero value, not a real severity).
var validLevels = map[string]v1.Severity{
	"LOW":      v1.Severity_LOW,
	"MEDIUM":   v1.Severity_MEDIUM,
	"HIGH":     v1.Severity_HIGH,
	"CRITICAL": v1.Severity_CRITICAL,
}

// ParseSet parses level names (case-insensitive, whitespace-trimmed) into
// a Set. Empty input yields an empty Set ("no filter"). An unknown or
// unspecified name returns an error listing the valid levels.
func ParseSet(names []string) (Set, error) {
	set := make(Set)
	for _, raw := range names {
		name := strings.ToUpper(strings.TrimSpace(raw))
		if name == "" {
			continue
		}
		sev, ok := validLevels[name]
		if !ok {
			return nil, fmt.Errorf("unknown severity %q (want one of: %s)", raw, validNamesList())
		}
		set[sev] = true
	}
	return set, nil
}

// ParseCSV splits a comma-separated string ("CRITICAL,HIGH") and parses it.
func ParseCSV(s string) (Set, error) {
	if strings.TrimSpace(s) == "" {
		return Set{}, nil
	}
	return ParseSet(strings.Split(s, ","))
}

func validNamesList() string {
	names := make([]string, 0, len(validLevels))
	for n := range validLevels {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd contracts && go test ./severity/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add contracts/severity/severity.go contracts/severity/severity_test.go
git commit -m "feat(contracts): shared severity-set parser in contracts/severity"
```

---

## Task 2: Writer config `severities` field + validation

**Files:**
- Modify: `services/writer/internal/config/load.go`
- Modify: `services/writer/internal/config/load_test.go`

Confirm first that `services/writer/go.mod` has a `replace` for `github.com/Harporis/harporis/contracts` (it does — writer already imports `v1`). The new `contracts/severity` package is reachable without a go.mod change.

- [ ] **Step 1: Write the failing test**

Add to `services/writer/internal/config/load_test.go`:

```go
func TestSeveritySet_Valid(t *testing.T) {
	c := &Config{Severities: []string{"CRITICAL", "HIGH"}}
	set, err := c.SeveritySet()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !set.Contains(v1.Severity_CRITICAL) || !set.Contains(v1.Severity_HIGH) {
		t.Fatalf("expected CRITICAL+HIGH in set")
	}
	if set.Contains(v1.Severity_LOW) {
		t.Fatalf("LOW should be filtered out")
	}
}

func TestSeveritySet_EmptyMeansAll(t *testing.T) {
	c := &Config{Severities: nil}
	set, err := c.SeveritySet()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !set.Contains(v1.Severity_LOW) {
		t.Fatalf("empty severities should pass all levels")
	}
}

func TestSeveritySet_UnknownLevelErrors(t *testing.T) {
	c := &Config{Severities: []string{"BOGUS"}}
	if _, err := c.SeveritySet(); err == nil {
		t.Fatalf("expected error for unknown level")
	}
}
```

Add the imports `v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"` to the test file if not already present.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd services/writer && go test ./internal/config/...`
Expected: FAIL (`Config` has no field `Severities`; no method `SeveritySet`).

- [ ] **Step 3: Write minimal implementation**

In `services/writer/internal/config/load.go`, add the import:

```go
	"github.com/Harporis/harporis/contracts/severity"
```

Add the field to the `Config` struct (next to the sink toggles):

```go
	// Severities, when non-empty, restricts which finding severities are
	// written to ANY sink (the gate sits before fan-out, so it affects all
	// formats uniformly). Empty (default) = no filter, every level written.
	// Valid level names: LOW, MEDIUM, HIGH, CRITICAL (case-insensitive).
	Severities []string `yaml:"severities"`
```

Add the method at the end of the file:

```go
// SeveritySet parses the configured severities into a severity.Set.
// An empty config list yields an empty set ("no filter"). Returns an
// error if any configured level name is invalid.
func (c *Config) SeveritySet() (severity.Set, error) {
	return severity.ParseSet(c.Severities)
}
```

In `Load`, after `applyDefaults(&cfg)`, validate fail-fast:

```go
	applyDefaults(&cfg)
	if _, err := cfg.SeveritySet(); err != nil {
		return nil, fmt.Errorf("severities: %w", err)
	}
	return &cfg, nil
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd services/writer && go test ./internal/config/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add services/writer/internal/config/load.go services/writer/internal/config/load_test.go
git commit -m "feat(writer): severities config field + SeveritySet validation"
```

---

## Task 3: Writer-time severity gate + metric

**Files:**
- Modify: `services/writer/internal/metrics/metrics.go`
- Modify: `services/writer/cmd/writer/main.go`

- [ ] **Step 1: Add the metric collector**

In `services/writer/internal/metrics/metrics.go`, add to the `var (...)` block (near `SinkFormatIgnored`):

```go
	SinkSeverityDropped *prometheus.CounterVec // labels: severity
```

In `Init()`, create and register it (place next to `SinkFormatIgnored`):

```go
		SinkSeverityDropped = prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "writer_sink_severity_dropped_total",
			Help: "Findings dropped before fan-out because their severity is not in the writer's configured `severities` set.",
		}, []string{"severity"})
```

Add `SinkSeverityDropped` to the `MustRegister` slice, and seed it next to the other seeds:

```go
		SinkSeverityDropped.WithLabelValues("")
```

- [ ] **Step 2: Build the severity set at writer startup**

In `services/writer/cmd/writer/main.go`, after the config is loaded (search for where `cfg` is available and sinks are built), add:

```go
	severitySet, err := cfg.SeveritySet()
	if err != nil {
		fatal("invalid severities config: %v", err)
	}
```

(Config was already validated in `Load`, but building the runtime set here is the single source for the worker closure. Add the import `// already imports config`; no new import needed — `SeveritySet()` returns `severity.Set` and the worker only calls `.Contains`, so add `"github.com/Harporis/harporis/contracts/severity"` ONLY if you reference the type name explicitly. Using `:=` avoids needing the import.)

- [ ] **Step 3: Gate findings in the worker closure**

In the worker closure in `main.go` (the `consumer.Run(..., func(ctx, f *v1.Finding) error {` block, around line 265), add the gate as the FIRST statement inside the callback, before the `wrote := 0` / sink loop:

```go
				if !severitySet.Contains(f.Severity) {
					metrics.SinkSeverityDropped.WithLabelValues(f.Severity.String()).Inc()
					return nil
				}
```

This drops the finding before any sink sees it, so all formats honour the filter. `return nil` acks the message (the finding is intentionally not written, not an error).

- [ ] **Step 4: Build to verify it compiles**

Run: `cd services/writer && go build ./...`
Expected: builds clean.

- [ ] **Step 5: Write a gate test**

The worker closure is inline in `main.go` and hard to unit-test directly. Instead, add a focused test for the decision in `services/writer/cmd/writer/` is not practical; rely on the `severity.Set.Contains` test (Task 1) for the gate logic and the config test (Task 2) for parsing. Add a metrics smoke test in `services/writer/internal/metrics/metrics_test.go` (create if absent):

```go
package metrics

import "testing"

func TestSinkSeverityDroppedRegistered(t *testing.T) {
	Init()
	if SinkSeverityDropped == nil {
		t.Fatal("SinkSeverityDropped not initialized")
	}
	SinkSeverityDropped.WithLabelValues("HIGH").Inc()
}
```

- [ ] **Step 6: Run tests**

Run: `cd services/writer && go test ./internal/metrics/... && go build ./...`
Expected: PASS + clean build.

- [ ] **Step 7: Commit**

```bash
git add services/writer/internal/metrics/metrics.go services/writer/cmd/writer/main.go services/writer/internal/metrics/metrics_test.go
git commit -m "feat(writer): severity gate before sink fan-out + dropped metric"
```

---

## Task 4: `writer-rebuild --severity` flag

**Files:**
- Modify: `services/writer/cmd/rebuild/main.go`
- Create: `services/writer/cmd/rebuild/main_test.go`

- [ ] **Step 1: Write the failing test**

Create `services/writer/cmd/rebuild/main_test.go`:

```go
package main

import (
	"bytes"
	"context"
	"testing"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/contracts/severity"
	"google.golang.org/protobuf/encoding/protojson"
)

// captureSink records findings written to it, for asserting the filter.
type captureSink struct{ got []v1.Severity }

func (c *captureSink) Name() string { return "capture_file" }
func (c *captureSink) Write(_ context.Context, f *v1.Finding) error {
	c.got = append(c.got, f.Severity)
	return nil
}
func (c *captureSink) Close() error                                  { return nil }
func (c *captureSink) Finalize(_ context.Context, _ string) error    { return nil }

func ndjsonLine(t *testing.T, scanID string, sev v1.Severity) []byte {
	t.Helper()
	b, err := protojson.Marshal(&v1.Finding{ScanId: scanID, Severity: sev})
	if err != nil {
		t.Fatal(err)
	}
	return append(b, '\n')
}

func TestReplayFiltersBySeverity(t *testing.T) {
	var buf bytes.Buffer
	buf.Write(ndjsonLine(t, "s1", v1.Severity_LOW))
	buf.Write(ndjsonLine(t, "s1", v1.Severity_CRITICAL))
	buf.Write(ndjsonLine(t, "s1", v1.Severity_HIGH))

	set, _ := severity.ParseCSV("CRITICAL,HIGH")
	sink := &captureSink{}
	n, err := replay(context.Background(), &buf, sink, "s1", set)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected 2 written, got %d", n)
	}
	for _, s := range sink.got {
		if s == v1.Severity_LOW {
			t.Fatalf("LOW should have been filtered")
		}
	}
}
```

Note: the existing `replay` takes an `*os.File`; the test passes a `*bytes.Buffer`. Step 3 changes `replay` to accept an `io.Reader` so it's testable.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd services/writer && go test ./cmd/rebuild/...`
Expected: FAIL (`replay` signature mismatch; `severity` import unused upstream).

- [ ] **Step 3: Implement the filter**

In `services/writer/cmd/rebuild/main.go`:

Add imports:
```go
	"io"

	"github.com/Harporis/harporis/contracts/severity"
```

Add the flag in `main()` after the `format` flag:
```go
	sevCSV := flag.String("severity", "", "comma-separated severity levels to KEEP (e.g. CRITICAL,HIGH); empty = all")
```

Parse it after `ValidateScanID`:
```go
	sevSet, err := severity.ParseCSV(*sevCSV)
	if err != nil {
		fail("invalid --severity: %v", err)
	}
```

Change the `replay` call to pass the set:
```go
	count, err := replay(ctx, f, out, *scanID, sevSet)
```

Change `replay`'s signature and add the filter (the param type changes from `*os.File` to `io.Reader`; `*os.File` satisfies `io.Reader` so the production call still works):
```go
func replay(ctx context.Context, r io.Reader, out rebuildSink, scanID string, sevSet severity.Set) (int, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 256*1024), 16*1024*1024)
	um := protojson.UnmarshalOptions{DiscardUnknown: true}
	var count int
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var f v1.Finding
		if err := um.Unmarshal(line, &f); err != nil {
			return count, fmt.Errorf("decode line %d: %w", count+1, err)
		}
		if f.ScanId != scanID {
			return count, fmt.Errorf("line %d carries scan_id %q, expected %q (mixed file?)", count+1, f.ScanId, scanID)
		}
		if !sevSet.Contains(f.Severity) {
			continue
		}
		if err := out.Write(ctx, &f); err != nil {
			return count, fmt.Errorf("sink write %d: %w", count+1, err)
		}
		count++
	}
	if err := sc.Err(); err != nil {
		return count, fmt.Errorf("read ndjson: %w", err)
	}
	return count, nil
}
```

Update the usage string to mention `--severity`:
```go
		fail("usage: writer-rebuild --scan-id X --format {sarif|html|xlsx|pdf|parquet} [--severity LEVELS] [--input-dir D] [--output-dir D]")
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd services/writer && go test ./cmd/rebuild/... && go build ./...`
Expected: PASS + clean build.

- [ ] **Step 5: Commit**

```bash
git add services/writer/cmd/rebuild/main.go services/writer/cmd/rebuild/main_test.go
git commit -m "feat(writer): writer-rebuild --severity filters replay by level set"
```

---

## Task 5: CLI `findings show --severity` for text formats

**Files:**
- Modify: `services/cli/internal/cmd/findings.go`
- Modify: `services/cli/internal/cmd/findings_test.go`

Confirm `services/cli/go.mod` has the `contracts` replace (it does — CLI imports `contracts`).

- [ ] **Step 1: Write the failing test**

Add to `services/cli/internal/cmd/findings_test.go`:

```go
func TestFilterNDJSONBySeverity(t *testing.T) {
	low, _ := protojson.Marshal(&v1.Finding{ScanId: "s1", Severity: v1.Severity_LOW})
	crit, _ := protojson.Marshal(&v1.Finding{ScanId: "s1", Severity: v1.Severity_CRITICAL})
	body := string(low) + "\n" + string(crit) + "\n"

	set, _ := severity.ParseCSV("CRITICAL")
	out, err := filterNDJSONBySeverity(body, set)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, `"LOW"`) {
		t.Fatalf("LOW line should be filtered out, got: %q", out)
	}
	if !strings.Contains(out, `"CRITICAL"`) {
		t.Fatalf("CRITICAL line should remain, got: %q", out)
	}
}

func TestFilterNDJSONBySeverity_EmptySetPassThrough(t *testing.T) {
	low, _ := protojson.Marshal(&v1.Finding{ScanId: "s1", Severity: v1.Severity_LOW})
	body := string(low) + "\n"
	set, _ := severity.ParseCSV("")
	out, err := filterNDJSONBySeverity(body, set)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != body {
		t.Fatalf("empty set should pass body unchanged")
	}
}
```

Ensure the test file imports: `"strings"`, `"google.golang.org/protobuf/encoding/protojson"`, `v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"`, `"github.com/Harporis/harporis/contracts/severity"`.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd services/cli && go test ./internal/cmd/... -run TestFilterNDJSON`
Expected: FAIL (`filterNDJSONBySeverity` undefined).

- [ ] **Step 3: Implement the filter helper + wire it into `show`**

In `services/cli/internal/cmd/findings.go`, add imports if missing:
```go
	"bufio"

	"google.golang.org/protobuf/encoding/protojson"
	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/contracts/severity"
```

Add the helper:
```go
// filterNDJSONBySeverity keeps only NDJSON lines whose Finding.severity is
// in set. An empty set returns body unchanged (no filter). Used for the
// text-rendered formats (ndjson/pretty/json/csv/md) which all derive from
// the on-disk NDJSON.
func filterNDJSONBySeverity(body string, set severity.Set) (string, error) {
	if len(set) == 0 {
		return body, nil
	}
	var b strings.Builder
	sc := bufio.NewScanner(strings.NewReader(body))
	sc.Buffer(make([]byte, 0, 256*1024), 16*1024*1024)
	um := protojson.UnmarshalOptions{DiscardUnknown: true}
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var f v1.Finding
		if err := um.Unmarshal(line, &f); err != nil {
			return "", fmt.Errorf("decode ndjson line: %w", err)
		}
		if !set.Contains(f.Severity) {
			continue
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := sc.Err(); err != nil {
		return "", fmt.Errorf("read ndjson: %w", err)
	}
	return b.String(), nil
}
```

In `newFindingsShowCmd`, add the flag var and registration:
```go
	var severityCSV string
	// ... in the flag registration block near outputDir/format:
	c.Flags().StringVar(&severityCSV, "severity", "", "comma-separated severity levels to KEEP (e.g. CRITICAL,HIGH); empty = all")
```

Inside the `RunE`, after `scanID` validation and after `format` is normalised/validated, parse the set once:
```go
			sevSet, err := severity.ParseCSV(severityCSV)
			if err != nil {
				return err
			}
```

For the text-rendered formats, the existing code reads `body` from `readFindingsFile(scanID, ".ndjson", outputDir)` then renders. Apply the filter to `body` before rendering. Locate the block where `ext := ".ndjson"` is used (text formats) and after `readFindingsFile` returns `body`, insert:
```go
			// Text formats derive from NDJSON; filter in-process.
			if _, isProxy := formatToExt[format]; !isProxy {
				body, err = filterNDJSONBySeverity(body, sevSet)
				if err != nil {
					return err
				}
			}
```

Note: for `format == "ndjson"` the current switch streams `body` raw — since `body` is now the filtered string, the raw stream already reflects the filter. No further change needed for the ndjson case.

(The proxy-format branch — `formatToExt[format]` present — is handled in Task 6.)

- [ ] **Step 4: Run test to verify it passes**

Run: `cd services/cli && go test ./internal/cmd/... -run TestFilterNDJSON && go build ./...`
Expected: PASS + clean build.

- [ ] **Step 5: Commit**

```bash
git add services/cli/internal/cmd/findings.go services/cli/internal/cmd/findings_test.go
git commit -m "feat(cli): findings show --severity filters text formats from ndjson"
```

---

## Task 6: CLI `findings show --severity` for binary formats (rebuild regen)

**Files:**
- Modify: `services/cli/internal/cmd/findings.go`

This path runs only in docker-compose mode (no `--output-dir`), because `writer-rebuild` lives in the writer container. With `--output-dir` set, regenerating a binary format with a filter isn't available locally — return a clear error.

- [ ] **Step 1: Add the proxy-format regen path in `show`**

In `newFindingsShowCmd`'s `RunE`, the current proxy-format handling reads the canonical file via `readFindingsFile(scanID, ext, outputDir)` and streams it (the `case "ndjson", "sarif", "html", "xlsx", "pdf":` arm streams raw). Before that read, branch when a severity filter is requested for a proxy format:

```go
			if _, isProxy := formatToExt[format]; isProxy && len(sevSet) > 0 {
				body, err = regenProxyWithSeverity(scanID, format, severityCSV, outputDir)
				if err != nil {
					return err
				}
				// body now holds the filtered, freshly-rendered file;
				// fall through to the streaming switch which writes it.
			}
```

Make sure this runs BEFORE the existing `body, err := readFindingsFile(...)` for proxy formats. Restructure so that when `body` was already populated by `regenProxyWithSeverity`, the code does not overwrite it with `readFindingsFile`. Concretely, guard the existing read:

```go
			if body == "" { // not already populated by the regen path
				body, err = readFindingsFile(scanID, ext, outputDir)
				if err != nil {
					return err
				}
			}
```

(Declare `var body string` earlier in `RunE` so both paths can assign it.)

- [ ] **Step 2: Implement `regenProxyWithSeverity`**

Add to `findings.go`:

```go
// regenProxyWithSeverity regenerates a binary/proxy format (sarif/html/
// xlsx/pdf/parquet) filtered by severity, by replaying the scan's NDJSON
// through writer-rebuild inside the writer container, then reading the
// result back. The canonical <scan>.<ext> on disk is untouched: rebuild
// writes to a temp dir, we cat it, then delete it.
//
// Only available in docker-compose mode; with --output-dir there is no
// writer container to run the rebuild.
func regenProxyWithSeverity(scanID, format, severityCSV, outputDir string) (string, error) {
	if outputDir != "" {
		return "", fmt.Errorf("--severity on format %q needs the writer container (writer-rebuild); "+
			"omit --output-dir to use docker compose, set `severities` in writer.yaml, or use a text format", format)
	}
	co, err := compose.NewDefault()
	if err != nil {
		return "", fmt.Errorf("docker compose not available: %w", err)
	}
	ext := formatToExt[format]
	tmpDir := "/tmp/harporis-sevfilter"
	tmpFile := tmpDir + "/" + scanID + ext

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Regenerate into the temp dir.
	if out, err := co.Exec(ctx, "writer",
		"/usr/local/bin/writer-rebuild",
		"--scan-id", scanID,
		"--format", format,
		"--severity", severityCSV,
		"--output-dir", tmpDir,
	); err != nil {
		detail := strings.TrimSpace(out)
		if detail == "" {
			detail = err.Error()
		}
		return "", fmt.Errorf("writer-rebuild --severity: %s", detail)
	}

	// Read it back, then clean up regardless of read outcome.
	body, readErr := co.Exec(ctx, "writer", "cat", tmpFile)
	_, _ = co.Exec(ctx, "writer", "rm", "-f", tmpFile)
	if readErr != nil {
		detail := strings.TrimSpace(body)
		if detail == "" {
			detail = readErr.Error()
		}
		return "", fmt.Errorf("read regenerated %s: %s", scanID+ext, detail)
	}
	return body, nil
}
```

`writer-rebuild` already creates `--output-dir` if it writes there? It does NOT mkdir — confirm by reading `cmd/rebuild/main.go`'s sink constructors. The sink `New*Config(dir, ...)` writes to `dir`; if `/tmp/harporis-sevfilter` doesn't exist the write fails. So create it first:

```go
	if out, err := co.Exec(ctx, "writer", "mkdir", "-p", tmpDir); err != nil {
		return "", fmt.Errorf("mkdir temp: %s", strings.TrimSpace(out))
	}
```

Place this `mkdir` exec immediately before the `writer-rebuild` exec.

- [ ] **Step 3: Build to verify it compiles**

Run: `cd services/cli && go build ./...`
Expected: clean build.

- [ ] **Step 4: Manual / integration verification**

This path requires a running stack and cannot be unit-tested (it shells into the writer container). Verify manually:

```bash
# with a stack up and a scan that produced findings of mixed severity:
harporis findings show <scan_id> --format pdf --severity CRITICAL > /tmp/crit.pdf
# confirm /tmp/crit.pdf contains only CRITICAL findings,
# and the canonical findings file is unchanged:
harporis findings show <scan_id> --format pdf > /tmp/all.pdf   # still all severities
```

Record the result in the commit message / PR description.

- [ ] **Step 5: Commit**

```bash
git add services/cli/internal/cmd/findings.go
git commit -m "feat(cli): findings show --severity regenerates binary formats via writer-rebuild"
```

---

## Task 7: Document the `severities` key + update help

**Files:**
- Modify: `services/writer/config/writer.yaml`
- Modify: `services/cli/internal/cmd/findings.go` (show `Long` help text)

- [ ] **Step 1: Add the config key with explanation**

In `services/writer/config/writer.yaml`, add after `mask_secrets`:

```yaml
# Severity filter. When non-empty, ONLY findings whose severity is listed
# reach the sinks — applied before fan-out, so it affects all formats
# uniformly. Empty (default) = no filter, every finding written.
# Valid levels (case-insensitive): LOW, MEDIUM, HIGH, CRITICAL.
# Example: severities: [CRITICAL, HIGH]
severities: []
```

- [ ] **Step 2: Mention `--severity` in show help**

In `newFindingsShowCmd`'s `Long` string, append a line after the format list:

```go
				"\nUse --severity CRITICAL,HIGH to keep only those levels (text " +
				"formats filtered in-process; binary formats regenerated via " +
				"writer-rebuild, leaving the on-disk report untouched).",
```

- [ ] **Step 3: Build + full test sweep**

Run:
```bash
cd contracts && go test ./severity/...
cd ../services/writer && go test ./... && go build ./...
cd ../cli && go test ./... && go build ./...
```
Expected: all PASS, clean builds.

- [ ] **Step 4: Commit**

```bash
git add services/writer/config/writer.yaml services/cli/internal/cmd/findings.go
git commit -m "docs(writer,cli): document severities config + --severity flag"
```

---

## Self-Review notes (addressed during planning)

- **Spec coverage:** writer-time filter (Task 3), read-time text (Task 5), read-time binary via rebuild (Task 6), shared parser (Task 1), config default + validation (Task 2), metric (Task 3), docs/recipe (Task 7). All spec sections mapped.
- **No proto/getter/scanner changes** — consistent with spec D2.
- **Validation lives in `load.go`**, not a `validate.go` (writer has no separate validate file) — corrected from the spec's generic wording.
- **Parser placed in `contracts/severity`** (not `kit/`) because `kit/go.mod` does not depend on `contracts`, and the `Severity` enum lives in `contracts` — mirrors `contracts/scanstate`. This refines spec's "kit/" note.
- **Type consistency:** `severity.Set`, `Contains`, `ParseSet`, `ParseCSV`, `SeveritySet()`, `filterNDJSONBySeverity`, `regenProxyWithSeverity`, `SinkSeverityDropped`, `replay(ctx, io.Reader, sink, scanID, severity.Set)` used consistently across tasks.
- **Known limitation (documented in Task 6):** `--severity` on a binary format requires docker-compose mode; `--output-dir` local mode returns a clear error.
```
