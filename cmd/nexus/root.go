// Package nexus contains the CLI commands for the nexus tool.
package nexus

import (
	"fmt"
	"os"
	"runtime/debug"

	"github.com/spf13/cobra"
)

var cfgFile string

// buildVersion is the ldflags injection target — must stay a plain string literal.
// Set by: go build -ldflags "-X github.com/iamaina/nexus/cmd/nexus.buildVersion=v1.2.3"
var buildVersion = ""

// Version is the resolved version used throughout the binary.
// Priority: ldflags tag > VCS commit hash embedded by Go toolchain > "dev".
var Version = resolveVersion()

func resolveVersion() string {
	if buildVersion != "" {
		return buildVersion
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	var rev string
	var dirty bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if len(s.Value) >= 7 {
				rev = s.Value[:7]
			}
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return "dev"
	}
	if dirty {
		return "dev+" + rev + "-dirty"
	}
	return "dev+" + rev
}

// RootCmd is the root command for the nexus CLI.
var RootCmd = &cobra.Command{
	Use:     "nexus",
	Short:   "Ops Nexus — local personal intelligence layer",
	Long:    `Local-first personal intelligence layer. No cloud, no subscriptions, no data leaving your machine.`,
	Version: Version,
}

// Execute executes the root command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	RootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default: ~/ops-nexus/nexus/config.yaml)")
	RootCmd.PersistentFlags().Float64Var(&queryThreshold, "threshold", 0, "relevance threshold for query results (overrides config)")
}
