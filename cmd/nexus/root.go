// Package nexus contains the CLI commands for the nexus tool.
package nexus

import (
	"fmt"
	"os"

	"github.com/iamaina/nexus/internal/config"
	"github.com/spf13/cobra"
)

var cfgFile, logLevel string

// RootCmd is the root command for the nexus CLI.
var RootCmd = &cobra.Command{
	Use:   "nexus",
	Short: "Ops Nexus local knowledge hub",
	Long:  `CLI for ingesting and querying your personal knowledge vault locally.`,
}

// Execute executes the root command, starting the CLI application.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {

	RootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file ...")
	RootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "", "log level...")
	RootCmd.PersistentFlags().Float64Var(&queryThreshold, "threshold", 0, "relevance threshold for query results (overrides config value)")

	// Load config early so log level is set for subcommands

	if len(os.Args) > 1 {
		_ = RootCmd.PersistentFlags().Parse(os.Args[1:])
	}
	config.C.RelevanceThreshold = float32(queryThreshold)
	config.C.LogLevel = &logLevel
}
