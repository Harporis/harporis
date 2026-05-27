package cmd

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"

	"github.com/Harporis/harporis/services/cli/internal/natscli"
	"github.com/Harporis/harporis/services/cli/internal/ui"
)

func newHealthCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "health",
		Short: "quick liveness check: NATS RTT + getter /metrics",
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

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost:9100/metrics", nil)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Row("getter /metrics", ui.ErrStyle.Render("DOWN"), err.Error())
			} else {
				resp.Body.Close()
				if resp.StatusCode == 200 {
					t.Row("getter /metrics", ui.OKStyle.Render("UP"), "HTTP 200")
				} else {
					t.Row("getter /metrics", ui.WarnStyle.Render("DEGRADED"), fmt.Sprintf("HTTP %d", resp.StatusCode))
				}
			}
			_, werr := t.WriteTo(cmd.OutOrStdout())
			return werr
		},
	}
}
