package nexus

import (
	"fmt"
	"time"

	"github.com/iamaina/nexus/internal/app"
	"github.com/iamaina/nexus/internal/live"
	"github.com/iamaina/nexus/internal/logger"
	"github.com/iamaina/nexus/internal/models"
	"github.com/spf13/cobra"
)

var contextDescription string

var contextCmd = &cobra.Command{
	Use:   "context",
	Short: "Manage live context sources injected into queries",
	Long: `Register shell commands whose output is injected into every query prompt.
Use this to give nexus real-time awareness of your infrastructure.

Examples:
  nexus context add kubectl "kubectl get pods -A" --description "all pods"
  nexus context add tf     "terraform show -json | jq '.values.root_module'"
  nexus context list
  nexus context run kubectl
  nexus context rm kubectl`,
}

var contextAddCmd = &cobra.Command{
	Use:   "add <name> <command>",
	Short: "Register a new live context source",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()
		a, ok := ctx.Value(app.AppKey).(*app.Application)
		if !ok {
			logger.Error(ctx, "Application not found in context")
			return
		}
		name, command := args[0], args[1]
		if err := a.ContextSources.Add(ctx, name, command, contextDescription); err != nil {
			logger.Error(ctx, fmt.Sprintf("context add failed: %v", err))
			return
		}
		fmt.Printf("  ✓ Registered %q\n    $ %s\n", name, command)
		if contextDescription != "" {
			fmt.Printf("    %s\n", contextDescription)
		}
	},
}

var contextListCmd = &cobra.Command{
	Use:   "list",
	Short: "List registered live context sources",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, _ []string) {
		ctx := cmd.Context()
		a, ok := ctx.Value(app.AppKey).(*app.Application)
		if !ok {
			logger.Error(ctx, "Application not found in context")
			return
		}
		sources, err := a.ContextSources.List(ctx)
		if err != nil {
			logger.Error(ctx, fmt.Sprintf("context list failed: %v", err))
			return
		}
		if len(sources) == 0 {
			fmt.Println("No context sources registered.")
			fmt.Println("Add one with: nexus context add <name> \"<command>\"")
			return
		}
		fmt.Printf("\n  %-16s  %-40s  %s\n", "NAME", "COMMAND", "ADDED")
		fmt.Printf("  %-16s  %-40s  %s\n", "────────────────", "────────────────────────────────────────", "───────────────────")
		for _, s := range sources {
			shortCmd := s.Command
			if len(shortCmd) > 40 {
				shortCmd = shortCmd[:37] + "..."
			}
			fmt.Printf("  %-16s  %-40s  %s\n", s.Name, shortCmd, s.CreatedAt)
			if s.Description != "" {
				fmt.Printf("  %-16s  %s\n", "", s.Description)
			}
		}
		fmt.Println()
	},
}

var contextRunCmd = &cobra.Command{
	Use:   "run <name>",
	Short: "Execute a context source and print its output",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()
		a, ok := ctx.Value(app.AppKey).(*app.Application)
		if !ok {
			logger.Error(ctx, "Application not found in context")
			return
		}
		src, err := a.ContextSources.Get(ctx, args[0])
		if err != nil {
			logger.Error(ctx, fmt.Sprintf("%v", err))
			return
		}
		fmt.Printf("  $ %s\n\n", src.Command)
		outputs := live.RunAll(ctx, []models.ContextSource{*src}, 10*time.Second)
		o := outputs[0]
		if o.Err != nil {
			fmt.Printf("  Error: %v\n", o.Err)
			return
		}
		if o.Text == "" {
			fmt.Println("  (no output)")
			return
		}
		fmt.Println(o.Text)
	},
}

var contextRmCmd = &cobra.Command{
	Use:   "rm <name>",
	Short: "Remove a registered live context source",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()
		a, ok := ctx.Value(app.AppKey).(*app.Application)
		if !ok {
			logger.Error(ctx, "Application not found in context")
			return
		}
		if err := a.ContextSources.Remove(ctx, args[0]); err != nil {
			logger.Error(ctx, fmt.Sprintf("%v", err))
			return
		}
		fmt.Printf("  ✓ Removed %q\n", args[0])
	},
}

func init() {
	contextAddCmd.Flags().StringVar(&contextDescription, "description", "", "optional description of what this source provides")
	contextCmd.AddCommand(contextAddCmd, contextListCmd, contextRunCmd, contextRmCmd)
	RootCmd.AddCommand(contextCmd)
}
