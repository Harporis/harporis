// `harporis nats` subcommands let an operator confirm that JetStream
// retention is doing what wire.go promises and intervene when it isn't.
//
//   stats   shows per-stream message count + bytes and per-consumer
//           ack_floor / num_pending. For WorkQueuePolicy streams the
//           healthy steady state is messages=0 (every Ack deletes the
//           backing message); STATUS uses LimitsPolicy so its message
//           count grows up to MaxAge/MaxBytes before rotation.
//   purge   force-drops every message from a named stream. Useful for
//           a fresh start after a bad scan that left STATUS noisy or
//           when bringing up a dev stack against an old NATS volume.

package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	natsclient "github.com/nats-io/nats.go"
	"github.com/spf13/cobra"

	"github.com/Harporis/harporis/kit/nats/wire"
	"github.com/Harporis/harporis/services/cli/internal/natscli"
)

func newNATSCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "nats",
		Short: "inspect or operate on the JetStream streams Harporis uses",
	}
	c.AddCommand(newNATSStatsCmd())
	c.AddCommand(newNATSPurgeCmd())
	return c
}

func newNATSStatsCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "stats",
		Short: "show per-stream message counts and per-consumer ack state",
		Long: `Reports the live JetStream state for the four Harporis streams.

For REQUESTS, CHUNKS, FINDINGS (WorkQueuePolicy) the healthy steady state
is messages=0 — every Ack from a worker deletes the backing message.
For STATUS (LimitsPolicy) the count grows up to retention limits.

Use this to confirm that scanner/writer are draining their queues; a
non-zero pending count over time is a sign of a wedged consumer.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			natsURL, _ := cmd.Root().PersistentFlags().GetString("nats")
			cl, err := natscli.Dial(natsURL, "harporis-cli-nats-stats")
			if err != nil {
				return fmt.Errorf("nats dial: %w", err)
			}
			defer cl.Close()
			w := cmd.OutOrStdout()
			streams := []string{
				wire.RequestsStream,
				wire.ChunksStream,
				wire.FindingsStream,
				wire.StatusStream,
			}
			fmt.Fprintf(w, "%-22s %10s %12s %8s %8s %14s\n",
				"STREAM", "MESSAGES", "BYTES", "FIRST", "LAST", "RETENTION")
			for _, name := range streams {
				si, err := cl.JS.StreamInfo(name)
				if err != nil {
					fmt.Fprintf(w, "%-22s   (error: %v)\n", name, err)
					continue
				}
				fmt.Fprintf(w, "%-22s %10d %12d %8d %8d %14s\n",
					name,
					si.State.Msgs, si.State.Bytes,
					si.State.FirstSeq, si.State.LastSeq,
					retentionName(si.Config.Retention),
				)
			}
			fmt.Fprintln(w)
			fmt.Fprintf(w, "%-28s %-22s %10s %12s %12s\n",
				"CONSUMER", "STREAM", "PENDING", "ACK_FLOOR", "DELIVERED")
			for _, name := range streams {
				var consumers []*natsclient.ConsumerInfo
				cs := cl.JS.ConsumersInfo(name)
				for ci := range cs {
					consumers = append(consumers, ci)
				}
				for _, ci := range consumers {
					fmt.Fprintf(w, "%-28s %-22s %10d %12d %12d\n",
						ci.Name, name, ci.NumPending,
						ci.AckFloor.Stream, ci.Delivered.Stream)
				}
			}
			return nil
		},
	}
	return c
}

func retentionName(r natsclient.RetentionPolicy) string {
	switch r {
	case natsclient.LimitsPolicy:
		return "limits"
	case natsclient.WorkQueuePolicy:
		return "workqueue"
	case natsclient.InterestPolicy:
		return "interest"
	}
	return "?"
}

func newNATSPurgeCmd() *cobra.Command {
	var force bool
	c := &cobra.Command{
		Use:   "purge <STREAM>",
		Short: "force-drop every message from a JetStream stream",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if !isHarporisStream(name) {
				return fmt.Errorf("refusing to purge %q — only Harporis streams (%s) are allowed",
					name, strings.Join(harporisStreamNames(), ", "))
			}
			if !force {
				return fmt.Errorf("purge is destructive — re-run with --force to confirm")
			}
			natsURL, _ := cmd.Root().PersistentFlags().GetString("nats")
			cl, err := natscli.Dial(natsURL, "harporis-cli-nats-purge")
			if err != nil {
				return fmt.Errorf("nats dial: %w", err)
			}
			defer cl.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := cl.JS.PurgeStream(name, &natsclient.StreamPurgeRequest{}, natsclient.Context(ctx)); err != nil {
				return fmt.Errorf("purge %s: %w", name, err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "purged %s\n", name)
			return nil
		},
	}
	c.Flags().BoolVar(&force, "force", false, "actually run the purge (no-op otherwise)")
	return c
}

func harporisStreamNames() []string {
	return []string{
		wire.RequestsStream,
		wire.ChunksStream,
		wire.FindingsStream,
		wire.StatusStream,
	}
}

func isHarporisStream(name string) bool {
	for _, s := range harporisStreamNames() {
		if name == s {
			return true
		}
	}
	return false
}
