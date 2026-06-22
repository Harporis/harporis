package findings

import (
	"strings"
	"testing"
)

func TestParseAndAccessors(t *testing.T) {
	// One valid finding (matched_secret is base64 of "AKIAIOSFODNN7EXAMPLE"),
	// one blank line, one malformed line (must be skipped).
	nd := `{"rule_id":"aws-access-key-id","severity":"HIGH","file_path":"docs/x.md","line_number":106,"matched_secret":"QUtJQUlPU0ZPRE5ON0VYQU1QTEU="}

not-json
{"rule_id":"private-key-pem","severity":"CRITICAL","refs":[{"path":"a/b.go"}],"line_number":78,"matched_secret":""}
`
	got, err := Parse(strings.NewReader(nd))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 parsed (malformed skipped), got %d", len(got))
	}
	if got[0].Location() != "docs/x.md:106" {
		t.Fatalf("location: %q", got[0].Location())
	}
	if got[0].SecretPreview(48) != "AKIAIOSFODNN7EXAMPLE" {
		t.Fatalf("secret preview: %q", got[0].SecretPreview(48))
	}
	// Falls back to refs[0].path when file_path is empty.
	if got[1].Location() != "a/b.go:78" {
		t.Fatalf("ref location: %q", got[1].Location())
	}
	// Empty secret renders as "-".
	if got[1].SecretPreview(48) != "-" {
		t.Fatalf("empty secret: %q", got[1].SecretPreview(48))
	}
}

func TestSeverityRank(t *testing.T) {
	if SeverityRank("CRITICAL") <= SeverityRank("HIGH") ||
		SeverityRank("HIGH") <= SeverityRank("MEDIUM") ||
		SeverityRank("MEDIUM") <= SeverityRank("LOW") ||
		SeverityRank("LOW") <= SeverityRank("nonsense") {
		t.Fatal("severity rank must order CRITICAL>HIGH>MEDIUM>LOW>unknown")
	}
	if SeverityRank("critical") != SeverityRank("CRITICAL") {
		t.Fatal("rank must be case-insensitive")
	}
}
