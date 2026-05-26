package cmd

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/kit/nats/wire"
	"github.com/Harporis/harporis/services/cli/internal/natscli"
)

func newCancelCmd() *cobra.Command {
	var reason string
	c := &cobra.Command{
		Use:   "cancel <scan-id>",
		Short: "ask the getter to cancel an active scan",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			scanID := args[0]
			if scanID == "" {
				return errors.New("scan-id is required")
			}
			natsURL, _ := cmd.Root().PersistentFlags().GetString("nats")
			cl, err := natscli.Dial(natsURL, "harporis-cli-cancel")
			if err != nil {
				return fmt.Errorf("nats dial: %w", err)
			}
			defer cl.Close()

			data, err := proto.Marshal(&v1.CancelScanRequest{ScanId: scanID, Reason: reason})
			if err != nil {
				return err
			}
			if err := cl.NC.Publish(wire.ScansCancelSubject, data); err != nil {
				return fmt.Errorf("publish cancel: %w", err)
			}
			if err := cl.NC.Flush(); err != nil {
				return fmt.Errorf("flush: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "cancel sent scan_id=%s reason=%q\n", scanID, reason)
			return nil
		},
	}
	c.Flags().StringVar(&reason, "reason", "operator cancelled", "free-form reason shown in the final status event")
	return c
}
