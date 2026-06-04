package cmd

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
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

func newFindingsShowCmd() *cobra.Command {
	var outputDir string
	var pretty bool
	c := &cobra.Command{
		Use:   "show <scan_id>",
		Short: "print the writer's NDJSON output for a scan (one finding per line)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			scanID := args[0]
			if !validScanID(scanID) {
				return fmt.Errorf("invalid scan_id %q (use UUID-ish chars only)", scanID)
			}
			path := "/var/lib/harporis/findings/" + scanID + ".ndjson"
			var body string
			if outputDir != "" {
				b, err := os.ReadFile(outputDir + "/" + scanID + ".ndjson")
				if err != nil {
					return fmt.Errorf("read %s: %w", scanID+".ndjson", err)
				}
				body = string(b)
			} else {
				co, err := compose.NewDefault()
				if err != nil {
					return fmt.Errorf("docker compose not available: %w (pass --output-dir for host file access)", err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				body, err = co.Exec(ctx, "writer", "cat", path)
				if err != nil {
					// CombinedOutput leaves stderr in body — surface it so
					// "no such file" / "permission denied" reach the user
					// instead of a bare "exit status 1".
					detail := strings.TrimSpace(body)
					if detail == "" {
						detail = err.Error()
					}
					return fmt.Errorf("compose exec writer cat %s: %s", path, detail)
				}
			}
			if pretty {
				return renderPrettyFindings(strings.NewReader(body), cmd.OutOrStdout())
			}
			if _, err := cmd.OutOrStdout().Write([]byte(body)); err != nil {
				return err
			}
			if body != "" && !strings.HasSuffix(body, "\n") {
				fmt.Fprintln(cmd.OutOrStdout())
			}
			return nil
		},
	}
	c.Flags().StringVar(&outputDir, "output-dir", "", "read NDJSON files from a host path instead of `docker compose exec writer cat`")
	c.Flags().BoolVar(&pretty, "pretty", false, "render findings as a human-readable table (decodes base64 matched_secret)")
	return c
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
