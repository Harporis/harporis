package cmd

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/Harporis/harporis/services/cli/internal/natscli"
	"github.com/Harporis/harporis/services/cli/internal/ui"
)

func newHistoryCmd() *cobra.Command {
	listCmd := newHistoryListCmd()
	c := &cobra.Command{
		Use:   "history",
		Short: "list past scans (latest state per scan from the status stream)",
		RunE:  listCmd.RunE,
	}
	// Mirror the list flag onto the parent so `harporis history --limit N` works.
	c.Flags().IntP("limit", "l", 25, "max scans to list")
	c.AddCommand(listCmd)
	c.AddCommand(newHistoryShowCmd())
	return c
}

func newHistoryListCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "list",
		Short: "list past scans (newest first)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			limit, _ := cmd.Flags().GetInt("limit")
			natsURL, _ := cmd.Root().PersistentFlags().GetString("nats")
			cl, err := natscli.Dial(natsURL, "harporis-cli-history")
			if err != nil {
				return fmt.Errorf("nats dial: %w", err)
			}
			defer cl.Close()
			if err := cl.EnsureStreams(); err != nil {
				return fmt.Errorf("ensure streams: %w", err)
			}
			evs, err := cl.ListHistory(limit, 1*time.Second)
			if err != nil {
				return err
			}
			if len(evs) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), ui.DimStyle.Render("(no scans found in status stream)"))
				return nil
			}
			t := ui.NewTable("SCAN_ID", "STATE", "UPDATED", "CHUNKS", "BYTES", "ERRORS")
			for _, e := range evs {
				m := e.GetMetrics()
				t.Row(
					e.ScanId,
					ui.StateStyle(e.State.String()).Render(e.State.String()),
					time.Unix(e.Timestamp, 0).UTC().Format(time.RFC3339),
					fmt.Sprintf("%d", m.GetChunksPublished()),
					fmt.Sprintf("%d", m.GetBytesPublished()),
					fmt.Sprintf("%d", m.GetErrorsTotal()),
				)
			}
			_, err = t.WriteTo(cmd.OutOrStdout())
			return err
		},
	}
	c.Flags().IntP("limit", "l", 25, "max scans to list")
	return c
}

func newHistoryShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <scan-id>",
		Short: "print the full status timeline of one scan",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			natsURL, _ := cmd.Root().PersistentFlags().GetString("nats")
			cl, err := natscli.Dial(natsURL, "harporis-cli-history-show")
			if err != nil {
				return err
			}
			defer cl.Close()
			if err := cl.EnsureStreams(); err != nil {
				return err
			}
			evs, err := cl.ShowHistory(args[0], 1*time.Second)
			if err != nil {
				return err
			}
			if len(evs) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), ui.DimStyle.Render("(no events found for "+args[0]+")"))
				return nil
			}
			for _, e := range evs {
				printStatusLine(cmd.OutOrStdout(), e)
			}
			return nil
		},
	}
}
