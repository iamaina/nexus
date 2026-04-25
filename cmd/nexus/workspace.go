package nexus

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/iamaina/nexus/internal/app"
	"github.com/iamaina/nexus/internal/ingestion"
	"github.com/iamaina/nexus/internal/logger"
	"github.com/iamaina/nexus/internal/workspace"
	"github.com/spf13/cobra"
)

var workspaceCmd = &cobra.Command{
	Use:   "workspace",
	Short: "Manage the workspace structure map",
	Long: `Commands for generating and inspecting the workspace structure map (dir_structure.md).

The workspace map is a prerequisite for nexus organise — it tells nexus what
directories already exist, where repos live, and how the workspace is laid out.

nexus watch keeps the map current automatically. Use nexus workspace scan when
setting up for the first time or to force an immediate refresh.

Since: v0.2.0`,
}

var workspaceScanCmd = &cobra.Command{
	Use:   "scan",
	Short: "Generate the workspace structure map (dir_structure.md)",
	Long: `Walk roots.workspace, detect all git repos, and write dir_structure.md.

This is a one-shot version of what nexus watch does on startup. Run it once
during initial setup, or any time you want to force an immediate refresh
without waiting for the next nexus watch cycle.

Since: v0.2.0`,
	Run: func(cmd *cobra.Command, _ []string) {
		ctx := cmd.Context()

		a, ok := ctx.Value(app.AppKey).(*app.Application)
		if !ok {
			logger.Error(ctx, "Application not found in context")
			return
		}

		workspaceRoot := a.Config.Roots.Workspace
		if workspaceRoot == "" {
			fmt.Println()
			fmt.Println("  ❌ roots.workspace is not set in config.yaml.")
			fmt.Println()
			fmt.Println("  Add it to your config:")
			fmt.Println()
			fmt.Println("    roots:")
			fmt.Println("      workspace: ~/your-workspace")
			fmt.Println()
			return
		}

		home, _ := os.UserHomeDir()
		display := strings.Replace(workspaceRoot, home, "~", 1)

		fmt.Printf("\n  Scanning workspace: %s\n\n", display)

		outPath, err := workspace.WriteTo(workspaceRoot)
		if err != nil {
			logger.Error(ctx, fmt.Sprintf("Snapshot generation failed: %v", err))
			return
		}

		fmt.Printf("  ✓ Written: %s\n", strings.Replace(outPath, home, "~", 1))

		fmt.Printf("  Ingesting into nexus...\n")
		if _, err := ingestion.IngestFile(ctx, a, outPath, "workspace-structure", true, nil); err != nil {
			logger.Error(ctx, fmt.Sprintf("Ingest failed: %v", err))
			return
		}

		fmt.Printf("  ✓ Workspace map ingested as source \"workspace-structure\"\n\n")
		fmt.Printf("  You can now run: nexus organise\n\n")
	},
}

// workspaceMapPath returns the expected path of dir_structure.md for the
// configured workspace root. Returns an empty string if roots.workspace is unset.
func workspaceMapPath(a *app.Application) string {
	if a.Config.Roots.Workspace == "" {
		return ""
	}
	return filepath.Join(a.Config.Roots.Workspace, "dir_structure.md")
}

// checkWorkspaceMap checks whether dir_structure.md exists and is ingested.
// Returns true if organise can proceed. If false it has already printed guidance.
func checkWorkspaceMap(a *app.Application) bool {
	mapPath := workspaceMapPath(a)
	if mapPath == "" {
		fmt.Println()
		fmt.Println("  ❌ roots.workspace is not set in config.yaml.")
		fmt.Println()
		fmt.Println("  nexus organise needs to know your workspace root.")
		fmt.Println("  Add it to your config, then run: nexus workspace scan")
		fmt.Println()
		return false
	}

	if _, err := os.Stat(mapPath); os.IsNotExist(err) {
		home, _ := os.UserHomeDir()
		display := strings.Replace(mapPath, home, "~", 1)
		fmt.Println()
		fmt.Printf("  ❌ Workspace not mapped yet (%s does not exist).\n\n", display)
		fmt.Println("  nexus organise uses the workspace map to make smart placement decisions.")
		fmt.Println("  Generate it first:")
		fmt.Println()
		fmt.Println("    nexus workspace scan")
		fmt.Println()
		return false
	}

	return true
}

func init() {
	workspaceCmd.AddCommand(workspaceScanCmd)
	RootCmd.AddCommand(workspaceCmd)
}
