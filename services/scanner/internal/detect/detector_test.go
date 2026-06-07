package detect

import (
	"regexp"
	"testing"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/services/scanner/internal/rules"
)

func chunkWithLines(lines ...string) *v1.GitRowChunk {
	rows := make([]*v1.GitRow, len(lines))
	for i, s := range lines {
		rows[i] = &v1.GitRow{
			LineNumber: int32(i + 1),
			ByteOffset: int64(0),
			Content:    []byte(s),
		}
	}
	return &v1.GitRowChunk{
		ScanId:   "scan-1",
		ChunkId:  "chunk-1",
		Kind:     v1.ChunkKind_BLOB,
		FilePath: "test.txt",
		Rows:     rows,
	}
}

func ruleAKIA() rules.Rule {
	return rules.Rule{
		ID:          "aws-access-key-id",
		Description: "AWS Access Key ID",
		Severity:    v1.Severity_HIGH,
		Regex:       regexp.MustCompile(`AKIA[A-Z0-9]{16}`),
	}
}

func TestScanChunk_EmitsFindingForRegexMatch(t *testing.T) {
	d := NewDetector([]rules.Rule{ruleAKIA()}, "scanner/test")
	c := chunkWithLines("# fixture", "aws_key = AKIAIOSFODNN7EXAMPLE", "end")
	got := d.ScanChunk(c)
	if len(got) != 1 {
		t.Fatalf("got %d findings, want 1", len(got))
	}
	f := got[0]
	if f.RuleId != "aws-access-key-id" || f.Severity != v1.Severity_HIGH {
		t.Errorf("finding rule/severity wrong: %+v", f)
	}
	if f.LineNumber != 2 || f.LineNumberEnd != 2 {
		t.Errorf("line_number/line_number_end = %d/%d, want 2/2", f.LineNumber, f.LineNumberEnd)
	}
	if string(f.MatchedSecret) != "AKIAIOSFODNN7EXAMPLE" {
		t.Errorf("matched_secret = %q, want AKIAIOSFODNN7EXAMPLE", f.MatchedSecret)
	}
	if f.ScanId != "scan-1" || f.ChunkId != "chunk-1" {
		t.Errorf("finding scan/chunk IDs wrong: %+v", f)
	}
	if f.DetectorVersion != "scanner/test" {
		t.Errorf("detector_version = %q, want scanner/test", f.DetectorVersion)
	}
}

func TestScanChunk_MultilinePEMSpansLines(t *testing.T) {
	r := rules.Rule{
		ID:       "private-key-pem",
		Severity: v1.Severity_CRITICAL,
		Regex:    regexp.MustCompile(`(?s)-----BEGIN PRIVATE KEY-----.*?-----END PRIVATE KEY-----`),
	}
	d := NewDetector([]rules.Rule{r}, "scanner/test")
	c := chunkWithLines(
		"prefix",
		"-----BEGIN PRIVATE KEY-----",
		"MIIE",
		"-----END PRIVATE KEY-----",
		"suffix",
	)
	got := d.ScanChunk(c)
	if len(got) != 1 {
		t.Fatalf("got %d findings, want 1", len(got))
	}
	if got[0].LineNumber != 2 || got[0].LineNumberEnd != 4 {
		t.Errorf("line range = %d-%d, want 2-4", got[0].LineNumber, got[0].LineNumberEnd)
	}
}

func TestScanChunk_EntropyFilterDropsLowEntropyMatches(t *testing.T) {
	r := rules.Rule{
		ID:         "generic",
		Severity:   v1.Severity_LOW,
		Regex:      regexp.MustCompile(`(?i)(token)=([A-Za-z0-9]+)`),
		EntropyMin: 3.5,
		EntropyGrp: 2,
	}
	d := NewDetector([]rules.Rule{r}, "scanner/test")

	// Low entropy: should be dropped.
	low := chunkWithLines("token=aaaaaaaaaa")
	if got := d.ScanChunk(low); len(got) != 0 {
		t.Errorf("low-entropy match: got %d findings, want 0", len(got))
	}

	// High entropy: should fire.
	high := chunkWithLines("token=3xq8Z1nQpvP7tkPlusEntropyHere")
	got := d.ScanChunk(high)
	if len(got) != 1 {
		t.Fatalf("high-entropy: got %d findings, want 1", len(got))
	}
	if got[0].EntropyScore < 3.5 {
		t.Errorf("entropy_score = %v, want >= 3.5", got[0].EntropyScore)
	}
}

func TestScanChunk_BLOBVsDIFFWINDOWLocationFields(t *testing.T) {
	r := ruleAKIA()
	d := NewDetector([]rules.Rule{r}, "scanner/test")

	// BLOB: file_path/commit_sha empty, refs populated.
	blob := chunkWithLines("AKIAIOSFODNN7EXAMPLE")
	blob.Kind = v1.ChunkKind_BLOB
	blob.FilePath = ""
	blob.Refs = []*v1.CommitFileRef{
		{CommitSha: []byte{1, 2, 3}, Path: "a.txt", Timestamp: 1000},
		{CommitSha: []byte{4, 5, 6}, Path: "b.txt", Timestamp: 2000},
	}
	got := d.ScanChunk(blob)
	if len(got) != 1 {
		t.Fatalf("BLOB: got %d findings, want 1", len(got))
	}
	if got[0].FilePath != "" || len(got[0].CommitSha) != 0 {
		t.Error("BLOB finding should have empty file_path/commit_sha")
	}
	if len(got[0].Refs) != 2 {
		t.Errorf("BLOB finding refs = %d, want 2", len(got[0].Refs))
	}

	// DIFF_WINDOW: file_path/commit_sha populated, refs empty.
	diff := chunkWithLines("AKIAIOSFODNN7EXAMPLE")
	diff.Kind = v1.ChunkKind_DIFF_WINDOW
	diff.FilePath = "src/foo.go"
	diff.CommitSha = []byte{0xab, 0xcd}
	got = d.ScanChunk(diff)
	if len(got) != 1 {
		t.Fatalf("DIFF: got %d findings, want 1", len(got))
	}
	if got[0].FilePath != "src/foo.go" || len(got[0].CommitSha) != 2 {
		t.Errorf("DIFF finding location fields wrong: %+v", got[0])
	}
	if len(got[0].Refs) != 0 {
		t.Error("DIFF finding should have empty refs")
	}
}

func TestScanChunk_MatchedSecretUsesCaptureGroup(t *testing.T) {
	// Rule mirrors the production aws-access-key-id pattern (boundary class
	// + secret capture group). SecretGrp=1 says "emit only group 1, not the
	// boundary + content".
	r := rules.Rule{
		ID:        "aws-access-key-id",
		Severity:  v1.Severity_HIGH,
		Regex:     regexp.MustCompile(`(?:^|[^A-Z0-9])((?:AKIA|ASIA)[A-Z0-9]{16})(?:[^A-Z0-9]|$)`),
		SecretGrp: 1,
	}
	d := NewDetector([]rules.Rule{r}, "scanner/test")
	c := chunkWithLines("aws_key=AKIAIOSFODNN7EXAMPLE")
	got := d.ScanChunk(c)
	if len(got) != 1 {
		t.Fatalf("got %d findings, want 1", len(got))
	}
	if want := "AKIAIOSFODNN7EXAMPLE"; string(got[0].MatchedSecret) != want {
		t.Errorf("MatchedSecret = %q, want %q", got[0].MatchedSecret, want)
	}
}

func TestScanChunk_MatchedSecretDefaultsToFullMatch(t *testing.T) {
	// No SecretGrp → MatchedSecret is the full regex match (backward compat).
	r := rules.Rule{
		ID:       "test",
		Severity: v1.Severity_HIGH,
		Regex:    regexp.MustCompile(`AKIA[A-Z0-9]{16}`),
	}
	d := NewDetector([]rules.Rule{r}, "scanner/test")
	c := chunkWithLines("AKIAIOSFODNN7EXAMPLE")
	got := d.ScanChunk(c)
	if len(got) != 1 {
		t.Fatalf("got %d findings, want 1", len(got))
	}
	if want := "AKIAIOSFODNN7EXAMPLE"; string(got[0].MatchedSecret) != want {
		t.Errorf("MatchedSecret = %q, want %q", got[0].MatchedSecret, want)
	}
}

func TestScanChunk_ContextLines_WindowAndEdgeClamp(t *testing.T) {
	d := NewDetector([]rules.Rule{ruleAKIA()}, "scanner/test")
	// Match on row 3 (0-indexed 2). With N=2 we expect rows 1..2 before
	// and rows 4..5 after.
	c := chunkWithLines(
		"line1",
		"line2",
		"aws_key = AKIAIOSFODNN7EXAMPLE",
		"line4",
		"line5",
		"line6",
	)
	c.OutputContextLines = 2
	got := d.ScanChunk(c)
	if len(got) != 1 {
		t.Fatalf("want 1 finding, got %d", len(got))
	}
	f := got[0]
	if want := []string{"line1", "line2"}; !bytesEq(f.ContextBefore, want) {
		t.Fatalf("ContextBefore = %v, want %v", asStrings(f.ContextBefore), want)
	}
	if want := []string{"line4", "line5"}; !bytesEq(f.ContextAfter, want) {
		t.Fatalf("ContextAfter = %v, want %v", asStrings(f.ContextAfter), want)
	}

	// Edge clamp: match on FIRST row with N=3 → ContextBefore empty, ContextAfter still up to 3.
	cFirst := chunkWithLines("aws_key = AKIAIOSFODNN7EXAMPLE", "a", "b")
	cFirst.OutputContextLines = 3
	got = d.ScanChunk(cFirst)
	if len(got[0].ContextBefore) != 0 {
		t.Fatalf("ContextBefore on edge match must be empty, got %v", asStrings(got[0].ContextBefore))
	}
	if want := []string{"a", "b"}; !bytesEq(got[0].ContextAfter, want) {
		t.Fatalf("ContextAfter on edge = %v, want %v", asStrings(got[0].ContextAfter), want)
	}

	// N=0 (default) → no context.
	cNone := chunkWithLines("x", "AKIAIOSFODNN7EXAMPLE", "y")
	got = d.ScanChunk(cNone)
	if got[0].ContextBefore != nil || got[0].ContextAfter != nil {
		t.Fatalf("N=0 must produce nil arrays, got before=%v after=%v",
			asStrings(got[0].ContextBefore), asStrings(got[0].ContextAfter))
	}
}

func bytesEq(got [][]byte, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if string(got[i]) != want[i] {
			return false
		}
	}
	return true
}

func asStrings(b [][]byte) []string {
	out := make([]string, len(b))
	for i := range b {
		out[i] = string(b[i])
	}
	return out
}

func TestScanChunk_NoMatchesReturnsEmpty(t *testing.T) {
	d := NewDetector([]rules.Rule{ruleAKIA()}, "scanner/test")
	c := chunkWithLines("nothing", "interesting", "here")
	got := d.ScanChunk(c)
	if len(got) != 0 {
		t.Errorf("got %d findings, want 0", len(got))
	}
}
