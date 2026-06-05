package cmd

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"

	"github.com/spf13/cobra"

	"github.com/Harporis/harporis/services/cli/internal/compose"
)

func newFindingsCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "findings",
		Short: "inspect findings emitted by the writer service",
	}
	c.AddCommand(newFindingsShowCmd())
	c.AddCommand(newFindingsListCmd())
	return c
}

// supportedFormats is the closed set accepted by --format. NDJSON is
// the on-disk source for everything except sarif (which has its own
// .sarif file written by the writer); the others transform NDJSON.
var supportedFormats = []string{"ndjson", "pretty", "sarif", "json", "csv", "md"}

func newFindingsShowCmd() *cobra.Command {
	var outputDir string
	var pretty bool
	var format string
	c := &cobra.Command{
		Use:   "show <scan_id>",
		Short: "print findings for a scan in the requested format",
		Long: "Renders findings for a scan_id. The writer materializes NDJSON + " +
			"SARIF files; --format controls how the CLI presents them.\n\n" +
			"Supported formats: " + strings.Join(supportedFormats, ", ") + ".\n" +
			"  ndjson  one protojson-encoded Finding per line (default; jq-friendly)\n" +
			"  pretty  tab-aligned table with decoded matched_secret\n" +
			"  sarif   SARIF v2.1.0 report (cat of writer's <scan_id>.sarif)\n" +
			"  json    pretty-printed JSON array (machine-readable, no streaming)\n" +
			"  csv     CSV row per finding: severity,rule,path,line,secret\n" +
			"  md      Markdown table (good for PR/issue comments)",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			scanID := args[0]
			if !validScanID(scanID) {
				return fmt.Errorf("invalid scan_id %q (use UUID-ish chars only)", scanID)
			}
			// --pretty is the deprecated single-purpose form; it loses
			// to an explicit --format. Keeping it avoids breaking
			// callers that scripted --pretty before --format existed.
			if pretty && format == "ndjson" {
				format = "pretty"
			}
			if !slices.Contains(supportedFormats, format) {
				return fmt.Errorf("unknown --format %q (want one of: %s)", format, strings.Join(supportedFormats, ", "))
			}

			ext := ".ndjson"
			if format == "sarif" {
				ext = ".sarif"
			}
			body, err := readFindingsFile(scanID, ext, outputDir)
			if err != nil {
				return err
			}

			switch format {
			case "ndjson", "sarif":
				// Stream raw — these are already the requested format on disk.
				if _, err := cmd.OutOrStdout().Write([]byte(body)); err != nil {
					return err
				}
				if body != "" && !strings.HasSuffix(body, "\n") {
					fmt.Fprintln(cmd.OutOrStdout())
				}
				return nil
			case "pretty":
				return renderPrettyFindings(strings.NewReader(body), cmd.OutOrStdout())
			case "json":
				return renderJSON(strings.NewReader(body), cmd.OutOrStdout())
			case "csv":
				return renderCSV(strings.NewReader(body), cmd.OutOrStdout())
			case "md":
				return renderMarkdown(strings.NewReader(body), cmd.OutOrStdout())
			default:
				return fmt.Errorf("unhandled format %q", format) // unreachable
			}
		},
	}
	c.Flags().StringVar(&outputDir, "output-dir", "", "read findings files from a host path instead of `docker compose exec writer cat`")
	c.Flags().BoolVar(&pretty, "pretty", false, "(deprecated) shorthand for --format pretty")
	c.Flags().StringVarP(&format, "format", "f", "ndjson", "output format: "+strings.Join(supportedFormats, "|"))
	return c
}

// readFindingsFile returns the contents of <output_dir>/<scan_id><ext>
// either from a host directory (--output-dir) or via
// `docker compose exec writer cat`. ext is ".ndjson" or ".sarif".
func readFindingsFile(scanID, ext, outputDir string) (string, error) {
	if outputDir != "" {
		b, err := os.ReadFile(outputDir + "/" + scanID + ext)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", scanID+ext, err)
		}
		return string(b), nil
	}
	path := "/var/lib/harporis/findings/" + scanID + ext
	co, err := compose.NewDefault()
	if err != nil {
		return "", fmt.Errorf("docker compose not available: %w (pass --output-dir for host file access)", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	body, err := co.Exec(ctx, "writer", "cat", path)
	if err != nil {
		detail := strings.TrimSpace(body)
		if detail == "" {
			detail = err.Error()
		}
		return "", fmt.Errorf("compose exec writer cat %s: %s", path, detail)
	}
	return body, nil
}

func newFindingsListCmd() *cobra.Command {
	var outputDir string
	c := &cobra.Command{
		Use:   "list",
		Short: "list scan_ids the writer has materialized findings for",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if outputDir != "" {
				return listLocalDir(outputDir, cmd.OutOrStdout())
			}
			co, err := compose.NewDefault()
			if err != nil {
				return fmt.Errorf("docker compose not available: %w (pass --output-dir for host file access)", err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			body, err := co.Exec(ctx, "writer", "ls", "-1", "/var/lib/harporis/findings")
			if err != nil {
				detail := strings.TrimSpace(body)
				if detail == "" {
					detail = err.Error()
				}
				return fmt.Errorf("compose exec writer ls: %s", detail)
			}
			body = strings.TrimSpace(body)
			if body == "" {
				fmt.Fprintln(cmd.OutOrStdout(), "(no findings yet)")
				return nil
			}
			for _, name := range strings.Split(body, "\n") {
				name = strings.TrimSpace(name)
				fmt.Fprintln(cmd.OutOrStdout(), strings.TrimSuffix(name, ".ndjson"))
			}
			return nil
		},
	}
	c.Flags().StringVar(&outputDir, "output-dir", "", "list NDJSON files in a host path instead of `docker compose exec writer ls`")
	return c
}

func listLocalDir(dir string, w io.Writer) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read dir %s: %w", dir, err)
	}
	any := false
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".ndjson") {
			continue
		}
		fmt.Fprintln(w, strings.TrimSuffix(name, ".ndjson"))
		any = true
	}
	if !any {
		fmt.Fprintln(w, "(no findings yet)")
	}
	return nil
}

// prettyFinding is the subset of the writer's NDJSON shape that
// --pretty renders. protojson emits snake_case field names (the writer
// sets UseProtoNames: true), so the json tags here match.
type prettyFinding struct {
	ScanID        string `json:"scan_id"`
	FindingID     string `json:"finding_id"`
	RuleID        string `json:"rule_id"`
	Severity      string `json:"severity"`
	FilePath      string `json:"file_path"`
	LineNumber    int    `json:"line_number"`
	LineNumberEnd int    `json:"line_number_end"`
	MatchedSecret string `json:"matched_secret"` // base64-encoded (bytes type in proto)
	Refs          []struct {
		Path string `json:"path"`
	} `json:"refs"`
}

// renderPrettyFindings reads NDJSON from r and writes a tab-aligned
// human-readable table to w. Each finding becomes one row; the secret
// preview is the decoded matched_secret truncated to ~48 chars with
// non-printable runes replaced.
func renderPrettyFindings(r io.Reader, w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SEVERITY\tRULE\tPATH:LINE\tSECRET")
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var count int
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var f prettyFinding
		if err := json.Unmarshal([]byte(line), &f); err != nil {
			fmt.Fprintf(tw, "?\tparse-error\t-\t%s\n", truncSecret(line, 48))
			count++
			continue
		}
		loc := formatLocation(f)
		secret := decodeAndPreview(f.MatchedSecret, 48)
		rule := f.RuleID
		if rule == "" {
			rule = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", f.Severity, rule, loc, secret)
		count++
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("read findings: %w", err)
	}
	if count == 0 {
		fmt.Fprintln(tw, "(no findings)")
	}
	return tw.Flush()
}

func formatLocation(f prettyFinding) string {
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

func decodeAndPreview(b64 string, max int) string {
	if b64 == "" {
		return "-"
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return truncSecret(b64, max) + " (raw)"
	}
	return truncSecret(string(raw), max)
}

// truncSecret replaces non-printable runes with '.' and clips to max
// chars. Newline/tab/CR all collapse to '.' so the table stays aligned.
func truncSecret(s string, max int) string {
	var b strings.Builder
	b.Grow(min(len(s), max))
	for i, r := range s {
		if i >= max {
			b.WriteString("…")
			break
		}
		if r == utf8Replacement || !unicode.IsPrint(r) {
			b.WriteByte('.')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

const utf8Replacement = '�'

// parseNDJSON decodes the writer's NDJSON output into prettyFinding
// values, skipping blank lines. Lines that fail to unmarshal are
// returned as zero-value entries with ScanID == "" so callers can
// surface them with a "parse-error" row instead of aborting the whole
// render. Returns the bufio.Scanner error if the stream itself broke.
func parseNDJSON(r io.Reader) ([]prettyFinding, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var out []prettyFinding
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var f prettyFinding
		if err := json.Unmarshal([]byte(line), &f); err != nil {
			out = append(out, prettyFinding{RuleID: "parse-error", Severity: "?"})
			continue
		}
		out = append(out, f)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read findings: %w", err)
	}
	return out, nil
}

// renderJSON pretty-prints findings as a single JSON array. Each
// matched_secret is decoded from base64 into a UTF-8 string so the
// output is human-eyeballable without an external decode step.
// Suitable for piping to jq, but `cat <id>.ndjson` is faster if you
// want streaming.
func renderJSON(r io.Reader, w io.Writer) error {
	findings, err := parseNDJSON(r)
	if err != nil {
		return err
	}
	type out struct {
		ScanID        string `json:"scan_id,omitempty"`
		FindingID     string `json:"finding_id,omitempty"`
		RuleID        string `json:"rule_id"`
		Severity      string `json:"severity"`
		// Path is FilePath if set; otherwise falls back to Refs[0].Path
		// so BLOB-source findings (where the path lives in Refs) carry
		// the same information as DIFF_WINDOW-source ones.
		Path          string `json:"path,omitempty"`
		LineNumber    int    `json:"line_number,omitempty"`
		LineNumberEnd int    `json:"line_number_end,omitempty"`
		MatchedSecret string `json:"matched_secret,omitempty"`
	}
	rows := make([]out, 0, len(findings))
	for _, f := range findings {
		path := f.FilePath
		if path == "" && len(f.Refs) > 0 {
			path = f.Refs[0].Path
		}
		rows = append(rows, out{
			ScanID: f.ScanID, FindingID: f.FindingID,
			RuleID: f.RuleID, Severity: f.Severity,
			Path: path, LineNumber: f.LineNumber, LineNumberEnd: f.LineNumberEnd,
			MatchedSecret: decodeRaw(f.MatchedSecret),
		})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(rows)
}

// renderCSV emits one row per finding with severity,rule,path,line,secret.
// The secret column is the decoded matched_secret with control characters
// stripped (CR/LF/etc would break CSV column alignment).
func renderCSV(r io.Reader, w io.Writer) error {
	findings, err := parseNDJSON(r)
	if err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	defer cw.Flush()
	if err := cw.Write([]string{"severity", "rule", "path", "line", "secret"}); err != nil {
		return err
	}
	for _, f := range findings {
		secret := stripControl(decodeRaw(f.MatchedSecret))
		line := ""
		if f.LineNumber > 0 {
			line = fmt.Sprintf("%d", f.LineNumber)
		}
		path := f.FilePath
		if path == "" && len(f.Refs) > 0 {
			path = f.Refs[0].Path
		}
		if err := cw.Write([]string{f.Severity, f.RuleID, path, line, secret}); err != nil {
			return err
		}
	}
	return cw.Error()
}

// renderMarkdown emits a GitHub-flavoured Markdown table. Same shape as
// renderCSV but designed to paste cleanly into a PR comment or issue.
// Pipe characters inside the secret column are escaped so they don't
// break the table.
func renderMarkdown(r io.Reader, w io.Writer) error {
	findings, err := parseNDJSON(r)
	if err != nil {
		return err
	}
	fmt.Fprintln(w, "| Severity | Rule | Path:Line | Secret |")
	fmt.Fprintln(w, "|---|---|---|---|")
	if len(findings) == 0 {
		fmt.Fprintln(w, "| _(no findings)_ |  |  |  |")
		return nil
	}
	for _, f := range findings {
		secret := stripControl(decodeRaw(f.MatchedSecret))
		secret = strings.ReplaceAll(secret, "|", "\\|")
		loc := formatLocation(f)
		rule := f.RuleID
		if rule == "" {
			rule = "-"
		}
		fmt.Fprintf(w, "| %s | %s | %s | %s |\n", f.Severity, rule, loc, secret)
	}
	return nil
}

// decodeRaw is the same idea as decodeAndPreview but without truncation
// or non-printable replacement. The CSV/Markdown callers want the full
// secret (stripControl handles only newlines/tabs that would break the
// table format).
func decodeRaw(b64 string) string {
	if b64 == "" {
		return ""
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return b64
	}
	return string(raw)
}

func stripControl(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == '\r' || r == '\n' || r == '\t' {
			b.WriteRune(' ')
			continue
		}
		if !unicode.IsPrint(r) {
			b.WriteByte('.')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// validScanID mirrors getter's ValidateScanID alphabet exactly
// ([A-Za-z0-9_-], length ≤128) so the CLI rejects any scan_id the
// server would. Period is NOT allowed even though `cat` via docker
// compose exec uses argv (not a shell) — keeping the validator
// synchronized prevents user confusion when CLI accepts a string the
// rest of the pipeline rejects.
func validScanID(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z',
			r >= 'a' && r <= 'z',
			r >= '0' && r <= '9',
			r == '-', r == '_':
			continue
		default:
			return false
		}
	}
	return true
}
