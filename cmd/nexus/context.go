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
	Short: "Connect live data from your infrastructure to every answer",
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
		created, err := a.ContextSources.Add(ctx, name, command, contextDescription)
		if err != nil {
			logger.Error(ctx, fmt.Sprintf("context add failed: %v", err))
			return
		}
		verb := "Updated"
		if created {
			verb = "Registered"
		}
		fmt.Printf("  ✓ %s %q\n    $ %s\n", verb, name, command)
		if contextDescription != "" {
			fmt.Printf("    %s\n", contextDescription)
		}
	},
}

var contextListVerbose bool

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

		if contextListVerbose {
			printContextListVerbose(sources)
		} else {
			printContextList(sources)
		}
	},
}

func printContextList(sources []models.ContextSource) {
	const maxDesc = 60
	fmt.Printf("\n  %-16s  %s\n", "NAME", "DESCRIPTION")
	fmt.Printf("  %-16s  %s\n", "────────────────", "────────────────────────────────────────────────────────────")
	for _, s := range sources {
		desc := s.Description
		if desc == "" {
			desc = "—"
		}
		if len(desc) > maxDesc {
			desc = desc[:maxDesc-1] + "…"
		}
		fmt.Printf("  %-16s  %s\n", s.Name, desc)
	}
	fmt.Println()
}

func printContextListVerbose(sources []models.ContextSource) {
	for _, s := range sources {
		desc := s.Description
		if desc == "" {
			desc = "—"
		}
		fmt.Printf("  %s\n", s.Name)
		fmt.Printf("    %s\n", desc)
		fmt.Printf("    $ %s\n", s.Command)
		fmt.Printf("    added %s\n\n", s.CreatedAt)
	}
}

func completeSourceNames(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	a, ok := cmd.Context().Value(app.AppKey).(*app.Application)
	if !ok {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	sources, err := a.ContextSources.List(cmd.Context())
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	names := make([]string, len(sources))
	for i, s := range sources {
		names[i] = s.Name
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}

var contextRunCmd = &cobra.Command{
	Use:               "run <name>",
	Short:             "Execute a context source and print its output",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeSourceNames,
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
	Use:               "rm <name>",
	Short:             "Remove a registered live context source",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeSourceNames,
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
	contextListCmd.Flags().BoolVarP(&contextListVerbose, "verbose", "v", false, "show full command and added date")
	contextCmd.AddCommand(contextAddCmd, contextListCmd, contextRunCmd, contextRmCmd)
	RootCmd.AddCommand(contextCmd)
}
