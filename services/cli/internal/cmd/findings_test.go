package cmd

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

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
	const ndjson = `

{this is not json}
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

