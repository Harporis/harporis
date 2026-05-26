package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Harporis/harporis/services/cli/internal/compose"
	"github.com/Harporis/harporis/services/cli/internal/ui"
)

func newUpCmd() *cobra.Command {
	var build bool
	c := &cobra.Command{
		Use:   "up",
		Short: "start the stack (docker compose up -d)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cp, err := compose.NewDefault()
			if err != nil {
				return err
			}
			out, err := cp.Up(context.Background(), build)
			fmt.Fprintln(cmd.OutOrStdout(), out)
			if err != nil {
				return fmt.Errorf("compose up failed: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), ui.OKStyle.Render("stack started"))
			return nil
		},
	}
	c.Flags().BoolVar(&build, "build", false, "rebuild images before starting")
	return c
}
