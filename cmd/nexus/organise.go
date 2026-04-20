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
	organiseDryRun bool
	organiseForce  bool
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
  nexus organise                             # processes personal.watchDirs`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()

		a, ok := ctx.Value(app.AppKey).(*app.Application)
		if !ok {
			logger.Error(ctx, "Application not found in context")
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

// collectFiles returns all supported files in dir.
// If recursive is true, it walks all subdirectories.
func collectFiles(dir string, recursive bool) ([]string, error) {
	var files []string
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		if e.IsDir() {
			if recursive {
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

func init() {
	organiseCmd.Flags().BoolVar(&organiseDryRun, "dry-run", false, "show plan without moving or ingesting")
	organiseCmd.Flags().BoolVarP(&organiseForce, "force", "f", false, "re-ingest even if file content is unchanged")
	RootCmd.AddCommand(organiseCmd)
}
