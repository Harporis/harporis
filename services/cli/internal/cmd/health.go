package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Harporis/harporis/kit/nats/wire"
	"github.com/Harporis/harporis/services/cli/internal/compose"
	"github.com/Harporis/harporis/services/cli/internal/natscli"
	"github.com/Harporis/harporis/services/cli/internal/ui"
)

func newHealthCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "health",
		Short: "quick liveness check: NATS RTT + getter and scanner /metrics",
		RunE: func(cmd *cobra.Command, _ []string) error {
			natsURL, _ := cmd.Root().PersistentFlags().GetString("nats")
			t := ui.NewTable("COMPONENT", "STATUS", "DETAIL")

			start := time.Now()
			cl, err := natscli.Dial(natsURL, "harporis-cli-health")
			natsRTT := time.Since(start)
			if err != nil {
				t.Row("nats", ui.ErrStyle.Render("DOWN"), err.Error())
			} else {
				cl.Close()
				t.Row("nats", ui.OKStyle.Render("UP"), fmt.Sprintf("connect in %s", natsRTT.Round(time.Millisecond)))
			}

			co, cerr := compose.NewDefault()
			for _, name := range wire.Services() {
				port := wire.MetricsPorts[name]
				row := name + " /metrics"
				if cerr != nil {
					t.Row(row, ui.ErrStyle.Render("DOWN"), "docker compose unavailable: "+cerr.Error())
					continue
				}
				// 5s budget accommodates Docker daemon RTT on slow setups
				// (WSL2, remote DOCKER_HOST). 2s tripped on cold-daemon
				// runs even with healthy services.
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				out, err := co.Exec(ctx, name, "wget", "-qO-", fmt.Sprintf("http://localhost:%d/metrics", port))
				cancel()
				if err != nil {
					detail := strings.TrimSpace(out)
					if detail == "" {
						detail = strings.TrimSpace(err.Error())
					}
					t.Row(row, ui.ErrStyle.Render("DOWN"), detail)
				} else if !strings.Contains(out, "# HELP") && !strings.Contains(out, "# TYPE") {
					t.Row(row, ui.WarnStyle.Render("DEGRADED"), "response not in Prometheus exposition format")
				} else {
					t.Row(row, ui.OKStyle.Render("UP"), "via compose exec")
				}
			}
			_, werr := t.WriteTo(cmd.OutOrStdout())
			return werr
		},
	}
}
