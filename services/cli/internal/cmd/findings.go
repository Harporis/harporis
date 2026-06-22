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
	"google.golang.org/protobuf/encoding/protojson"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/contracts/severity"
	kitscan "github.com/Harporis/harporis/kit/scan"
	"github.com/Harporis/harporis/services/cli/internal/compose"
	"github.com/Harporis/harporis/services/cli/internal/findings"
)

func newFindingsCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "findings",
		Short: "inspect findings emitted by the writer service",
	}
	c.AddCommand(newFindingsShowCmd())
	c.AddCommand(newFindingsListCmd())
	c.AddCommand(newFindingsRebuildCmd())
	return c
}

// rebuildFormats is the closed set of targets writer-rebuild accepts.
// NDJSON is excluded — it IS the source of truth, can't be a target.
var rebuildFormats = []string{"sarif", "html", "xlsx", "pdf", "parquet"}

func newFindingsRebuildCmd() *cobra.Command {
	var format string
	var severityCSV string
	c := &cobra.Command{
		Use:   "rebuild <scan_id>",
		Short: "reconstruct a stale sink file from the scan's NDJSON",
		Long: "Replays the authoritative <scan_id>.ndjson through the writer's sink " +
			"machinery and writes a fresh <scan_id>.<ext>. Useful when an accumulator " +
			"sink (SARIF/HTML/XLSX/PDF) lost its last batch to a writer crash, or when " +
			"a Parquet tempfile was orphaned before Finalize.\n\n" +
			"Runs via `docker compose exec writer writer-rebuild`, so the rebuilt file " +
			"lands inside the writer container's /var/lib/harporis/findings — the same " +
			"mount the live writer uses.\n\n" +
			"Supported --format values: " + strings.Join(rebuildFormats, ", ") + ".\n\n" +
			"--severity CRITICAL,HIGH rebuilds a filtered report IN PLACE — unlike " +
			"`findings show --severity` (which leaves the on-disk file untouched), this " +
			"overwrites the canonical <scan_id>.<ext> with only the listed levels.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			scanID := args[0]
			if err := kitscan.ValidateScanID(scanID); err != nil {
				return err
			}
			if !slices.Contains(rebuildFormats, format) {
				return fmt.Errorf("unknown --format %q (want one of: %s)", format, strings.Join(rebuildFormats, ", "))
			}
			if _, err := severity.ParseCSV(severityCSV); err != nil {
				return err
			}
			co, err := compose.NewDefault()
			if err != nil {
				return fmt.Errorf("docker compose not available: %w", err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			body, err := co.Exec(ctx, "writer",
				"/usr/local/bin/writer-rebuild",
				"--scan-id", scanID,
				"--format", format,
				"--severity", severityCSV,
			)
			if body != "" {
				_, _ = cmd.OutOrStdout().Write([]byte(body))
				if !strings.HasSuffix(body, "\n") {
					fmt.Fprintln(cmd.OutOrStdout())
				}
			}
			if err != nil {
				return fmt.Errorf("writer-rebuild: %w", err)
			}
			return nil
		},
	}
	c.Flags().StringVarP(&format, "format", "f", "", "target format: "+strings.Join(rebuildFormats, "|")+" (required)")
	c.Flags().StringVar(&severityCSV, "severity", "", "comma-separated severity levels to KEEP (e.g. CRITICAL,HIGH); empty = all. Overwrites the canonical file in place.")
	_ = c.MarkFlagRequired("format")
	return c
}

// supportedFormats is the closed set accepted by --format.
//   - ndjson, pretty, json, csv, md:           derived from <id>.ndjson on disk
//   - sarif, html, xlsx, pdf, parquet:         read straight from the writer's
//     <id>.<ext> file (writer's sink wrote them; CLI just streams)
var supportedFormats = []string{"ndjson", "pretty", "sarif", "json", "csv", "md", "html", "xlsx", "pdf", "parquet"}

// formatToExt maps a --format value to the file extension the writer
// uses. Empty string means "derive from ndjson on the CLI side".
var formatToExt = map[string]string{
	"sarif":   ".sarif",
	"html":    ".html",
	"xlsx":    ".xlsx",
	"pdf":     ".pdf",
	"parquet": ".parquet",
}

func newFindingsShowCmd() *cobra.Command {
	var outputDir string
	var pretty bool
	var format string
	var severityCSV string
	c := &cobra.Command{
		Use:   "show <scan_id>",
		Short: "print findings for a scan in the requested format",
		Long: "Renders findings for a scan_id. The writer materializes one " +
			"file per format (NDJSON+SARIF+HTML+XLSX+PDF by default); --format " +
			"controls how the CLI surfaces them.\n\n" +
			"Supported formats: " + strings.Join(supportedFormats, ", ") + ".\n" +
			"  ndjson  one protojson-encoded Finding per line (default; jq-friendly)\n" +
			"  pretty  tab-aligned table with decoded matched_secret\n" +
			"  sarif   SARIF v2.1.0 report (cat of writer's <scan_id>.sarif)\n" +
			"  json    pretty-printed JSON array (machine-readable, no streaming)\n" +
			"  csv     CSV row per finding: severity,rule,path,line,secret\n" +
			"  md      Markdown table (good for PR/issue comments)\n" +
			"  html    self-contained browser report with sort + filter\n" +
			"  xlsx    Excel workbook (audit/triage in spreadsheets)\n" +
			"  pdf     printable A4 report (formal hand-off / compliance binder)\n" +
			"  parquet columnar workbook (SIEM ingestion, DuckDB/Polars/Athena)" +
			"\nUse --severity CRITICAL,HIGH to keep only those levels (text " +
			"formats filtered in-process; binary formats regenerated via " +
			"writer-rebuild, leaving the on-disk report untouched).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			scanID := args[0]
			if err := kitscan.ValidateScanID(scanID); err != nil {
				return err
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

			sevSet, err := severity.ParseCSV(severityCSV)
			if err != nil {
				return err
			}

			ext := ".ndjson"
			if v, ok := formatToExt[format]; ok {
				ext = v
			}
			var body string
			if _, isProxy := formatToExt[format]; isProxy && len(sevSet) > 0 {
				// Binary/proxy format + severity filter: regenerate a
				// filtered copy from NDJSON via writer-rebuild; the
				// canonical <scan>.<ext> on disk is left untouched.
				body, err = regenProxyWithSeverity(scanID, format, severityCSV, outputDir)
				if err != nil {
					return err
				}
			} else {
				body, err = findings.ReadFile(scanID, ext, outputDir)
				if err != nil {
					return err
				}
				// Text formats derive from NDJSON; filter in-process.
				if _, isProxy := formatToExt[format]; !isProxy {
					body, err = filterNDJSONBySeverity(body, sevSet)
					if err != nil {
						return err
					}
				}
			}

			switch format {
			case "ndjson", "sarif", "html", "xlsx", "pdf", "parquet":
				// Stream raw — writer already wrote this format to disk.
				// xlsx/pdf/parquet are binary; stdout still works (`> file.pdf`).
				if _, err := cmd.OutOrStdout().Write([]byte(body)); err != nil {
					return err
				}
				if (format == "ndjson" || format == "sarif" || format == "html") &&
					body != "" && !strings.HasSuffix(body, "\n") {
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
	c.Flags().StringVar(&severityCSV, "severity", "", "comma-separated severity levels to KEEP (e.g. CRITICAL,HIGH); empty = all")
	return c
}

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
	// Per-invocation temp dir so concurrent `findings show --severity` runs
	// (even for the same scan_id) don't race on the same regenerated file.
	tmpDir := fmt.Sprintf("/tmp/harporis-sevfilter-%d-%d", os.Getpid(), time.Now().UnixNano())
	tmpFile := tmpDir + "/" + scanID + ext

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if out, err := co.Exec(ctx, "writer", "mkdir", "-p", tmpDir); err != nil {
		detail := strings.TrimSpace(out)
		if detail == "" {
			detail = err.Error()
		}
		return "", fmt.Errorf("mkdir temp: %s", detail)
	}

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

	body, readErr := co.Exec(ctx, "writer", "cat", tmpFile)
	_, _ = co.Exec(ctx, "writer", "rm", "-rf", tmpDir)
	if readErr != nil {
		detail := strings.TrimSpace(body)
		if detail == "" {
			detail = readErr.Error()
		}
		return "", fmt.Errorf("read regenerated %s: %s", scanID+ext, detail)
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
			// The writer emits both <id>.ndjson and <id>.sarif per scan;
			// dedup to one row per scan_id, ignore tempfiles.
			seen := make(map[string]struct{})
			for _, name := range strings.Split(body, "\n") {
				name = strings.TrimSpace(name)
				switch {
				case strings.HasSuffix(name, ".ndjson"):
					name = strings.TrimSuffix(name, ".ndjson")
				case strings.HasSuffix(name, ".sarif"):
					name = strings.TrimSuffix(name, ".sarif")
				default:
					continue // skip tempfiles like *.sarif.tmp-*
				}
				if _, ok := seen[name]; ok {
					continue
				}
				seen[name] = struct{}{}
				fmt.Fprintln(cmd.OutOrStdout(), name)
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
	seen := make(map[string]struct{})
	for _, e := range entries {
		name := e.Name()
		switch {
		case strings.HasSuffix(name, ".ndjson"):
			name = strings.TrimSuffix(name, ".ndjson")
		case strings.HasSuffix(name, ".sarif"):
			name = strings.TrimSuffix(name, ".sarif")
		default:
			continue
		}
		seen[name] = struct{}{}
	}
	if len(seen) == 0 {
		fmt.Fprintln(w, "(no findings yet)")
		return nil
	}
	for name := range seen {
		fmt.Fprintln(w, name)
	}
	return nil
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
		var f findings.Finding
		if err := json.Unmarshal([]byte(line), &f); err != nil {
			fmt.Fprintf(tw, "?\tparse-error\t-\t%s\n", findings.DecodeAndPreview(line, 48))
			count++
			continue
		}
		loc := f.Location()
		secret := findings.DecodeAndPreview(f.MatchedSecret, 48)
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

// parseNDJSON decodes the writer's NDJSON output into findings.Finding
// values, skipping blank lines. Lines that fail to unmarshal are
// returned as zero-value entries with ScanID == "" so callers can
// surface them with a "parse-error" row instead of aborting the whole
// render. Returns the bufio.Scanner error if the stream itself broke.
func parseNDJSON(r io.Reader) ([]findings.Finding, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var out []findings.Finding
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var f findings.Finding
		if err := json.Unmarshal([]byte(line), &f); err != nil {
			out = append(out, findings.Finding{RuleID: "parse-error", Severity: "?"})
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
		loc := f.Location()
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
