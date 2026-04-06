package ingestion

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/iamaina/nexus/internal/app"
	"github.com/iamaina/nexus/internal/classifier"
	"github.com/iamaina/nexus/internal/logger"
	"github.com/iamaina/nexus/internal/models"
)

// FilingResult is returned by FileAndIngest.
type FilingResult struct {
	Classification *classifier.Classification
	DestPath       string
	Ingested       bool // false if already up to date
}

// FileAndIngest classifies srcPath, moves it to destBaseDir/<cl.DestDir>/<cl.Filename>.<ext>,
// then ingests the file into nexus under source "personal".
// It is used by both `nexus file` (manual) and `nexus watch` (automatic).
func FileAndIngest(ctx context.Context, a *app.Application, srcPath string) (*FilingResult, error) {
	destBaseDir := a.Config.Personal.DestDir
	if destBaseDir == "" {
		destBaseDir = filepath.Join(os.Getenv("HOME"), "Documents", "PersonalDocs")
	}

	// 1. Classify
	cl, err := a.Classifier.Classify(ctx, srcPath)
	if err != nil {
		logger.Warn(ctx, "classification failed — falling back to other/",
			slog.String("file", filepath.Base(srcPath)),
			slog.Any("err", err))
		cl = fallback(srcPath)
	}

	// 2. Build destination path
	ext := strings.ToLower(filepath.Ext(srcPath))
	filename := cl.Filename
	if filename == "" {
		filename = strings.TrimSuffix(filepath.Base(srcPath), filepath.Ext(srcPath))
	}
	destSubDir := filepath.Join(destBaseDir, cl.DestDir)
	destPath := filepath.Join(destSubDir, filename+ext)

	// 3. Create destination directory
	if err := os.MkdirAll(destSubDir, 0o750); err != nil { //nolint:gosec // destSubDir is built from config.DestDir + LLM dest_dir sanitised to lowercase alphanum/hyphens/slashes
		return nil, fmt.Errorf("create dir %s: %w", destSubDir, err)
	}

	// 4. Move file
	if err := moveFile(srcPath, destPath); err != nil {
		return nil, fmt.Errorf("move file: %w", err)
	}

	// 5. Ingest
	meta := &models.DocMeta{
		DocType:     cl.DocType,
		Language:    cl.Language,
		Institution: cl.Institution,
		DocDate:     cl.Date,
	}
	ingested, err := IngestFile(ctx, a, destPath, "personal", false, meta)
	if err != nil {
		return nil, fmt.Errorf("ingest: %w", err)
	}

	return &FilingResult{
		Classification: cl,
		DestPath:       destPath,
		Ingested:       ingested,
	}, nil
}

// fallback returns a safe Classification when the LLM fails.
func fallback(path string) *classifier.Classification {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	return &classifier.Classification{
		DocType:  "other",
		Language: "unknown",
		Filename: base,
		DestDir:  "other",
	}
}

// moveFile moves src to dst, falling back to copy+delete across filesystem boundaries.
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil { //nolint:gosec // paths are caller-controlled and validated
		return nil
	}
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
