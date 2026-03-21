// Package nexus contains the CLI commands for the nexus tool.
package nexus

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/iamaina/nexus/internal/app"
	"github.com/iamaina/nexus/internal/ingestion"
	"github.com/iamaina/nexus/internal/logger"
	"github.com/spf13/cobra"
)

var force bool
var ingestCmd = &cobra.Command{
	Use:   "ingest",
	Short: "Ingest documents from configured sources",
	Run: func(cmd *cobra.Command, _ []string) {
		ctx := cmd.Context()

		services, ok := ctx.Value("services").(*app.Services)
		if !ok {
			logger.Error(ctx, "Services not found in context")
			os.Exit(1)
		}

		logger.Info(ctx, "Starting ingestion", slog.Int("sources", len(services.Config.Sources)))

		var processed, skipped, failed int

		for _, src := range services.Config.Sources {
			logger.Info(ctx, "Processing source",
				slog.String("name", src.Name),
				slog.String("path", src.Path),
			)

			err := filepath.WalkDir(src.Path, func(path string, d os.DirEntry, err error) error {
				if err != nil {
					logger.Error(ctx, "Error accessing path",
						slog.String("path", path),
						slog.Any("error", err))
					return err
				}
				if d.IsDir() {
					return nil
				}

				ext := strings.ToLower(filepath.Ext(path))
				match := false
				for _, e := range src.Extensions {
					if ext == strings.ToLower(e) {
						match = true
						break
					}
				}
				if !match {
					return nil
				}

				ingested, err := ingestion.IngestFile(ctx, services, src.Name, path, force)
				if err != nil {
					logger.Error(ctx, "File ingestion failed", slog.String("path", path), slog.Any("err", err))
					failed++
					return nil // continue
				}
				if ingested {
					processed++
				} else {
					skipped++
				}
				return nil
			})

			if err != nil {
				logger.Error(ctx, "Source walk failed",
					slog.String("source", src.Name),
					slog.String("path", src.Path),
					slog.Any("err", err))
			}
		}
		logger.Info(ctx, "Ingestion complete",
			slog.Int("processed", processed),
			slog.Int("skipped", skipped),
			slog.Int("failed", failed))
	},
}

func init() {
	ingestCmd.Flags().BoolVar(&force, "force", false, "Force re-ingestion (ignore dedup)")
	RootCmd.AddCommand(ingestCmd)
}
