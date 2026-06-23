package cmd

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/contracts/severity"
	"github.com/Harporis/harporis/services/cli/internal/findings"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestResolveFindingsName(t *testing.T) {
	const scan = "3258dee2-475c-417a-a3c7-917b98e205c1"
	cases := []struct {
		name  string
		names []string
		ext   string
		want  string
	}{
		{
			name:  "exact bare name (single-pool mode)",
			names: []string{scan + ".ndjson", scan + ".pdf"},
			ext:   ".pdf",
			want:  scan + ".pdf",
		},
		{
			name:  "replica-suffixed name (HARPORIS_FINDINGS_SHARDS=1)",
			names: []string{scan + ".ndjson", scan + ".3bad9a0183a5.pdf"},
			ext:   ".pdf",
			want:  scan + ".3bad9a0183a5.pdf",
		},
		{
			name:  "bare preferred over suffixed when both present",
			names: []string{scan + ".pdf", scan + ".3bad9a0183a5.pdf"},
			ext:   ".pdf",
			want:  scan + ".pdf",
		},
		{
			name:  "ignores tempfiles and other extensions",
			names: []string{scan + ".sarif", scan + ".sarif.tmp-99", scan + ".ndjson"},
			ext:   ".sarif",
			want:  scan + ".sarif",
		},
		{
			name:  "no match returns empty",
			names: []string{scan + ".ndjson", scan + ".sarif"},
			ext:   ".pdf",
			want:  "",
		},
		{
			name:  "does not match a different scan that shares no prefix",
			names: []string{"other-scan.pdf"},
			ext:   ".pdf",
			want:  "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := findings.ResolveFindingsName(tc.names, scan, tc.ext)
			if got != tc.want {
				t.Errorf("resolveFindingsName(%v, %q, %q) = %q, want %q", tc.names, scan, tc.ext, got, tc.want)
			}
		})
	}
}

func TestRenderPrettyFindings_TableShapeAndDecodedSecret(t *testing.T) {
	// Two findings: one with file_path + matched_secret (base64-encoded
	// "AKIAIOSFODNN7EXAMPLE"); one with empty file_path but Refs.
	const ndjson = `{"scan_id":"s","finding_id":"f1","rule_id":"aws-key","severity":"CRITICAL","file_path":"src/.env","line_number":3,"matched_secret":"QUtJQUlPU0ZPRE5ON0VYQU1QTEU="}
{"scan_id":"s","finding_id":"f2","rule_id":"pem","severity":"HIGH","refs":[{"path":"keys/id_rsa"}],"matched_secret":""}`

	var buf bytes.Buffer
	if err := renderPrettyFindings(strings.NewReader(ndjson), &buf); err != nil {
		t.Fatalf("renderPrettyFindings: %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, "SEVERITY") || !strings.Contains(out, "SECRET") {
		t.Errorf("missing header: %q", out)
	}
	if !strings.Contains(out, "CRITICAL") || !strings.Contains(out, "aws-key") {
		t.Errorf("missing first row fields: %q", out)
	}
	if !strings.Contains(out, "src/.env:3") {
		t.Errorf("missing path:line: %q", out)
	}
	if !strings.Contains(out, "AKIAIOSFODNN7EXAMPLE") {
		t.Errorf("matched_secret was not base64-decoded into the table: %q", out)
	}
	if !strings.Contains(out, "keys/id_rsa") {
		t.Errorf("fallback to Refs path failed: %q", out)
	}
}

func TestRenderPrettyFindings_HandlesEmptyAndMalformed(t *testing.T) {
	// "not json {{{"  is clearly not valid base64, so the old buggy path
	// (DecodeAndPreview) would have appended " (raw)". The fixed path
	// (TruncateLine) must NOT append that suffix.
	const ndjson = `

not json {{{
{"scan_id":"s","rule_id":"r","severity":"LOW"}
`
	var buf bytes.Buffer
	if err := renderPrettyFindings(strings.NewReader(ndjson), &buf); err != nil {
		t.Fatalf("renderPrettyFindings: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "parse-error") {
		t.Errorf("malformed line should surface as parse-error row: %q", out)
	}
	if strings.Contains(out, "(raw)") {
		t.Errorf("parse-error row must NOT contain '(raw)' suffix: %q", out)
	}
	if !strings.Contains(out, "LOW") {
		t.Errorf("valid line after malformed should still render: %q", out)
	}
}

func TestRenderPrettyFindings_TruncatesAndSanitizesSecret(t *testing.T) {
	// Long ASCII secret + a control char in the middle.
	long := strings.Repeat("A", 100) + "\x00" + strings.Repeat("B", 10)
	enc := base64.StdEncoding.EncodeToString([]byte(long))
	ndjson := `{"scan_id":"s","rule_id":"r","severity":"LOW","matched_secret":"` + enc + `"}`
	var buf bytes.Buffer
	if err := renderPrettyFindings(strings.NewReader(ndjson), &buf); err != nil {
		t.Fatalf("renderPrettyFindings: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "…") {
		t.Errorf("expected truncation marker '…' in %q", out)
	}
	if strings.Contains(out, "\x00") {
		t.Errorf("control char should be sanitized: %q", out)
	}
}

// sampleNDJSON is the two-line fixture reused by the format renderers.
// "QUtJQUlPU0ZPRE5ON0VYQU1QTEU=" decodes to "AKIAIOSFODNN7EXAMPLE".
const sampleNDJSON = `{"scan_id":"s","finding_id":"f1","rule_id":"aws-key","severity":"CRITICAL","file_path":"src/.env","line_number":3,"matched_secret":"QUtJQUlPU0ZPRE5ON0VYQU1QTEU="}
{"scan_id":"s","finding_id":"f2","rule_id":"pem","severity":"HIGH","refs":[{"path":"keys/id_rsa"}],"matched_secret":""}`

func TestRenderJSON_PrettyArrayWithDecodedSecret(t *testing.T) {
	var buf bytes.Buffer
	if err := renderJSON(strings.NewReader(sampleNDJSON), &buf); err != nil {
		t.Fatalf("renderJSON: %v", err)
	}
	out := buf.String()
	if !strings.HasPrefix(strings.TrimSpace(out), "[") || !strings.HasSuffix(strings.TrimSpace(out), "]") {
		t.Errorf("output is not a JSON array: %q", out)
	}
	if !strings.Contains(out, `"matched_secret": "AKIAIOSFODNN7EXAMPLE"`) {
		t.Errorf("matched_secret was not base64-decoded in JSON output: %q", out)
	}
	if !strings.Contains(out, `"rule_id": "aws-key"`) {
		t.Errorf("expected rule_id in JSON output: %q", out)
	}
	if !strings.Contains(out, `"path": "src/.env"`) {
		t.Errorf("expected DIFF_WINDOW path in JSON output: %q", out)
	}
	if !strings.Contains(out, `"path": "keys/id_rsa"`) {
		t.Errorf("expected BLOB-source Refs path to surface as path in JSON output: %q", out)
	}
}

func TestRenderCSV_HeaderAndRows(t *testing.T) {
	var buf bytes.Buffer
	if err := renderCSV(strings.NewReader(sampleNDJSON), &buf); err != nil {
		t.Fatalf("renderCSV: %v", err)
	}
	out := buf.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("CSV line count = %d, want 3 (header + 2 findings): %q", len(lines), out)
	}
	if lines[0] != "severity,rule,path,line,secret" {
		t.Errorf("CSV header = %q", lines[0])
	}
	if !strings.Contains(lines[1], "CRITICAL,aws-key,src/.env,3,AKIAIOSFODNN7EXAMPLE") {
		t.Errorf("first row missing expected fields: %q", lines[1])
	}
	if !strings.Contains(lines[2], "HIGH,pem,keys/id_rsa") {
		t.Errorf("second row should fall back to Refs path: %q", lines[2])
	}
}

func TestRenderCSV_StripsControlCharsFromSecret(t *testing.T) {
	long := "AKIA" + "\n" + "EXAMPLE" + "\x00"
	enc := base64.StdEncoding.EncodeToString([]byte(long))
	ndjson := `{"scan_id":"s","rule_id":"r","severity":"LOW","file_path":"x","line_number":1,"matched_secret":"` + enc + `"}`
	var buf bytes.Buffer
	if err := renderCSV(strings.NewReader(ndjson), &buf); err != nil {
		t.Fatalf("renderCSV: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "\n\n") {
		// CSV's encoder is fine with embedded newlines (quoted field), but
		// stripControl should have already collapsed them — the row count
		// stays at 2 (header + 1).
		t.Errorf("expected control chars to be stripped, got embedded newlines: %q", out)
	}
	if strings.Count(out, "\n") != 2 {
		t.Errorf("CSV should have exactly 2 newlines (header + 1 row), got %d in %q", strings.Count(out, "\n"), out)
	}
}

func TestRenderMarkdown_TableShape(t *testing.T) {
	var buf bytes.Buffer
	if err := renderMarkdown(strings.NewReader(sampleNDJSON), &buf); err != nil {
		t.Fatalf("renderMarkdown: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "| Severity |") {
		t.Errorf("missing header row: %q", out)
	}
	if !strings.Contains(out, "|---|---|---|---|") {
		t.Errorf("missing markdown separator row: %q", out)
	}
	if !strings.Contains(out, "| CRITICAL | aws-key | src/.env:3 | AKIAIOSFODNN7EXAMPLE |") {
		t.Errorf("first finding row missing or malformed: %q", out)
	}
}

func TestRenderMarkdown_EscapesPipeInSecret(t *testing.T) {
	enc := base64.StdEncoding.EncodeToString([]byte("a|b|c"))
	ndjson := `{"scan_id":"s","rule_id":"r","severity":"LOW","file_path":"x","line_number":1,"matched_secret":"` + enc + `"}`
	var buf bytes.Buffer
	if err := renderMarkdown(strings.NewReader(ndjson), &buf); err != nil {
		t.Fatalf("renderMarkdown: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `a\|b\|c`) {
		t.Errorf("expected escaped pipes 'a\\|b\\|c' in %q", out)
	}
}

func TestRenderMarkdown_EmptyFindingsRendersPlaceholder(t *testing.T) {
	var buf bytes.Buffer
	if err := renderMarkdown(strings.NewReader(""), &buf); err != nil {
		t.Fatalf("renderMarkdown: %v", err)
	}
	if !strings.Contains(buf.String(), "_(no findings)_") {
		t.Errorf("empty input should render the no-findings placeholder, got %q", buf.String())
	}
}

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

