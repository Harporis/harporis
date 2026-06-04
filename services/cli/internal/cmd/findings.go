package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

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
			if outputDir != "" {
				return readLocalFile(outputDir+"/"+scanID+".ndjson", cmd.OutOrStdout())
			}
			co, err := compose.NewDefault()
			if err != nil {
				return fmt.Errorf("docker compose not available: %w (pass --output-dir for host file access)", err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			body, err := co.Exec(ctx, "writer", "cat", path)
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

func readLocalFile(path string, w io.Writer) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	_, err = io.Copy(w, f)
	return err
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
