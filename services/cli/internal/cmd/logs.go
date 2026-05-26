package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Harporis/harporis/services/cli/internal/compose"
)

func newLogsCmd() *cobra.Command {
	var follow bool
	c := &cobra.Command{
		Use:   "logs [service]",
		Short: "stream container logs",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cp, err := compose.NewDefault()
			if err != nil {
				return err
			}
			svc := ""
			if len(args) == 1 {
				svc = args[0]
			}
			out, err := cp.Logs(context.Background(), svc, follow)
			fmt.Fprintln(cmd.OutOrStdout(), out)
			return err
		},
	}
	c.Flags().BoolVarP(&follow, "follow", "f", false, "follow log output")
	return c
}
