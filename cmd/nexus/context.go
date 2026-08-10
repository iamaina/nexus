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
var contextTags string

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
  nexus context rm kubectl

Since: v0.0.1`,
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
		created, err := a.ContextSources.Add(ctx, name, command, contextDescription, contextTags)
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
		if contextTags != "" {
			fmt.Printf("    tags: %s\n", contextTags)
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
	const maxDesc = 48
	const maxTags = 28
	fmt.Printf("\n  %-16s  %-28s  %s\n", "NAME", "TAGS", "DESCRIPTION")
	fmt.Printf("  %-16s  %-28s  %s\n", "────────────────", "────────────────────────────", "────────────────────────────────────────────────")
	for _, s := range sources {
		tags := s.Tags
		if tags == "" {
			tags = "(always)"
		}
		if len(tags) > maxTags {
			tags = tags[:maxTags-1] + "…"
		}
		desc := s.Description
		if desc == "" {
			desc = "—"
		}
		if len(desc) > maxDesc {
			desc = desc[:maxDesc-1] + "…"
		}
		fmt.Printf("  %-16s  %-28s  %s\n", s.Name, tags, desc)
	}
	fmt.Println()
}

func printContextListVerbose(sources []models.ContextSource) {
	for _, s := range sources {
		desc := s.Description
		if desc == "" {
			desc = "—"
		}
		tags := s.Tags
		if tags == "" {
			tags = "(always)"
		}
		fmt.Printf("  %s\n", s.Name)
		fmt.Printf("    %s\n", desc)
		fmt.Printf("    tags:  %s\n", tags)
		fmt.Printf("    $ %s\n", s.Command)
		fmt.Printf("    added %s\n\n", s.CreatedAt)
	}
}

var contextTagsCmd = &cobra.Command{
	Use:   "tags <name> <tags>",
	Short: "Set tags on a context source (comma-separated keywords)",
	Long: `Set the relevance tags for an existing context source.
Tags are comma-separated keywords. The source is only injected when the query
contains at least one matching keyword. An empty string removes all tags and
restores always-inject behaviour.

Examples:
  nexus context tags current-repo "git,repo,branch,commit,code,changes"
  nexus context tags my-issues "issues,tickets,assigned,gitlab,work,tasks"
  nexus context tags repo-paths ""   # remove tags — always inject`,
	Args:              cobra.ExactArgs(2),
	ValidArgsFunction: completeSourceNames,
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()
		a, ok := ctx.Value(app.AppKey).(*app.Application)
		if !ok {
			logger.Error(ctx, "Application not found in context")
			return
		}
		name, tags := args[0], args[1]
		if err := a.ContextSources.SetTags(ctx, name, tags); err != nil {
			logger.Error(ctx, fmt.Sprintf("%v", err))
			return
		}
		if tags == "" {
			fmt.Printf("  ✓ %q — tags cleared (always inject)\n", name)
		} else {
			fmt.Printf("  ✓ %q — tags: %s\n", name, tags)
		}
	},
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
	contextAddCmd.Flags().StringVar(&contextTags, "tags", "", "comma-separated keywords that trigger this source (empty = always inject)")
	contextListCmd.Flags().BoolVarP(&contextListVerbose, "verbose", "v", false, "show full command and added date")
	contextCmd.AddCommand(contextAddCmd, contextListCmd, contextRunCmd, contextRmCmd, contextTagsCmd)
	RootCmd.AddCommand(contextCmd)
}
