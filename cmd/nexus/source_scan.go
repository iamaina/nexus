package nexus

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/iamaina/nexus/internal/app"
	"github.com/iamaina/nexus/internal/config"
	"github.com/iamaina/nexus/internal/workspace"
	"github.com/spf13/cobra"
)

var sourceScanDryRun bool

// sourceCmd is the parent for source-management subcommands.
var sourceCmd = &cobra.Command{
	Use:   "source",
	Short: "Manage nexus document sources",
	Long: `Manage nexus document sources.

  nexus source scan   — discover repo directories and propose them as sources

Since: v0.1.0`,
}

// sourceScanCmd reads dir_structure.md and proposes new sources to config.yaml.
var sourceScanCmd = &cobra.Command{
	Use:   "scan",
	Short: "Discover repo directories and propose them as nexus sources",
	Long: `Reads dir_structure.md from the workspace root, groups git repositories
by their parent directory, and proposes each group as a nexus source.

  nexus source scan             — interactive: prompts for source names, then applies
  nexus source scan --dry-run   — show discovered groups without modifying config.yaml

Each proposed source covers all .md and .txt files in the group directory.
Run 'nexus ingest' after applying to index the new sources.

Since: v0.1.0`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		ctx := cmd.Context()
		a, ok := ctx.Value(app.AppKey).(*app.Application)
		if !ok {
			return fmt.Errorf("application not available")
		}

		if a.Config.Roots.Workspace == "" {
			return fmt.Errorf("roots.workspace is not set in config.yaml — nexus needs a workspace root to know where to look")
		}

		structurePath := filepath.Join(a.Config.Roots.Workspace, "dir_structure.md")
		if _, err := os.Stat(structurePath); err != nil {
			return fmt.Errorf("dir_structure.md not found at %s\n  Run 'nexus watch' once to generate it", structurePath)
		}

		repos, err := workspace.ParseRepos(structurePath)
		if err != nil {
			return fmt.Errorf("parse dir_structure.md: %w", err)
		}
		if len(repos) == 0 {
			fmt.Println("No git repositories found in dir_structure.md.")
			return nil
		}

		groups := workspace.GroupByDirectory(repos)

		// Build set of paths already covered by existing sources.
		configuredPaths := map[string]bool{}
		for _, s := range a.Config.Sources {
			configuredPaths[filepath.Clean(s.Path)] = true
		}

		home, _ := os.UserHomeDir()
		tilde := func(p string) string { return strings.Replace(p, home, "~", 1) }

		// Filter out groups whose directory is already a configured source.
		var newGroups []workspace.RepoGroup
		for _, g := range groups {
			if !configuredPaths[filepath.Clean(g.DirPath)] {
				newGroups = append(newGroups, g)
			}
		}

		if len(newGroups) == 0 {
			fmt.Println("All discovered repo directories are already configured as nexus sources.")
			return nil
		}

		fmt.Printf("\nDiscovered %d repo group(s) not yet in config.yaml:\n\n", len(newGroups))
		for i, g := range newGroups {
			fmt.Printf("  [%d] %s/  (%d repo(s))\n", i+1, tilde(g.DirPath), len(g.Repos))
			for j, r := range g.Repos {
				if j >= 5 {
					fmt.Printf("        ... and %d more\n", len(g.Repos)-5)
					break
				}
				remote := r.Remote
				if remote == "" {
					remote = "no remote"
				}
				fmt.Printf("        - %-30s  [%s]\n", r.Name, remote)
			}
			fmt.Println()
		}

		if sourceScanDryRun {
			fmt.Println("(dry-run — config.yaml not modified)")
			return nil
		}

		// Interactive prompt: ask for a source name per group.
		scanner := bufio.NewScanner(os.Stdin)

		type pendingSource struct {
			name string
			path string
		}
		var pending []pendingSource

		fmt.Println("For each group, enter a source name (Enter = use directory name, '-' = skip):")
		fmt.Println()
		for _, g := range newGroups {
			suggested := filepath.Base(g.DirPath)
			fmt.Printf("  %s/ → [%s]: ", tilde(g.DirPath), suggested)
			scanner.Scan()
			input := strings.TrimSpace(scanner.Text())
			switch input {
			case "", ".":
				input = suggested
			case "-", "skip", "s":
				continue
			}
			pending = append(pending, pendingSource{name: input, path: g.DirPath})
		}

		if len(pending) == 0 {
			fmt.Println("\nNo sources to add.")
			return nil
		}

		fmt.Printf("\nWill add %d source(s) to config.yaml:\n\n", len(pending))
		for _, s := range pending {
			fmt.Printf("  - name: %s\n    path: %s\n    extensions: [.md, .txt]\n    watch: false\n\n",
				s.name, tilde(s.path))
		}
		fmt.Print("Apply? [Y/n]: ")
		scanner.Scan()
		answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
		if answer != "" && answer != "y" && answer != "yes" {
			fmt.Println("Cancelled.")
			return nil
		}

		for _, s := range pending {
			a.Config.Sources = append(a.Config.Sources, config.Source{
				Name:       s.name,
				Path:       s.path,
				Extensions: []string{".md", ".txt"},
				Watch:      false,
			})
		}

		if err := a.Config.Save(); err != nil {
			return fmt.Errorf("save config: %w", err)
		}

		fmt.Printf("\n✅  Added %d source(s) to config.yaml.\n", len(pending))
		fmt.Println("    Run 'nexus ingest' to index the new sources.")
		return nil
	},
}

func init() {
	sourceScanCmd.Flags().BoolVar(&sourceScanDryRun, "dry-run", false, "Show discovered groups without modifying config.yaml")
	sourceCmd.AddCommand(sourceScanCmd)
	RootCmd.AddCommand(sourceCmd)
}
