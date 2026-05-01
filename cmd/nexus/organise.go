package nexus

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/iamaina/nexus/internal/app"
	"github.com/iamaina/nexus/internal/logger"
	"github.com/iamaina/nexus/internal/organiser"
	"github.com/spf13/cobra"
)

var (
	organiseDryRun      bool
	organiseForce       bool
	organiseReindex     bool
	organiseStatus      bool
	organiseConsolidate bool
	organiseCleanup     bool
)

var organiseCmd = &cobra.Command{
	Use:   "organise [path]",
	Short: "Classify, file, and index documents",
	Long: `Classify documents using the local LLM, move them to the right location,
and ingest them into nexus — with a confirmation step before any file moves.

If path is a file, organise that file only.
If path is a directory, organise all supported files inside it.
If path is omitted, organise all personal intake directories (personal.watchDirs).

Supported file types: .pdf .md .txt

Examples:
  nexus organise ~/Downloads
  nexus organise ~/Downloads/kubernetes-handbook.pdf
  nexus organise --dry-run ~/Downloads
  nexus organise                                        # processes personal.watchDirs
  nexus organise --reindex ~/Documents/PersonalDocs     # retry un-indexed files in place
  nexus organise --reindex --dry-run ~/Documents        # preview without making changes
  nexus organise --status ~/Documents/PersonalDocs      # show index coverage
  nexus organise --consolidate ~/Documents/PersonalDocs # re-point moved files in the DB
  nexus organise --cleanup ~/Documents/PersonalDocs     # delete originals, save names in DB

Since: v0.1.0`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()

		a, ok := ctx.Value(app.AppKey).(*app.Application)
		if !ok {
			logger.Error(ctx, "Application not found in context")
			return
		}

		// --status: read-only coverage report — how many files are indexed vs missing.
		if organiseStatus {
			target := a.Config.Personal.DestDir
			if len(args) > 0 {
				var err error
				target, err = filepath.Abs(args[0])
				if err != nil {
					logger.Error(ctx, fmt.Sprintf("Cannot resolve path: %v", err))
					return
				}
			}
			fmt.Printf("\n  Index coverage for %s\n\n", strings.Replace(target, func() string { h, _ := os.UserHomeDir(); return h }(), "~", 1))
			if err := organiser.StatusCheck(ctx, a, target); err != nil {
				logger.Error(ctx, fmt.Sprintf("Status check failed: %v", err))
			}
			fmt.Println()
			return
		}

		// --cleanup: delete duplicate originals and re-point DB records in one step.
		// Original filenames are saved to documents.original_path before deletion.
		if organiseCleanup {
			target := a.Config.Personal.DestDir
			if len(args) > 0 {
				var err error
				target, err = filepath.Abs(args[0])
				if err != nil {
					logger.Error(ctx, fmt.Sprintf("Cannot resolve path: %v", err))
					return
				}
			}
			fmt.Printf("\n  Scanning %s for duplicate originals...\n\n", strings.Replace(target, func() string { h, _ := os.UserHomeDir(); return h }(), "~", 1))
			if err := organiser.Cleanup(ctx, a, target); err != nil {
				logger.Error(ctx, fmt.Sprintf("Cleanup failed: %v", err))
			}
			fmt.Println()
			return
		}

		// --consolidate: re-point DB records for files that were moved/renamed after
		// ingestion, so nexus search and query return the current file path.
		if organiseConsolidate {
			target := a.Config.Personal.DestDir
			if len(args) > 0 {
				var err error
				target, err = filepath.Abs(args[0])
				if err != nil {
					logger.Error(ctx, fmt.Sprintf("Cannot resolve path: %v", err))
					return
				}
			}
			fmt.Printf("\n  Scanning %s for moved files...\n\n", strings.Replace(target, func() string { h, _ := os.UserHomeDir(); return h }(), "~", 1))
			if err := organiser.Consolidate(ctx, a, target, organiseDryRun); err != nil {
				logger.Error(ctx, fmt.Sprintf("Consolidate failed: %v", err))
			}
			fmt.Println()
			return
		}

		// --reindex: retry ingestion for files already in place but absent from the DB.
		// Useful for recovering files organised in a previous run that were silently
		// skipped (e.g. scanned PDFs that now have a text layer after re-scan/OCR, or
		// any file that failed to ingest for transient reasons).
		if organiseReindex {
			target := a.Config.Personal.DestDir
			if len(args) > 0 {
				var err error
				target, err = filepath.Abs(args[0])
				if err != nil {
					logger.Error(ctx, fmt.Sprintf("Cannot resolve path: %v", err))
					return
				}
			}
			verb := "Scanning"
			if organiseDryRun {
				verb = "Previewing"
			}
			fmt.Printf("\n  %s %s for un-indexed files...\n\n",
				verb, strings.Replace(target, func() string { h, _ := os.UserHomeDir(); return h }(), "~", 1))
			if err := organiser.ReindexUnindexed(ctx, a, target, organiseDryRun); err != nil {
				logger.Error(ctx, fmt.Sprintf("Reindex failed: %v", err))
			}
			fmt.Println()
			return
		}

		// Collect files to process.
		var files []string
		var err error

		if len(args) == 0 {
			// No argument — use all personal intake dirs.
			for _, dir := range a.Config.Personal.WatchDirs {
				found, e := collectFiles(dir, false)
				if e != nil {
					logger.Error(ctx, fmt.Sprintf("Cannot read directory %s: %v", dir, e))
					return
				}
				files = append(files, found...)
			}
			if len(files) == 0 {
				fmt.Println("  Nothing to organise in personal.watchDirs.")
				return
			}
		} else {
			target, e := filepath.Abs(args[0])
			if e != nil {
				logger.Error(ctx, fmt.Sprintf("Cannot resolve path: %v", e))
				return
			}
			info, e := os.Stat(target)
			if e != nil {
				logger.Error(ctx, fmt.Sprintf("Path not found: %s", target))
				return
			}

			if info.IsDir() {
				files, err = collectFiles(target, true)
				if err != nil {
					logger.Error(ctx, fmt.Sprintf("Cannot read directory: %v", err))
					return
				}
				if len(files) == 0 {
					fmt.Printf("  No supported files found in %s\n", target)
					return
				}
			} else {
				ext := strings.ToLower(filepath.Ext(target))
				if !organiser.SupportedExtensions[ext] {
					logger.Error(ctx, fmt.Sprintf("Unsupported file type: %s", ext))
					return
				}
				files = []string{target}
			}
		}

		// Require a workspace map before proceeding — organise uses it for
		// smart placement decisions. nexus workspace scan generates it.
		if !checkWorkspaceMap(a) {
			return
		}

		home, _ := os.UserHomeDir()

		// Build plan: classify each file.
		fmt.Printf("\n  Classifying %d file(s)...\n\n", len(files))
		org := organiser.New(a.Config)
		plan, err := org.BuildPlan(ctx, a, files)
		if err != nil {
			logger.Error(ctx, fmt.Sprintf("Plan failed: %v", err))
			return
		}
		if len(plan.Items) == 0 {
			fmt.Println("\n  Nothing to organise.")
			return
		}

		// Print the plan.
		var source string
		if len(args) > 0 {
			source = strings.Replace(args[0], home, "~", 1)
		} else {
			source = "personal.watchDirs"
		}
		fmt.Printf("  Plan for %s (%d file(s)):\n\n", source, len(plan.Items))

		// Find the longest source filename for alignment.
		maxName := 0
		for _, item := range plan.Items {
			if n := len(filepath.Base(item.SrcPath)); n > maxName {
				maxName = n
			}
		}

		for _, item := range plan.Items {
			displayDest := strings.Replace(item.DestPath, home, "~", 1)
			marker := "[existing]"
			if item.IsNew {
				marker = "[new dir] "
			}
			fmt.Printf("    %-*s  →  %s  %s\n",
				maxName, filepath.Base(item.SrcPath), displayDest, marker)
		}
		fmt.Println()

		if organiseDryRun {
			fmt.Println("  [dry-run] No files were moved or ingested.")
			return
		}

		// Confirm.
		fmt.Print("  Apply? [Y/n] ")
		reader := bufio.NewReader(os.Stdin)
		answer, _ := reader.ReadString('\n')
		answer = strings.TrimSpace(strings.ToLower(answer))
		if answer != "" && answer != "y" && answer != "yes" {
			fmt.Println("  Cancelled.")
			return
		}

		fmt.Println()
		organiser.Execute(ctx, a, plan, organiseForce)
		fmt.Println()
	},
}

// skipDirs are directory names that collectFiles never descends into.
// This prevents organise from classifying files that belong to a git repo,
// a package manager cache, or a generated environment — none of which are
// "loose documents" that need to be filed.
var skipDirs = map[string]bool{
	".git":         true, // git repo root — treat the repo as a unit, not its files
	"node_modules": true,
	"vendor":       true,
	".direnv":      true,
	"__pycache__":  true,
	".venv":        true,
	".tox":         true,
	".mypy_cache":  true,
	"dist":         true,
	"build":        true,
	".terraform":   true,
	".cache":       true,
}

// collectFiles returns all supported files in dir.
// If recursive is true it walks subdirectories, skipping:
//   - directories whose name appears in skipDirs (caches, build outputs, etc.)
//   - directories that ARE git repos — repos are atomic units; their files
//     belong to the repo, not to the loose-document organiser
func collectFiles(dir string, recursive bool) ([]string, error) {
	var files []string
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		if e.IsDir() {
			if recursive && !skipDirs[e.Name()] && !isGitRepo(path) {
				sub, err := collectFiles(path, true)
				if err != nil {
					return nil, err
				}
				files = append(files, sub...)
			}
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if organiser.SupportedExtensions[ext] {
			files = append(files, path)
		}
	}
	return files, nil
}

// isGitRepo reports whether dir contains a .git entry (file or directory),
// making it a git repository root that collectFiles should not descend into.
func isGitRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

func init() {
	organiseCmd.Flags().BoolVar(&organiseDryRun, "dry-run", false, "show plan without moving or ingesting")
	organiseCmd.Flags().BoolVarP(&organiseForce, "force", "f", false, "re-ingest even if file content is unchanged")
	organiseCmd.Flags().BoolVar(&organiseReindex, "reindex", false, "retry ingestion for files already in place but absent from the index")
	organiseCmd.Flags().BoolVar(&organiseStatus, "status", false, "show index coverage for a directory (read-only)")
	organiseCmd.Flags().BoolVar(&organiseConsolidate, "consolidate", false, "re-point DB records to match files moved/renamed by organise")
	organiseCmd.Flags().BoolVar(&organiseCleanup, "cleanup", false, "delete duplicate originals and re-point DB records (saves original names)")
	RootCmd.AddCommand(organiseCmd)
}
