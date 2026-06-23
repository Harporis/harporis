// Package findings reads and parses the writer's per-scan NDJSON findings
// report. It is the shared source for both the `findings` CLI command and
// the interactive watch Findings tab (which cannot import the cmd package).
package findings

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode"
)

// Finding is one parsed NDJSON record. The fields mirror the writer's
// protojson output (snake_case); matched_secret is base64 over the wire.
type Finding struct {
	ScanID        string `json:"scan_id"`
	FindingID     string `json:"finding_id"`
	RuleID        string `json:"rule_id"`
	Severity      string `json:"severity"`
	FilePath      string `json:"file_path"`
	LineNumber    int    `json:"line_number"`
	LineNumberEnd int    `json:"line_number_end"`
	MatchedSecret string `json:"matched_secret"`
	Refs          []struct {
		Path string `json:"path"`
	} `json:"refs"`
}

// Location renders "path:line" (or "path", or "-"), preferring file_path and
// falling back to the first ref's path.
func (f Finding) Location() string {
	switch {
	case f.FilePath != "" && f.LineNumber > 0:
		return fmt.Sprintf("%s:%d", f.FilePath, f.LineNumber)
	case f.FilePath != "":
		return f.FilePath
	case len(f.Refs) > 0 && f.Refs[0].Path != "":
		if f.LineNumber > 0 {
			return fmt.Sprintf("%s:%d", f.Refs[0].Path, f.LineNumber)
		}
		return f.Refs[0].Path
	default:
		return "-"
	}
}

// SecretPreview decodes the base64 matched_secret and truncates to max
// printable chars ("-" when empty).
func (f Finding) SecretPreview(max int) string { return DecodeAndPreview(f.MatchedSecret, max) }

// SeverityRank maps a severity string to an order (CRITICAL=4 .. LOW=1,
// unknown=0). Case-insensitive.
func SeverityRank(s string) int {
	switch strings.ToUpper(s) {
	case "CRITICAL":
		return 4
	case "HIGH":
		return 3
	case "MEDIUM":
		return 2
	case "LOW":
		return 1
	}
	return 0
}

// Parse decodes NDJSON (one Finding per line) from r. Blank and malformed
// lines are skipped — partial reports never abort the whole parse.
func Parse(r io.Reader) ([]Finding, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var out []Finding
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var f Finding
		if err := jsonUnmarshal(line, &f); err != nil {
			continue
		}
		out = append(out, f)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read findings: %w", err)
	}
	return out, nil
}

// DecodeAndPreview decodes a base64 secret and truncates to max printable
// chars. Non-printable runes collapse to '.'; "-" for empty.
func DecodeAndPreview(b64 string, max int) string {
	if b64 == "" {
		return "-"
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return truncSecret(b64, max) + " (raw)"
	}
	return truncSecret(string(raw), max)
}

// TruncateLine sanitizes and clips s to max printable chars, without any
// base64 decoding — used to display a raw (e.g. malformed) line.
func TruncateLine(s string, max int) string { return truncSecret(s, max) }

func truncSecret(s string, max int) string {
	var b strings.Builder
	b.Grow(min(len(s), max))
	for i, r := range s {
		if i >= max {
			b.WriteString("…")
			break
		}
		if r == '�' || !unicode.IsPrint(r) {
			b.WriteByte('.')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func jsonUnmarshal(line string, v any) error { return json.Unmarshal([]byte(line), v) }
