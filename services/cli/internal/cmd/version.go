package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Harporis/harporis/services/cli/internal/version"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "print version, commit, and proto contract version",
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintf(cmd.OutOrStdout(),
				"harporis %s (commit %s, proto %s)\n",
				version.Version, version.Commit, version.ProtoVersion)
			return nil
		},
	}
}
