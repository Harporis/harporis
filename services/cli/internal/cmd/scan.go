package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/kit/nats/wire"
	kitscan "github.com/Harporis/harporis/kit/scan"
	"github.com/Harporis/harporis/services/cli/internal/compose"
	"github.com/Harporis/harporis/services/cli/internal/natscli"
)

// writerFormats is the set of output formats that ship as writer sinks
// — what `harporis scan --format` is allowed to request. Distinct from
// the read-side `findings show --format` set (which also includes
// CLI-derived shapes like pretty/json/csv/md).
var writerFormats = []string{"ndjson", "sarif", "html", "xlsx", "pdf", "parquet", "sqlite"}

// maxContextLines caps `--context` client-side so an operator typo
// (e.g. 100000) gets a friendly error instead of silently shipping a
// pathological scan request. Server-side getter applies its own clamp
// (kept identical so the two stay in sync).
const maxContextLines = 100

func newScanCmd() *cobra.Command {
	var (
		scanID, scanType, local, remoteURL       string
		token, sshKey, knownHosts                string
		branch, baseBranch, commitFrom, commitTo string
		formats                                  []string
		contextLines                             int32
		noWait, noMountHost, fromInit            bool
		initTo, commit, rangeSpec                string
		formatHelp                               bool
		idleTimeout                              time.Duration
		outputDir                                string
	)
	c := &cobra.Command{
		Use:   "scan",
		Short: "submit a scan request to NATS (waits for terminal state by default)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if formatHelp {
				printFormatHelp(cmd.OutOrStdout())
				return nil
			}
			if scanID == "" {
				scanID = uuid.NewString()
			}
			// Validate with the shared kit/scan rule so the CLI rejects
			// exactly what the getter/writer would, before submitting.
			if err := kitscan.ValidateScanID(scanID); err != nil {
				return err
			}
			// Range presets are mutually exclusive shortcuts that expand
			// into --type / --from / --to. Resolve them first so the
			// subsequent code path is unchanged.
			if err := applyRangePresets(cmd, &scanType, &commitFrom, &commitTo,
				fromInit, initTo, commit, rangeSpec); err != nil {
				return err
			}
			typ, ok := scanTypeFromString(scanType)
			if !ok {
				return fmt.Errorf("invalid --type %q", scanType)
			}
			translated, err := translateLocalPath(local, os.Getenv("HOME"), !noMountHost)
			if err != nil {
				return err
			}
			if translated != local {
				fmt.Fprintf(cmd.OutOrStdout(), "mounted host path: %s → %s (read-only via getter:/host)\n", local, translated)
			}
			src, err := buildSource(translated, remoteURL, token, sshKey, knownHosts)
			if err != nil {
				return err
			}
			req := &v1.ScanRequest{ScanId: scanID, Type: typ, Source: src}
			if branch != "" || baseBranch != "" || commitFrom != "" || commitTo != "" {
				req.Range = &v1.ScanRange{
					Branch: branch, BaseBranch: baseBranch,
					CommitFrom: commitFrom, CommitTo: commitTo,
				}
			}
			if contextLines < 0 {
				return fmt.Errorf("--context must be >= 0 (got %d)", contextLines)
			}
			if contextLines > maxContextLines {
				return fmt.Errorf("--context %d exceeds cap %d", contextLines, maxContextLines)
			}
			if len(formats) > 0 || contextLines > 0 {
				normalized := make([]string, 0, len(formats))
				for _, f := range formats {
					f = strings.ToLower(strings.TrimSpace(f))
					if f == "" {
						continue
					}
					if !slices.Contains(writerFormats, f) {
						return fmt.Errorf("unknown --format %q (want one of: %s)", f, strings.Join(writerFormats, ", "))
					}
					normalized = append(normalized, f)
				}
				req.Output = &v1.OutputConfig{}
				if len(normalized) > 0 {
					req.Output.Formats = normalized
				}
				if contextLines > 0 {
					req.Output.ContextLines = contextLines
				}
			}

			natsURL, _ := cmd.Root().PersistentFlags().GetString("nats")
			cl, err := natscli.Dial(natsURL, "harporis-cli")
			if err != nil {
				return fmt.Errorf("nats dial: %w", err)
			}
			defer cl.Close()
			if err := cl.EnsureStreams(); err != nil {
				return fmt.Errorf("ensure streams: %w", err)
			}
			data, err := proto.Marshal(req)
			if err != nil {
				return err
			}
			if _, err := cl.JS.Publish(wire.ScansRequestsSubject, data); err != nil {
				return fmt.Errorf("publish: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "submitted scan_id=%s type=%s\n", req.ScanId, typ.String())
			if noWait {
				if outputDir != "" {
					fmt.Fprintln(cmd.OutOrStdout(), "--output-dir is ignored with --no-wait (no terminal state to copy after)")
				}
				return nil
			}
			if err := StreamStatusLines(cmd.OutOrStdout(), cl, req.ScanId, idleTimeout); err != nil {
				return err
			}
			if outputDir != "" {
				return copyFindingsToHost(cmd.OutOrStdout(), req.ScanId, outputDir)
			}
			return nil
		},
	}
	c.Flags().StringVar(&scanID, "scan-id", "", "scan id (default: generated UUID)")
	c.Flags().StringVar(&scanType, "type", "current_state", "scan type: current_state|full_history|branch_full|commit_range|branch_diff|head_diff|staged")
	c.Flags().StringVar(&local, "local", "", "local repo path; host paths under $HOME are auto-translated to /host/<rel> via the getter's read-only $HOME mount (see --no-mount-host)")
	c.Flags().BoolVar(&noMountHost, "no-mount-host", false, "disable auto-translation of --local; pass a container-side path (e.g. /repos/myrepo via docker-compose.override.yml)")
	c.Flags().StringVar(&remoteURL, "remote-url", "", "remote repo URL (https:// or git@host:repo.git)")
	c.Flags().StringVar(&token, "remote-token", "", "Bearer / PAT token for HTTPS remotes")
	c.Flags().StringVar(&sshKey, "remote-ssh-key", "", "path to SSH private key file (PEM)")
	c.Flags().StringVar(&knownHosts, "remote-known-hosts", "", "path to known_hosts file")
	c.Flags().StringVar(&branch, "branch", "", "branch name (branch_full / branch_diff)")
	c.Flags().StringVar(&baseBranch, "base-branch", "", "base branch (branch_diff)")
	c.Flags().StringVar(&commitFrom, "from", "", "commit from (commit_range, exclusive)")
	c.Flags().StringVar(&commitTo, "to", "", "commit to (commit_range, inclusive)")
	c.Flags().BoolVar(&noWait, "no-wait", false, "do not block on status events; submit and return")
	c.Flags().DurationVar(&idleTimeout, "timeout", 30*time.Minute, "give up if no status events arrive for this long")
	c.Flags().StringSliceVarP(&formats, "format", "f", nil, "writer output formats this scan should emit (comma-separated). Allowed: "+strings.Join(writerFormats, ", ")+". Empty = all writer-enabled sinks fire (default).")
	c.Flags().Int32Var(&contextLines, "context", 0, fmt.Sprintf("number of lines BEFORE and AFTER each finding to include in the report (0 = no context, capped at %d). Visible in NDJSON/SARIF/HTML/XLSX/PDF.", maxContextLines))
	c.Flags().BoolVar(&fromInit, "from-init", false, "shortcut for --type full_history (scan every commit reachable from init)")
	c.Flags().StringVar(&initTo, "init-to", "", "shortcut for --type commit_range --from <init> --to <sha> (init → sha)")
	c.Flags().StringVar(&commit, "commit", "", "shortcut for --type commit_range scanning a single commit's diff (sha~1 → sha)")
	c.Flags().StringVar(&rangeSpec, "range", "", "shortcut for --type commit_range using git A..B syntax")
	c.Flags().BoolVar(&formatHelp, "format-help", false, "print the difference between `scan -f` (writer-side) and `findings show -f` (read-side) format sets and exit")
	c.Flags().StringVarP(&outputDir, "output-dir", "o", "", "after the scan completes, copy every <scan_id>.<ext> file from the writer container into this host directory (created if missing). Polls until files stabilize past finalize_grace_ms.")
	return c
}

// copyFindingsToHost waits for writer-side finalization to settle, then
// copies every <scan_id>.* file from the writer container into dst.
// Sequence:
//  1. Sleep `findingsCopyInitialDelay` (default 12s) — covers the
//     writer's default finalize_grace_ms (10s) plus a small buffer
//     for the slowest sink (Parquet rename).
//  2. Poll the writer's findings dir; treat the file list as stable
//     once two consecutive snapshots match, bounded by
//     `findingsCopyMaxWait`.
//
// The initial delay is what makes this reliable: NDJSON streams and
// appears immediately, but the accumulator + Parquet sinks only
// produce their final file after finalize_grace_ms. Without the wait,
// the early `ls` snapshot would lock in NDJSON-only as "stable".
func copyFindingsToHost(out io.Writer, scanID, dst string) error {
	dst = filepath.Clean(dst)
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return fmt.Errorf("mkdir output dir %s: %w", dst, err)
	}
	co, err := compose.NewDefault()
	if err != nil {
		return fmt.Errorf("docker compose not available: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), findingsCopyMaxWait)
	defer cancel()

	files, err := waitForFindingsStable(ctx, co, scanID)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		fmt.Fprintf(out, "no findings files matched scan_id=%s in writer's findings dir\n", scanID)
		return nil
	}

	copied := 0
	for _, name := range files {
		src := "/var/lib/harporis/findings/" + name
		hostPath := filepath.Join(dst, name)
		if cerr := co.CopyFromContainer(ctx, "writer", src, hostPath); cerr != nil {
			return fmt.Errorf("copy %s: %w", name, cerr)
		}
		copied++
	}
	fmt.Fprintf(out, "copied %d file(s) for scan_id=%s into %s\n", copied, scanID, dst)
	return nil
}

const (
	// findingsCopyInitialDelay is the unconditional wait after terminal
	// state before the first `ls` probe. Tuned to cover the default
	// writer finalize_grace_ms (10s) plus a small slack so the
	// slowest sink (Parquet rename) is on disk by the first snapshot.
	findingsCopyInitialDelay = 12 * time.Second
	// findingsCopyMaxWait bounds the total time spent waiting +
	// polling after terminal state. Must be >> findingsCopyInitialDelay.
	findingsCopyMaxWait = 30 * time.Second
	// findingsCopyPollInterval is the cadence at which `ls` is re-probed;
	// stability = two identical snapshots in a row.
	findingsCopyPollInterval = 500 * time.Millisecond
)

// waitForFindingsStable sleeps `findingsCopyInitialDelay` to clear the
// writer's finalize-grace window, then polls the findings dir until
// two consecutive `ls` snapshots scoped to scan_id match. Orphan
// tempfiles are filtered out before the comparison.
func waitForFindingsStable(ctx context.Context, co *compose.Compose, scanID string) ([]string, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(findingsCopyInitialDelay):
	}
	prev := ""
	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("timed out waiting for writer to finalize scan %s: %w", scanID, ctx.Err())
		default:
		}
		body, err := co.Exec(ctx, "writer", "ls", "-1", "/var/lib/harporis/findings")
		if err != nil {
			return nil, fmt.Errorf("ls writer findings: %w (%s)", err, strings.TrimSpace(body))
		}
		matched := filterFindingsForScan(body, scanID)
		snap := strings.Join(matched, "\n")
		if snap != "" && snap == prev {
			return matched, nil
		}
		prev = snap
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(findingsCopyPollInterval):
		}
	}
}

// filterFindingsForScan keeps `<scanID>.<ext>` and
// `<scanID>.<replica>.<ext>` shapes, dropping orphan tempfile patterns
// and unrelated files.
func filterFindingsForScan(lsOutput, scanID string) []string {
	out := []string{}
	for _, name := range strings.Split(strings.TrimSpace(lsOutput), "\n") {
		name = strings.TrimSpace(name)
		if name == "" || strings.HasPrefix(name, ".") || strings.Contains(name, ".tmp-") {
			continue
		}
		if name != scanID && !strings.HasPrefix(name, scanID+".") {
			continue
		}
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

// printFormatHelp explains the two -f flag scopes. The submission-side
// set (this command's -f) names which sinks the WRITER materializes
// per scan; the read-side set (findings show -f) controls how the CLI
// surfaces those materialized files (some derived locally from
// NDJSON, some streamed verbatim from the writer's <scan_id>.<ext>).
func printFormatHelp(w io.Writer) {
	fmt.Fprintln(w, "Writer-side formats — what `harporis scan -f <list>` accepts.")
	fmt.Fprintln(w, "Each name maps 1:1 to a writer sink; only the named sinks materialize.")
	fmt.Fprintln(w, "Empty list (the default) = every writer-enabled sink fires.")
	fmt.Fprintln(w)
	for _, f := range writerFormats {
		fmt.Fprintf(w, "  %-8s\n", f)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Read-side formats — what `harporis findings show -f <fmt>` accepts.")
	fmt.Fprintln(w, "Adds CLI-derived shapes (pretty/json/csv/md) on top of the writer-side files.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  ndjson  one protojson-encoded Finding per line (default; jq-friendly)")
	fmt.Fprintln(w, "  pretty  tab-aligned table with decoded matched_secret")
	fmt.Fprintln(w, "  sarif   SARIF v2.1.0 (writer's <scan_id>.sarif)")
	fmt.Fprintln(w, "  json    pretty-printed JSON array (machine-readable, no streaming)")
	fmt.Fprintln(w, "  csv     CSV row per finding")
	fmt.Fprintln(w, "  md      Markdown table (good for PR/issue comments)")
	fmt.Fprintln(w, "  html    self-contained browser report (writer's <scan_id>.html)")
	fmt.Fprintln(w, "  xlsx    Excel workbook (writer's <scan_id>.xlsx)")
	fmt.Fprintln(w, "  pdf     printable A4 report (writer's <scan_id>.pdf)")
	fmt.Fprintln(w, "  parquet columnar workbook (writer's <scan_id>.parquet; DuckDB/Polars/Athena)")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Note: requesting `scan -f pdf` while pdf_enabled=false in writer.yaml is")
	fmt.Fprintln(w, "silently dropped; the writer's writer_sink_format_ignored_total metric ticks.")
}

