package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Harporis/harporis/services/cli/internal/compose"
)

func newDownCmd() *cobra.Command {
	var vols bool
	c := &cobra.Command{
		Use:   "down",
		Short: "stop the stack (docker compose down)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cp, err := compose.NewDefault()
			if err != nil {
				return err
			}
			out, err := cp.Down(context.Background(), vols)
			fmt.Fprintln(cmd.OutOrStdout(), out)
			return err
		},
	}
	c.Flags().BoolVarP(&vols, "volumes", "v", false, "also remove named volumes")
	return c
}
