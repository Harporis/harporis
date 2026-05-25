// Package cmd is the cobra command tree for the harporis CLI.
package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

// NewRootCmd builds a fresh root command. Used by main and by tests.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:          "harporis",
		Short:        "git-aware secret hunter — operator CLI",
		SilenceUsage: true,
	}
	root.PersistentFlags().String("nats", defaultNATSURL(), "NATS server URL (env NATS_URL)")
	root.PersistentFlags().Bool("no-color", false, "disable ANSI styling (env NO_COLOR)")
	root.PersistentFlags().Bool("json", false, "machine-readable JSON output on read commands")
	root.PersistentFlags().BoolP("quiet", "q", false, "suppress banner and secondary output")

	root.AddCommand(newVersionCmd())
	return root
}

// Execute is the package-level entry point used by main.
func Execute() {
	if err := NewRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func defaultNATSURL() string {
	if v := os.Getenv("NATS_URL"); v != "" {
		return v
	}
	return "nats://localhost:4222"
}
