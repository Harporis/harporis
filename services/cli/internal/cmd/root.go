// Package cmd is the cobra command tree for the harporis CLI.
package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Harporis/harporis/services/cli/internal/config"
	"github.com/Harporis/harporis/services/cli/internal/ui"
	"github.com/Harporis/harporis/services/cli/internal/version"
)

// NewRootCmd builds a fresh root command. Used by main and by tests.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "harporis",
		Short:         "git-aware secret hunter — operator CLI",
		SilenceUsage:  true,
		SilenceErrors: true,
		Run: func(cmd *cobra.Command, _ []string) {
			quiet, _ := cmd.Flags().GetBool("quiet")
			if !quiet {
				natsURL, _ := cmd.Flags().GetString("nats")
				fmt.Fprint(cmd.OutOrStdout(), ui.Banner(version.Version, version.ProtoVersion, natsURL))
			}
			_ = cmd.Help()
		},
	}
	root.PersistentFlags().String("nats", defaultNATSURL(), "NATS server URL (env NATS_URL)")
	root.PersistentFlags().Bool("no-color", false, "disable ANSI styling (env NO_COLOR)")
	root.PersistentFlags().Bool("json", false, "machine-readable JSON output on read commands")
	root.PersistentFlags().BoolP("quiet", "q", false, "suppress banner and secondary output")
	root.PersistentFlags().String("config", "", "config file path (default: ~/.config/harporis/config.yaml)")

	// Load config before any subcommand runs; explicit flags win.
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		cfgPath, _ := cmd.Flags().GetString("config")
		if cfgPath == "" {
			cfgPath = config.DefaultPath()
			if cfgPath == "" {
				return nil
			}
		}
		cfg, err := config.Load(cfgPath)
		if err != nil {
			return err
		}
		if !root.PersistentFlags().Changed("nats") {
			_ = root.PersistentFlags().Set("nats", cfg.NATSURL)
		}
		return nil
	}

	root.AddCommand(newVersionCmd())
	root.AddCommand(newScanCmd())
	root.AddCommand(newCancelCmd())
	root.AddCommand(newWatchCmd())
	root.AddCommand(newHistoryCmd())
	root.AddCommand(newUpCmd())
	root.AddCommand(newDownCmd())
	root.AddCommand(newPSCmd())
	root.AddCommand(newLogsCmd())
	root.AddCommand(newHealthCmd())
	root.AddCommand(newDoctorCmd())
	root.AddCommand(newMetricsCmd())
	root.AddCommand(newCompletionCmd(root))
	return root
}

// Execute is the package-level entry point used by main. Translates
// typed exitErrors into the matching process exit code.
func Execute() {
	if err := NewRootCmd().Execute(); err != nil {
		var ex interface{ ExitCode() int }
		if errors.As(err, &ex) {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(ex.ExitCode())
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func defaultNATSURL() string {
	if v := os.Getenv("NATS_URL"); v != "" {
		return v
	}
	return "nats://localhost:4222"
}
