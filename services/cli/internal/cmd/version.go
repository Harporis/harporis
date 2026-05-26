package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Harporis/harporis/services/cli/internal/ui"
	"github.com/Harporis/harporis/services/cli/internal/version"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "print version, commit, and proto contract version",
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			quiet, _ := cmd.Root().PersistentFlags().GetBool("quiet")
			if !quiet {
				natsURL, _ := cmd.Root().PersistentFlags().GetString("nats")
				fmt.Fprint(out, ui.Banner(version.Version, version.ProtoVersion, natsURL))
			}
			fmt.Fprintf(out,
				"harporis %s (commit %s, proto %s)\n",
				version.Version, version.Commit, version.ProtoVersion)
			return nil
		},
	}
}
