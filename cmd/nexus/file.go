package nexus

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/iamaina/nexus/internal/app"
	"github.com/iamaina/nexus/internal/ingestion"
	"github.com/iamaina/nexus/internal/logger"
	"github.com/spf13/cobra"
)

var fileDryRun bool

var fileCmd = &cobra.Command{
	Use:   "file <path>",
	Short: "Classify, move, and ingest a personal document",
	Long: `Classify a document using the local LLM, move it to the correct folder
inside PersonalDocs with a clean filename, then ingest it into nexus.

Examples:
  nexus file ~/Downloads/some-letter.pdf
  nexus file ~/Downloads/bank-statement.pdf --dry-run`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()

		a, ok := ctx.Value(app.AppKey).(*app.Application)
		if !ok {
			logger.Error(ctx, "Application not found in context")
			return
		}

		srcPath, err := filepath.Abs(args[0])
		if err != nil {
			logger.Error(ctx, fmt.Sprintf("Cannot resolve path: %v", err))
			return
		}
		if _, err := os.Stat(srcPath); err != nil {
			logger.Error(ctx, fmt.Sprintf("File not found: %s", srcPath))
			return
		}

		// Dry-run: classify only, no move or ingest.
		if fileDryRun {
			fmt.Printf("  Classifying %s ...\n", filepath.Base(srcPath))
			cl, err := a.Classifier.Classify(ctx, srcPath)
			if err != nil {
				logger.Error(ctx, fmt.Sprintf("Classification failed: %v", err))
				return
			}
			destBase := a.Config.Personal.DestDir
			if destBase == "" {
				destBase = filepath.Join(os.Getenv("HOME"), "Documents", "PersonalDocs")
			}
			ext := filepath.Ext(srcPath)
			destPath := filepath.Join(destBase, cl.DestDir, cl.Filename+ext)
			printClassification(cl.DocType, cl.Language, cl.Institution, cl.Date, destPath, cl.Filename+ext)
			fmt.Println("  [dry-run] No files were moved or ingested.")
			return
		}

		// Real run: classify → move → ingest.
		fmt.Printf("  Classifying %s ...\n", filepath.Base(srcPath))
		result, err := ingestion.FileAndIngest(ctx, a, srcPath)
		if err != nil {
			logger.Error(ctx, fmt.Sprintf("Filing failed: %v", err))
			return
		}

		cl := result.Classification
		printClassification(cl.DocType, cl.Language, cl.Institution, cl.Date, result.DestPath, filepath.Base(result.DestPath))
		fmt.Printf("  Moved → %s\n", result.DestPath)
		if result.Ingested {
			fmt.Println("  Done. File is filed and searchable.")
		} else {
			fmt.Println("  Done. File already ingested (content unchanged).")
		}
	},
}

func printClassification(docType, language, institution, date, destPath, filename string) {
	fmt.Printf("\n  Classification\n")
	fmt.Printf("  ├─ Type:        %s\n", docType)
	fmt.Printf("  ├─ Language:    %s\n", language)
	if institution != "" {
		fmt.Printf("  ├─ Institution: %s\n", institution)
	}
	if date != "" {
		fmt.Printf("  ├─ Date:        %s\n", date)
	}
	fmt.Printf("  ├─ Destination: %s\n", destPath)
	fmt.Printf("  └─ Filename:    %s\n\n", filename)
}

func init() {
	fileCmd.Flags().BoolVar(&fileDryRun, "dry-run", false, "show classification without moving or ingesting")
	RootCmd.AddCommand(fileCmd)
}
