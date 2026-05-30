package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/services/cli/internal/natscli"
	"github.com/Harporis/harporis/services/cli/internal/tui"
	"github.com/Harporis/harporis/services/cli/internal/ui"
)

func newWatchCmd() *cobra.Command {
	var idle time.Duration
	c := &cobra.Command{
		Use:   "watch <scan-id>",
		Short: "follow status events for a scan",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			scanID := args[0]
			natsURL, _ := cmd.Root().PersistentFlags().GetString("nats")
			jsonOut, _ := cmd.Root().PersistentFlags().GetBool("json")
			cl, err := natscli.Dial(natsURL, "harporis-cli-watch")
			if err != nil {
				return fmt.Errorf("nats dial: %w", err)
			}
			defer cl.Close()
			if err := cl.EnsureStreams(); err != nil {
				return fmt.Errorf("ensure streams: %w", err)
			}
			if !jsonOut && isatty.IsTerminal(os.Stdout.Fd()) {
				return RunWatchTUI(cl, scanID, idle)
			}
			return StreamStatusLines(cmd.OutOrStdout(), cl, scanID, idle)
		},
	}
	c.Flags().DurationVar(&idle, "timeout", 30*time.Minute, "give up if no status events arrive for this long")
	return c
}

// RunWatchTUI runs the bubble tea watch panel until terminal state or
// ctrl+c. Returns nil on success, a typed *exitError on FAILED/CANCELLED
// or on subscribe failure.
func RunWatchTUI(cl *natscli.Client, scanID string, idle time.Duration) error {
	sub, cleanup, err := cl.SubscribeStatus(scanID)
	if err != nil {
		return err
	}
	defer cleanup()

	p := tea.NewProgram(tui.NewWatchModel(scanID), tea.WithAltScreen())
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	go func() {
		lastSeen := time.Now()
		for ctx.Err() == nil {
			if time.Since(lastSeen) > idle {
				p.Send(tui.SubscribeErrMsg{Err: fmt.Errorf("idle timeout %s", idle)})
				return
			}
			events, err := natscli.FetchStatusEvents(sub, 2*time.Second)
			if err != nil {
				p.Send(tui.SubscribeErrMsg{Err: err})
				return
			}
			if len(events) == 0 {
				continue
			}
			lastSeen = time.Now()
			for _, ev := range events {
				p.Send(tui.StatusEventMsg{Ev: ev})
			}
		}
	}()

	finalModel, err := p.Run()
	if err != nil {
		return err
	}
	if wm, ok := finalModel.(tui.WatchModel); ok && wm.ExitCode() != 0 {
		return &exitError{code: wm.ExitCode(), msg: "scan terminal state non-zero"}
	}
	return nil
}

// StreamStatusLines follows the JetStream status subject for one scan
// and prints colored lines per event. Returns nil on success states,
// a typed exitError for FAILED/CANCELLED (code 3), or for an idle
// timeout (code 124).
func StreamStatusLines(out io.Writer, cl *natscli.Client, scanID string, idleTimeout time.Duration) error {
	sub, cleanup, err := cl.SubscribeStatus(scanID)
	if err != nil {
		return err
	}
	defer cleanup()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	lastSeen := time.Now()
	for ctx.Err() == nil {
		if time.Since(lastSeen) > idleTimeout {
			return &exitError{code: 124, msg: fmt.Sprintf("idle timeout (%s) — no status events for %s", idleTimeout, scanID)}
		}
		events, err := natscli.FetchStatusEvents(sub, 2*time.Second)
		if err != nil {
			return fmt.Errorf("watch fetch: %w", err)
		}
		for _, ev := range events {
			lastSeen = time.Now()
			ui.PrintStatusLine(out, ev)
			if tui.IsTerminal(ev.State) {
				return terminalExitCode(ev.State)
			}
		}
	}
	return nil
}

// terminalExitCode returns nil for success states and a typed exitError
// (code 3) for FAILED/CANCELLED so cobra's Execute can translate it.
func terminalExitCode(s v1.ScanState) error {
	switch s {
	case v1.ScanState_FAILED, v1.ScanState_CANCELLED:
		return &exitError{code: 3, msg: s.String()}
	}
	return nil
}
