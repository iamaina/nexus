// Package nexus contains the CLI commands for the nexus tool.
package nexus

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/iamaina/nexus/internal/app"
	"github.com/iamaina/nexus/internal/ingestion"
	"github.com/iamaina/nexus/internal/logger"
	"github.com/spf13/cobra"
)

var force bool

var ingestCmd = &cobra.Command{
	Use:   "ingest",
	Short: "Index documents from your configured source folders",
	Run: func(cmd *cobra.Command, _ []string) {
		ctx := cmd.Context()

		a, ok := ctx.Value(app.AppKey).(*app.Application)
		if !ok {
			logger.Error(ctx, "Application not found in context")
			os.Exit(1)
		}

		batchStart := time.Now()
		logger.Info(ctx, "ingestion.start",
			slog.String("component", "ingestion"),
			slog.String("event", "ingestion.start"),
			slog.Int("source_count", len(a.Config.Sources)),
		)

		var processed, skipped, failed int

		for _, src := range a.Config.Sources {
			logger.Debug(ctx, "source.start",
				slog.String("component", "ingestion"),
				slog.String("event", "source.start"),
				slog.String("source", src.Name),
				slog.String("path", src.Path),
			)

			err := filepath.WalkDir(src.Path, func(path string, d os.DirEntry, err error) error {
				if err != nil {
					logger.Error(ctx, "source.walk_error",
						slog.String("component", "ingestion"),
						slog.String("event", "source.walk_error"),
						slog.String("path", path),
						slog.Any("err", err))
					return err
				}

				// Skip paths matching any exclude pattern.
				for _, excl := range src.Exclude {
					if strings.Contains(path, excl) {
						if d.IsDir() {
							return filepath.SkipDir
						}
						return nil
					}
				}

				if d.IsDir() {
					return nil
				}

				ext := strings.ToLower(filepath.Ext(path))
				for _, e := range src.Extensions {
					if ext != strings.ToLower(e) {
						continue
					}
					ingested, err := ingestion.IngestFile(ctx, a, path, src.Name, force, nil)
					if err != nil {
						logger.Error(ctx, "file.failed",
							slog.String("component", "ingestion"),
							slog.String("event", "file.failed"),
							slog.String("source", src.Name),
							slog.String("file_path", path),
							slog.Any("err", err))
						failed++
						return nil
					}
					if ingested {
						processed++
					} else {
						skipped++
					}
					break
				}
				return nil
			})

			if err != nil {
				logger.Error(ctx, "source.failed",
					slog.String("component", "ingestion"),
					slog.String("event", "source.failed"),
					slog.String("source", src.Name),
					slog.String("path", src.Path),
					slog.Any("err", err))
			}
		}

		// Ingest URL sources configured in config.yaml.
		for _, u := range a.Config.URLs {
			count, err := ingestion.CrawlAndIngest(ctx, a, u.URL, u.Name, u.Depth, parseDelay(u.Delay), force, false)
			if err != nil {
				logger.Error(ctx, "url.source_failed",
					slog.String("component", "ingestion"),
					slog.String("event", "url.source_failed"),
					slog.String("source", u.Name),
					slog.String("url", u.URL),
					slog.Any("err", err))
				failed++
				continue
			}
			processed += count
		}

		logger.Info(ctx, "ingestion.complete",
			slog.String("component", "ingestion"),
			slog.String("event", "ingestion.complete"),
			slog.Int("processed", processed),
			slog.Int("skipped", skipped),
			slog.Int("failed", failed),
			slog.Int64("duration_ms", time.Since(batchStart).Milliseconds()),
		)
	},
}

func init() {
	ingestCmd.Flags().BoolVar(&force, "force", false, "Force re-ingestion (ignore dedup)")
	RootCmd.AddCommand(ingestCmd)
}
