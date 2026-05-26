package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Harporis/harporis/services/cli/internal/compose"
)

func newPSCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ps",
		Short: "show stack container status",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cp, err := compose.NewDefault()
			if err != nil {
				return err
			}
			out, err := cp.PS(context.Background())
			fmt.Fprintln(cmd.OutOrStdout(), out)
			return err
		},
	}
}
