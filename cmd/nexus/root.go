// Package nexus contains the CLI commands for the nexus tool.
package nexus

import (
	"fmt"
	"os"
	"runtime/debug"

	"github.com/spf13/cobra"
)

var cfgFile string
var verbose bool
var showVersion bool
var resumeSession string

// Verbose reports whether --verbose / -v was passed on the command line.
func Verbose() bool { return verbose }

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
// Version is handled manually via -V/--version so that -v is free for --verbose.
var RootCmd = &cobra.Command{
	Use:   "nexus",
	Short: "Your local workspace intelligence — chat, search, and organise",
	Long: `nexus — your local workspace intelligence layer.

  Running nexus with no subcommand starts an interactive chat session.
  Ask anything in plain English across your documents, notes, and live infrastructure.
  Every answer is cited so you know exactly which source it came from.

  Sessions are saved to ~/.config/nexus/chats/ and can be resumed:

    nexus                                         start a new session
    nexus --resume 2026-04-20_14-32_praefect      continue a saved session

  Chat flags:

    --model string      override the generation model for this session
    --no-live           skip live context sources (kubectl, terraform, etc.)
    --source string     restrict search to one source or filename fragment
    --threshold float   minimum similarity score to include a chunk (default 0.70)

  Runs entirely on your own infrastructure. No cloud. No API keys. No subscriptions.
  Your data stays under your control — no third party ever sees what you ask or store.

Since: v0.1.0  (--category added v0.2.0)`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if showVersion {
			fmt.Printf("nexus %s\n", Version)
			return nil
		}
		return runChatSession(cmd, resumeSession)
	},
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
	RootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "show connection and pipeline logs (INFO level)")
	RootCmd.Flags().BoolVarP(&showVersion, "version", "V", false, "show version information")
	RootCmd.Flags().StringVar(&resumeSession, "resume", "", "continue a saved chat session")
	_ = RootCmd.RegisterFlagCompletionFunc("resume", func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return chatSessionNames()
	})
}
