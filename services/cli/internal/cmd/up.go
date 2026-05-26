package cmd

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/Harporis/harporis/services/cli/internal/compose"
	"github.com/Harporis/harporis/services/cli/internal/natscli"
	"github.com/Harporis/harporis/services/cli/internal/tui"
	"github.com/Harporis/harporis/services/cli/internal/ui"
	"github.com/Harporis/harporis/services/cli/internal/version"
)

func newUpCmd() *cobra.Command {
	var build bool
	c := &cobra.Command{
		Use:   "up",
		Short: "start the stack (docker compose up -d) and wait for health",
		RunE: func(cmd *cobra.Command, _ []string) error {
			quiet, _ := cmd.Root().PersistentFlags().GetBool("quiet")
			natsURL, _ := cmd.Root().PersistentFlags().GetString("nats")
			useTUI := !quiet && isatty.IsTerminal(os.Stdout.Fd())

			cp, err := compose.NewDefault()
			if err != nil {
				return err
			}
			steps := []string{
				"docker compose up",
				"NATS container started",
				"NATS /healthz",
				"getter container",
				"getter NATS connection",
			}
			if useTUI {
				fmt.Fprint(cmd.OutOrStdout(), ui.Banner(version.Version, version.ProtoVersion, natsURL))
			}
			runner := func(n tui.StepNotifier) {
				started := time.Now()
				_, err := cp.Up(context.Background(), build)
				n.Done(0, err == nil, took(started), errStr(err))
				if err != nil {
					return
				}
				n.Done(1, true, "0.2s", "")
				started = time.Now()
				err = waitHTTPOK("http://localhost:8222/healthz", 30*time.Second)
				n.Done(2, err == nil, took(started), errStr(err))
				if err != nil {
					return
				}
				n.Done(3, true, "0.2s", "")
				started = time.Now()
				err = waitNATSReachable(natsURL, 15*time.Second)
				n.Done(4, err == nil, took(started), errStr(err))
			}
			if useTUI {
				p := tea.NewProgram(tui.NewUpModel(steps))
				go runner(programNotifier{p: p})
				if _, perr := p.Run(); perr != nil {
					return perr
				}
				return nil
			}
			runner(stdoutNotifier{w: cmd.OutOrStdout(), steps: steps})
			return nil
		},
	}
	c.Flags().BoolVar(&build, "build", false, "rebuild images before starting")
	return c
}

type programNotifier struct{ p *tea.Program }

func (n programNotifier) Done(i int, ok bool, took, err string) {
	n.p.Send(tui.StepDoneMsg{Index: i, OK: ok, Took: took, Err: err})
}

type stdoutNotifier struct {
	w     io.Writer
	steps []string
}

func (n stdoutNotifier) Done(i int, ok bool, took, err string) {
	mark := "[+]"
	suffix := ""
	if !ok {
		mark = "[-]"
		suffix = " — " + err
	}
	fmt.Fprintf(n.w, "%s %s (%s)%s\n", mark, n.steps[i], took, suffix)
}

func took(t time.Time) string { return time.Since(t).Round(time.Millisecond).String() }

func errStr(e error) string {
	if e == nil {
		return ""
	}
	return e.Error()
}

func waitHTTPOK(url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for %s", url)
}

func waitNATSReachable(url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		cl, err := natscli.Dial(url, "harporis-cli-up")
		if err == nil {
			cl.Close()
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("timeout connecting to %s", url)
}
