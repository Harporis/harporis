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

// stackStep is one named action in the `up` checklist. Splitting the
// orchestration into a slice keeps adding/reordering steps to a
// one-liner instead of editing an index-coupled closure.
type stackStep struct {
	label string
	run   func() error
}

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
			steps := []stackStep{
				{label: "docker compose up", run: func() error { _, err := cp.Up(context.Background(), build); return err }},
				{label: "NATS /healthz", run: func() error { return waitHTTPOK("http://localhost:8222/healthz", 30*time.Second) }},
				{label: "getter NATS reachable", run: func() error { return waitNATSReachable(natsURL, 15*time.Second) }},
			}
			labels := make([]string, len(steps))
			for i, s := range steps {
				labels[i] = s.label
			}

			if useTUI {
				fmt.Fprint(cmd.OutOrStdout(), ui.Banner(version.Version, version.ProtoVersion, natsURL))
				p := tea.NewProgram(tui.NewUpModel(labels))
				go runSteps(steps, programNotifier{p: p})
				if _, perr := p.Run(); perr != nil {
					return perr
				}
				return nil
			}
			runSteps(steps, stdoutNotifier{w: cmd.OutOrStdout(), steps: labels})
			return nil
		},
	}
	c.Flags().BoolVar(&build, "build", false, "rebuild images before starting")
	return c
}

// runSteps drives a slice of stackStep through a StepNotifier, stopping
// on the first failure so downstream steps don't run against a broken
// pre-condition.
func runSteps(steps []stackStep, n tui.StepNotifier) {
	for i, s := range steps {
		started := time.Now()
		err := s.run()
		n.Done(i, err == nil, took(started), errStr(err))
		if err != nil {
			return
		}
	}
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
