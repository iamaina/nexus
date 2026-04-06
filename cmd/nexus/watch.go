package nexus

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/iamaina/nexus/internal/app"
	"github.com/iamaina/nexus/internal/ingestion"
	"github.com/iamaina/nexus/internal/logger"
	"github.com/spf13/cobra"
)

// settleDelay is how long we wait after a CREATE/WRITE event before processing.
// Files copied from a browser or phone are written in chunks; we need the
// write to finish before we read the content.
const settleDelay = 3 * time.Second

// watchedExtensions are the file types processed automatically by the watcher.
var watchedExtensions = map[string]bool{
	".pdf": true,
	".md":  true,
	".txt": true,
}

var watchCmd = &cobra.Command{
	Use:   "watch",
	Short: "Watch directories and automatically file incoming documents",
	Long: `Watch the directories configured under personal.watchDirs in config.yaml.
When a supported file (.pdf, .md, .txt) is created, nexus automatically
classifies it, moves it to PersonalDocs/<category>/, and ingests it.

Press Ctrl+C to stop watching.`,
	Run: func(cmd *cobra.Command, _ []string) {
		ctx := cmd.Context()

		a, ok := ctx.Value(app.AppKey).(*app.Application)
		if !ok {
			logger.Error(ctx, "Application not found in context")
			return
		}

		dirs := a.Config.Personal.WatchDirs
		if len(dirs) == 0 {
			logger.Error(ctx, "No watch directories configured — add personal.watchDirs to config.yaml")
			return
		}

		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			logger.Error(ctx, fmt.Sprintf("Cannot create watcher: %v", err))
			return
		}
		defer func() { _ = watcher.Close() }()

		watching := 0
		for _, dir := range dirs {
			if err := watcher.Add(dir); err != nil {
				logger.Warn(ctx, "Cannot watch directory",
					slog.String("dir", dir),
					slog.Any("err", err))
				continue
			}
			logger.Info(ctx, "Watching directory", slog.String("dir", dir))
			watching++
		}
		if watching == 0 {
			logger.Error(ctx, "No directories could be watched — check that watchDirs exist")
			return
		}

		fmt.Printf("\n  Watching %d director(ies). Press Ctrl+C to stop.\n\n", watching)

		// pending tracks settle timers per file path to debounce rapid WRITE events.
		// mu guards pending — the map is written by the main loop and deleted by
		// AfterFunc callbacks, which run in separate goroutines.
		pending := make(map[string]*time.Timer)
		var mu sync.Mutex

		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Op&(fsnotify.Create|fsnotify.Write) == 0 {
					continue
				}
				path := event.Name
				if !watchedExtensions[strings.ToLower(filepath.Ext(path))] {
					continue
				}

				// Debounce: reset the timer on every write so we only process
				// once the file has stopped changing.
				mu.Lock()
				if t, exists := pending[path]; exists {
					t.Stop()
				}
				filePath := path // capture for closure
				pending[filePath] = time.AfterFunc(settleDelay, func() {
					mu.Lock()
					delete(pending, filePath)
					mu.Unlock()
					// Each file gets its own background context so a slow
					// classification doesn't block the watcher loop.
					processWatchedFile(context.Background(), a, filePath)
				})
				mu.Unlock()

			case watchErr, ok := <-watcher.Errors:
				if !ok {
					return
				}
				logger.Warn(ctx, "Watcher error", slog.Any("err", watchErr))

			case <-ctx.Done():
				return
			}
		}
	},
}

// processWatchedFile runs classify → move → ingest for a file detected by the
// watcher. Errors are logged but never fatal — the watcher keeps running.
func processWatchedFile(ctx context.Context, a *app.Application, path string) {
	if _, err := os.Stat(path); err != nil {
		// File may have been moved or deleted before the settle timer fired.
		return
	}

	fmt.Printf("  → Detected: %s\n", filepath.Base(path))

	result, err := ingestion.FileAndIngest(ctx, a, path)
	if err != nil {
		logger.Warn(ctx, "Auto-filing failed",
			slog.String("file", filepath.Base(path)),
			slog.Any("err", err))
		return
	}

	cl := result.Classification
	fmt.Printf("  ✓ Filed [%s/%s]: %s\n",
		cl.DocType, cl.Language, filepath.Base(result.DestPath))
}

func init() {
	RootCmd.AddCommand(watchCmd)
}
