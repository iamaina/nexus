package nexus

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/iamaina/nexus/internal/app"
	"github.com/iamaina/nexus/internal/classifier"
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

		destDir := a.Config.Personal.DestDir
		if destDir == "" {
			destDir = filepath.Join(os.Getenv("HOME"), "Documents", "PersonalDocs")
		}

		// 1. Classify
		fmt.Printf("  Classifying %s ...\n", filepath.Base(srcPath))
		cl, err := a.Classifier.Classify(ctx, srcPath)
		if err != nil {
			logger.Error(ctx, fmt.Sprintf("Classification failed: %v — filing to 'other'", err))
			cl = fallbackClassification(srcPath)
		}

		// 2. Build destination path
		ext := strings.ToLower(filepath.Ext(srcPath))
		filename := cl.Filename
		if filename == "" {
			filename = strings.TrimSuffix(filepath.Base(srcPath), filepath.Ext(srcPath))
		}
		destSubDir := filepath.Join(destDir, cl.DestDir)
		destPath := filepath.Join(destSubDir, filename+ext)

		// Print classification result
		fmt.Printf("\n  Classification\n")
		fmt.Printf("  ├─ Type:        %s\n", cl.DocType)
		fmt.Printf("  ├─ Language:    %s\n", cl.Language)
		if cl.Institution != "" {
			fmt.Printf("  ├─ Institution: %s\n", cl.Institution)
		}
		if cl.Date != "" {
			fmt.Printf("  ├─ Date:        %s\n", cl.Date)
		}
		fmt.Printf("  ├─ Destination: %s\n", destPath)
		fmt.Printf("  └─ Filename:    %s%s\n\n", filename, ext)

		if fileDryRun {
			fmt.Println("  [dry-run] No files were moved or ingested.")
			return
		}

		// 3. Create destination directory
		if err := os.MkdirAll(destSubDir, 0o750); err != nil {
			logger.Error(ctx, fmt.Sprintf("Cannot create directory %s: %v", destSubDir, err))
			return
		}

		// 4. Move file (rename is atomic on same filesystem; falls back to copy+delete across filesystems)
		if err := moveFile(srcPath, destPath); err != nil {
			logger.Error(ctx, fmt.Sprintf("Cannot move file: %v", err))
			return
		}
		fmt.Printf("  Moved → %s\n", destPath)

		// 5. Ingest
		fmt.Printf("  Ingesting ...\n")
		ingested, err := ingestion.IngestFile(ctx, a, destPath, "personal", false)
		if err != nil {
			logger.Error(ctx, fmt.Sprintf("Ingest failed: %v", err))
			return
		}
		if ingested {
			fmt.Printf("  Done. File is filed and searchable.\n")
		} else {
			fmt.Printf("  Done. File already ingested (content unchanged).\n")
		}
	},
}

// fallbackClassification returns a safe default when the LLM fails.
func fallbackClassification(path string) *classifier.Classification {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	return &classifier.Classification{
		DocType:  "other",
		Language: "unknown",
		Filename: base,
		DestDir:  "other",
	}
}

// moveFile moves src to dst, falling back to copy+delete if os.Rename fails
// (e.g. across filesystem boundaries such as /tmp → ~/Documents).
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil { //nolint:gosec // src is resolved via filepath.Abs, dst is constructed from config + LLM filename that has been sanitised
		return nil
	}
	// Cross-filesystem: copy then delete.
	in, err := os.Open(src) //nolint:gosec
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640) //nolint:gosec
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

	buf := make([]byte, 32*1024)
	for {
		n, readErr := in.Read(buf)
		if n > 0 {
			if _, writeErr := out.Write(buf[:n]); writeErr != nil {
				return writeErr
			}
		}
		if readErr != nil {
			break
		}
	}
	return os.Remove(src)
}

func init() {
	fileCmd.Flags().BoolVar(&fileDryRun, "dry-run", false, "show classification without moving or ingesting")
	RootCmd.AddCommand(fileCmd)
}
