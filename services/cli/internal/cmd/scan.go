package cmd

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/kit/nats/wire"
	"github.com/Harporis/harporis/services/cli/internal/natscli"
)

// writerFormats is the set of output formats that ship as writer sinks
// — what `harporis scan --format` is allowed to request. Distinct from
// the read-side `findings show --format` set (which also includes
// CLI-derived shapes like pretty/json/csv/md).
var writerFormats = []string{"ndjson", "sarif", "html", "xlsx", "pdf"}

func newScanCmd() *cobra.Command {
	var (
		scanID, scanType, local, remoteURL       string
		token, sshKey, knownHosts                string
		branch, baseBranch, commitFrom, commitTo string
		formats                                  []string
		noWait, noMountHost                      bool
		idleTimeout                              time.Duration
	)
	c := &cobra.Command{
		Use:   "scan",
		Short: "submit a scan request to NATS (waits for terminal state by default)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if scanID == "" {
				scanID = uuid.NewString()
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
			if len(formats) > 0 {
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
				if len(normalized) > 0 {
					req.Output = &v1.OutputConfig{Formats: normalized}
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
				return nil
			}
			return StreamStatusLines(cmd.OutOrStdout(), cl, req.ScanId, idleTimeout)
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
	return c
}

