package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-isatty"
	natsclient "github.com/nats-io/nats.go"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/kit/nats/wire"
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
	consumer := "cli-watch-" + natscli.SanitizeConsumerName(scanID)
	sub, err := cl.JS.PullSubscribe(wire.StatusSubject(scanID), consumer,
		natsclient.BindStream(wire.StatusStream))
	if err != nil {
		return fmt.Errorf("subscribe status: %w", err)
	}
	defer func() {
		_ = sub.Unsubscribe()
		_ = cl.JS.DeleteConsumer(wire.StatusStream, consumer)
	}()

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
			msgs, err := sub.Fetch(8, natsclient.MaxWait(2*time.Second))
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, natsclient.ErrTimeout) {
					continue
				}
				p.Send(tui.SubscribeErrMsg{Err: err})
				return
			}
			for _, m := range msgs {
				lastSeen = time.Now()
				var ev v1.StatusEvent
				if err := proto.Unmarshal(m.Data, &ev); err != nil {
					_ = m.Ack()
					continue
				}
				p.Send(tui.StatusEventMsg{Ev: &ev})
				_ = m.Ack()
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
// a typed exitError for FAILED/CANCELLED so cobra can map to exit code 3,
// or an idle-timeout error mapped to 124.
func StreamStatusLines(out io.Writer, cl *natscli.Client, scanID string, idleTimeout time.Duration) error {
	consumer := "cli-watch-" + natscli.SanitizeConsumerName(scanID)
	sub, err := cl.JS.PullSubscribe(wire.StatusSubject(scanID), consumer,
		natsclient.BindStream(wire.StatusStream))
	if err != nil {
		return fmt.Errorf("subscribe status: %w", err)
	}
	defer func() {
		_ = sub.Unsubscribe()
		_ = cl.JS.DeleteConsumer(wire.StatusStream, consumer)
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	lastSeen := time.Now()
	for ctx.Err() == nil {
		if time.Since(lastSeen) > idleTimeout {
			return &exitError{code: 124, msg: fmt.Sprintf("idle timeout (%s) — no status events for %s", idleTimeout, scanID)}
		}
		msgs, err := sub.Fetch(8, natsclient.MaxWait(2*time.Second))
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, natsclient.ErrTimeout) {
				continue
			}
			return fmt.Errorf("watch fetch: %w", err)
		}
		for _, m := range msgs {
			lastSeen = time.Now()
			var ev v1.StatusEvent
			if err := proto.Unmarshal(m.Data, &ev); err != nil {
				fmt.Fprintf(out, "watch unmarshal: %v\n", err)
				_ = m.Ack()
				continue
			}
			printStatusLine(out, &ev)
			_ = m.Ack()
			if isTerminal(ev.State) {
				return terminalExitCode(ev.State)
			}
		}
	}
	return nil
}

func printStatusLine(out io.Writer, ev *v1.StatusEvent) {
	ts := time.Unix(ev.Timestamp, 0).UTC().Format(time.RFC3339)
	state := ui.StateStyle(ev.State.String()).Render(ev.State.String())
	m := ev.GetMetrics()
	fmt.Fprintf(out, "[%s] %-9s | %s | scanned=%d skipped=%d chunks=%d bytes=%d errors=%d\n",
		ts, state, ev.Message,
		m.GetBlobsScanned(), m.GetBlobsSkipped(),
		m.GetChunksPublished(), m.GetBytesPublished(), m.GetErrorsTotal())
}

func isTerminal(s v1.ScanState) bool {
	switch s {
	case v1.ScanState_COMPLETED, v1.ScanState_FAILED,
		v1.ScanState_CANCELLED, v1.ScanState_PARTIAL:
		return true
	}
	return false
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

// exitError is consumed by Execute() in root.go to set the process exit code.
type exitError struct {
	code int
	msg  string
}

func (e *exitError) Error() string { return e.msg }
func (e *exitError) ExitCode() int { return e.code }
