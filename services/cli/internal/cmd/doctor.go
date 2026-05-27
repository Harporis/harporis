package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Harporis/harporis/services/cli/internal/doctor"
	"github.com/Harporis/harporis/services/cli/internal/ui"
	"github.com/Harporis/harporis/services/cli/internal/version"
)

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "run environment checks and print a verdict",
		RunE: func(cmd *cobra.Command, _ []string) error {
			natsURL, _ := cmd.Root().PersistentFlags().GetString("nats")
			quiet, _ := cmd.Root().PersistentFlags().GetBool("quiet")
			if !quiet {
				fmt.Fprint(cmd.OutOrStdout(), ui.Banner(version.Version, version.ProtoVersion, natsURL))
			}
			checks := []doctor.Check{
				doctor.DockerCheck{},
				doctor.ComposeCheck{},
				doctor.NATSCheck{URL: natsURL},
				doctor.GetterHealthCheck{},
			}
			results := doctor.RunAll(checks)
			t := ui.NewTable("CHECK", "RESULT", "DETAIL")
			allOK := true
			for _, r := range results {
				badge := ui.OKStyle.Render("OK")
				if !r.OK {
					badge = ui.ErrStyle.Render("FAIL")
					allOK = false
				}
				t.Row(r.Name, badge, r.Detail)
			}
			if _, err := t.WriteTo(cmd.OutOrStdout()); err != nil {
				return err
			}
			if !allOK {
				return &exitError{code: 2, msg: "one or more doctor checks failed"}
			}
			return nil
		},
	}
}
