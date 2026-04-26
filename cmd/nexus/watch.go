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
	"github.com/iamaina/nexus/internal/config"
	"github.com/iamaina/nexus/internal/ingestion"
	"github.com/iamaina/nexus/internal/logger"
	"github.com/iamaina/nexus/internal/models"
	"github.com/iamaina/nexus/internal/workspace"
	"github.com/spf13/cobra"
)

// Settle delays and scan intervals.
const (
	settleDelay       = 3 * time.Second  // personal files: wait for write to finish
	repoSettleDelay   = 10 * time.Second // new repo dir: wait for clone to complete
	sourceScanTick    = 5 * time.Minute  // how often to re-scan watched sources
	gdocSyncTick      = 30 * time.Minute // how often to re-sync registered Google Docs
	defaultURLTick    = 24 * time.Hour   // default polling interval for URL sources
	workspaceSnapTick = 24 * time.Hour   // periodic workspace snapshot refresh
)

// watchedExtensions are the file types processed by the personal intake watcher.
var watchedExtensions = map[string]bool{
	".pdf": true,
	".md":  true,
	".txt": true,
}

// watchAction classifies what should happen when an event fires in a directory.
type watchAction int

const (
	actionPersonalFile watchAction = iota // classify → move → ingest
	actionWorkspace                       // regenerate dir_structure.md
	actionRepoRoot                        // detect new .git dirs (Phase 4 prep)
)

var watchList bool

var watchCmd = &cobra.Command{
	Use:   "watch",
	Short: "Automatically organise, index, and monitor your workspace",
	Long: `Watch multiple directory types concurrently:

  personal.watchDirs  — classify and file new documents (PDF, MD, TXT)
  sources with watch:true — re-scan for new/changed files every 5 minutes
  roots.workspace     — regenerate workspace structure snapshot on changes
  roots.repos[watch]  — detect newly cloned repositories

Press Ctrl+C to stop watching.

Since: v0.0.1  (workspace OS layer added v0.1.0)`,
	Run: func(cmd *cobra.Command, _ []string) {
		ctx := cmd.Context()

		a, ok := ctx.Value(app.AppKey).(*app.Application)
		if !ok {
			logger.Error(ctx, "Application not found in context")
			return
		}

		if watchList {
			printWatchList(a)
			return
		}

		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			logger.Error(ctx, fmt.Sprintf("Cannot create watcher: %v", err))
			return
		}
		defer func() { _ = watcher.Close() }()

		// watchedDirs maps each watched directory to its purpose so the event
		// loop can route events without a chain of string comparisons.
		watchedDirs := make(map[string]watchAction)

		// 1. Personal intake directories.
		personalDirs := a.Config.Personal.WatchDirs
		watchCount := 0
		for _, dir := range personalDirs {
			if err := watcher.Add(dir); err != nil {
				logger.Warn(ctx, "Cannot watch directory",
					slog.String("dir", dir),
					slog.Any("err", err))
				continue
			}
			watchedDirs[dir] = actionPersonalFile
			logger.Info(ctx, "Watching personal dir", slog.String("dir", dir))
			watchCount++
		}

		// 2. Workspace structural watch (non-recursive — top-level dir events only).
		workspaceRoot := a.Config.Roots.Workspace
		if workspaceRoot != "" {
			if err := watcher.Add(workspaceRoot); err != nil {
				logger.Warn(ctx, "Cannot watch workspace root",
					slog.String("dir", workspaceRoot),
					slog.Any("err", err))
			} else {
				watchedDirs[workspaceRoot] = actionWorkspace
				logger.Info(ctx, "Watching workspace root", slog.String("dir", workspaceRoot))
				watchCount++
			}
		}

		// 3. Repo root watches (non-recursive — detect newly cloned repos).
		repoRootNames := make(map[string]string) // path → root name for logging
		for _, root := range a.Config.Roots.Repos {
			if !root.Watch {
				continue
			}
			if err := watcher.Add(root.Path); err != nil {
				logger.Warn(ctx, "Cannot watch repo root",
					slog.String("root", root.Name),
					slog.String("dir", root.Path),
					slog.Any("err", err))
				continue
			}
			watchedDirs[root.Path] = actionRepoRoot
			repoRootNames[root.Path] = root.Name
			logger.Info(ctx, "Watching repo root",
				slog.String("root", root.Name),
				slog.String("dir", root.Path))
			watchCount++
		}

		if watchCount == 0 && len(a.Config.Sources) == 0 {
			logger.Error(ctx, "Nothing to watch — configure personal.watchDirs or sources with watch:true")
			return
		}

		// 4. Source tickers: one goroutine per source marked watch:true.
		tickerCount := 0
		for _, src := range a.Config.Sources {
			if !src.Watch {
				continue
			}
			src := src // capture for goroutine
			go startSourceTicker(ctx, a, src)
			tickerCount++
			logger.Info(ctx, "Source ticker started",
				slog.String("source", src.Name),
				slog.Duration("interval", sourceScanTick))
		}

		// 4b. URL tickers: one goroutine per URL source marked watch:true.
		urlTickerCount := 0
		for _, u := range a.Config.URLs {
			if !u.Watch {
				continue
			}
			u := u // capture for goroutine
			go startURLTicker(ctx, a, u)
			urlTickerCount++
			logger.Info(ctx, "URL ticker started",
				slog.String("source", u.Name),
				slog.String("url", u.URL),
				slog.Duration("interval", parseURLInterval(u.Interval)))
		}

		// 5. Workspace snapshot — generate once at startup, then refresh every 24 h.
		// The periodic refresh catches structural changes (new subdirs, renamed dirs)
		// that fsnotify misses because the workspace watch is non-recursive.
		if workspaceRoot != "" {
			go func() {
				regenerateWorkspaceSnapshot(context.Background(), a, workspaceRoot)
				ticker := time.NewTicker(workspaceSnapTick)
				defer ticker.Stop()
				for {
					select {
					case <-ticker.C:
						regenerateWorkspaceSnapshot(context.Background(), a, workspaceRoot)
					case <-ctx.Done():
						return
					}
				}
			}()
		}

		// 6. Index health check — warn every 24 h if a rebuild is recommended.
		// Runs on a ticker only (not at startup) because it is diagnostic, not
		// urgent. No auto-rebuild — the user decides when to act.
		go func() {
			ticker := time.NewTicker(workspaceSnapTick)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					checkIndexHealth(context.Background(), a)
				case <-ctx.Done():
					return
				}
			}
		}()

		// 7. Google Docs sync ticker — only starts if credentials are configured.
		gdocCount := 0
		if a.Config.Gdoc.CredentialsPath != "" {
			docs, _ := a.Gdocs.List(ctx)
			gdocCount = len(docs)
			if gdocCount > 0 {
				go startGdocTicker(ctx, a)
			}
		}

		fmt.Printf("\n  Watching %d director(ies), %d source ticker(s), %d URL ticker(s)%s. Press Ctrl+C to stop.\n\n",
			watchCount, tickerCount, urlTickerCount, gdocSuffix(gdocCount))

		// pending tracks debounce timers per path.
		// mu guards pending — AfterFunc callbacks run in separate goroutines.
		pending := make(map[string]*time.Timer)
		var mu sync.Mutex

		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove) == 0 {
					continue
				}

				parentDir := filepath.Dir(event.Name)
				action, known := watchedDirs[parentDir]
				if !known {
					continue
				}

				switch action {
				case actionPersonalFile:
					// Only process Create/Write of supported file types.
					if event.Op&(fsnotify.Create|fsnotify.Write) == 0 {
						continue
					}
					if !watchedExtensions[strings.ToLower(filepath.Ext(event.Name))] {
						continue
					}
					debounce(&mu, pending, event.Name, settleDelay, func(path string) {
						processWatchedFile(context.Background(), a, path)
					})

				case actionWorkspace:
					// Skip events caused by our own snapshot write to avoid a feedback loop.
					if filepath.Base(event.Name) == "dir_structure.md" {
						continue
					}
					debounce(&mu, pending, workspaceRoot+"/_snapshot", settleDelay, func(_ string) {
						regenerateWorkspaceSnapshot(context.Background(), a, workspaceRoot)
					})

				case actionRepoRoot:
					// Only care about new directories being created.
					if event.Op&fsnotify.Create == 0 {
						continue
					}
					rootName := repoRootNames[parentDir]
					debounce(&mu, pending, event.Name, repoSettleDelay, func(path string) {
						checkNewRepo(context.Background(), a, rootName, path)
					})
				}

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

// debounce resets or creates a settle timer for key. When the timer fires,
// fn is called with path. Safe for concurrent use.
func debounce(mu *sync.Mutex, pending map[string]*time.Timer, key string, delay time.Duration, fn func(string)) {
	mu.Lock()
	if t, exists := pending[key]; exists {
		t.Stop()
	}
	k := key // capture for closure
	pending[key] = time.AfterFunc(delay, func() {
		mu.Lock()
		delete(pending, k)
		mu.Unlock()
		fn(k)
	})
	mu.Unlock()
}

// processWatchedFile runs classify → move → ingest for a personal intake file.
// Errors are logged but never fatal — the watcher keeps running.
func processWatchedFile(ctx context.Context, a *app.Application, path string) {
	if _, err := os.Stat(path); err != nil {
		// File moved or deleted before the settle timer fired.
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

// regenerateWorkspaceSnapshot generates dir_structure.md and ingests it.
func regenerateWorkspaceSnapshot(ctx context.Context, a *app.Application, workspaceRoot string) {
	outPath, err := workspace.WriteTo(workspaceRoot)
	if err != nil {
		logger.Warn(ctx, "workspace: snapshot generation failed", slog.Any("err", err))
		return
	}
	if _, err := ingestion.IngestFile(ctx, a, outPath, "workspace-structure", true, nil); err != nil {
		logger.Warn(ctx, "workspace: ingest failed", slog.Any("err", err))
		return
	}
	logger.Info(ctx, "workspace.snapshot_updated", slog.String("path", outPath))
	fmt.Printf("  ✓ Workspace snapshot updated: %s\n", outPath)
}

// checkNewRepo checks if path is a newly cloned git repo, logs it, and
// registers it in the database via the Repos model.
func checkNewRepo(ctx context.Context, a *app.Application, rootName, path string) {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return
	}
	if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
		return
	}

	logger.Info(ctx, "repo.detected",
		slog.String("root", rootName),
		slog.String("path", path))
	fmt.Printf("  → New repo detected in %s: %s\n", rootName, filepath.Base(path))

	// Register in DB so nexus repo check can find it immediately.
	remote := repoNormaliseRemote(repoGitRun(ctx, path, "remote", "get-url", "origin"))
	repoType := repoTypeFromName(rootName)
	if err := a.Repos.Upsert(ctx, models.Repo{
		Path:      path,
		RemoteURL: remote,
		Platform:  detectPlatform(remote),
		RepoType:  repoType,
		RootName:  rootName,
	}); err != nil {
		logger.Warn(ctx, "repo.register_failed",
			slog.String("path", path),
			slog.Any("err", err))
	}

	// Regenerate the workspace snapshot so dir_structure.md reflects the new repo.
	if ws := a.Config.Roots.Workspace; ws != "" {
		regenerateWorkspaceSnapshot(ctx, a, ws)
	}
}

// startURLTicker polls a URL source immediately then re-polls at the configured
// interval. Stops when ctx is cancelled.
func startURLTicker(ctx context.Context, a *app.Application, u config.URLSource) {
	pollURLSource(ctx, a, u)

	ticker := time.NewTicker(parseURLInterval(u.Interval))
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			pollURLSource(ctx, a, u)
		case <-ctx.Done():
			return
		}
	}
}

// pollURLSource re-ingests a URL source. Unchanged pages are skipped by
// hash-based dedup inside CrawlAndIngest.
func pollURLSource(ctx context.Context, a *app.Application, u config.URLSource) {
	logger.Info(ctx, "url.poll_start",
		slog.String("source", u.Name),
		slog.String("url", u.URL))
	fmt.Printf("  ⟳ Crawling %q (%s)…\n", u.Name, u.URL)

	count, err := ingestion.CrawlAndIngest(ctx, a, u.URL, u.Name, u.Depth, parseDelay(u.Delay), false, false, u.Exclude)
	if err != nil {
		logger.Warn(ctx, "url.poll_error",
			slog.String("source", u.Name),
			slog.Any("err", err))
		fmt.Printf("  ✗ %q: crawl error — %v\n", u.Name, err)
		return
	}
	if count > 0 {
		logger.Info(ctx, "url.poll_done",
			slog.String("source", u.Name),
			slog.Int("ingested", count))
		fmt.Printf("  ✓ %q: %d page(s) updated\n", u.Name, count)
	} else {
		fmt.Printf("  ✓ %q: up to date (no changes)\n", u.Name)
	}
}

// parseURLInterval converts a duration string (e.g. "6h", "24h") to a
// time.Duration. Falls back to defaultURLTick if the string is empty or invalid.
func parseURLInterval(s string) time.Duration {
	if s == "" {
		return defaultURLTick
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < time.Minute {
		return defaultURLTick
	}
	return d
}

// startSourceTicker runs an immediate scan of src then re-scans every
// sourceScanTick interval. Stops when ctx is cancelled.
func startSourceTicker(ctx context.Context, a *app.Application, src config.Source) {
	scanSource(ctx, a, src)

	ticker := time.NewTicker(sourceScanTick)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			scanSource(ctx, a, src)
		case <-ctx.Done():
			return
		}
	}
}

// scanSource walks src.Path and calls IngestFile for each matching file.
// Unchanged files are skipped via hash-based dedup in IngestFile.
func scanSource(ctx context.Context, a *app.Application, src config.Source) {
	var ingested, skipped int

	err := filepath.WalkDir(src.Path, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
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
			ok, err := ingestion.IngestFile(ctx, a, path, src.Name, false, nil)
			if err != nil {
				logger.Warn(ctx, "source.scan_error",
					slog.String("source", src.Name),
					slog.String("file", path),
					slog.Any("err", err))
				return nil
			}
			if ok {
				ingested++
			} else {
				skipped++
			}
			break
		}
		return nil
	})

	if err != nil {
		logger.Warn(ctx, "source.scan_walk_error",
			slog.String("source", src.Name),
			slog.Any("err", err))
	}

	if ingested > 0 {
		logger.Info(ctx, "source.scan_done",
			slog.String("source", src.Name),
			slog.Int("ingested", ingested),
			slog.Int("skipped", skipped))
	}
}

// startGdocTicker syncs all registered Google Docs immediately then every gdocSyncTick.
func startGdocTicker(ctx context.Context, a *app.Application) {
	syncAllGdocs(ctx, a)

	ticker := time.NewTicker(gdocSyncTick)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			syncAllGdocs(ctx, a)
		case <-ctx.Done():
			return
		}
	}
}

// syncAllGdocs re-fetches and re-ingests all registered Google Docs.
func syncAllGdocs(ctx context.Context, a *app.Application) {
	if a.Config.Gdoc.CredentialsPath == "" {
		return
	}
	docs, err := a.Gdocs.List(ctx)
	if err != nil {
		logger.Warn(ctx, "gdoc.sync_list_failed", "err", err)
		return
	}
	for _, d := range docs {
		if err := syncGdoc(ctx, a, d); err != nil {
			logger.Warn(ctx, "gdoc.sync_failed", "name", d.Name, "err", err)
		} else {
			logger.Info(ctx, "gdoc.synced", "name", d.Name)
		}
	}
}

// gdocSuffix formats the Google Docs count for the startup message.
func gdocSuffix(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf(", %d Google Doc(s)", n)
}

// printWatchList shows what nexus watch would monitor without starting.
func printWatchList(a *app.Application) {
	home, _ := os.UserHomeDir()
	display := func(p string) string { return strings.Replace(p, home, "~", 1) }

	fmt.Println()
	fmt.Println("  nexus watch — configured watchers")
	fmt.Println()

	fmt.Println("  Personal intake (classify → file → ingest on new file):")
	for _, d := range a.Config.Personal.WatchDirs {
		fmt.Printf("    %s\n", display(d))
	}

	fmt.Println()
	fmt.Println("  Source tickers (re-ingest on change, every 5 min):")
	tickers := 0
	for _, src := range a.Config.Sources {
		if src.Watch {
			fmt.Printf("    %-20s  %s\n", src.Name, display(src.Path))
			tickers++
		}
	}
	if tickers == 0 {
		fmt.Println("    (none — set watch: true on a source to enable)")
	}

	fmt.Println()
	fmt.Println("  Workspace snapshot (regenerate dir_structure.md on structural change):")
	if ws := a.Config.Roots.Workspace; ws != "" {
		fmt.Printf("    %s\n", display(ws))
	} else {
		fmt.Println("    (none — set roots.workspace in config to enable)")
	}

	fmt.Println()
	fmt.Println("  Repo roots (detect newly cloned repos):")
	repoWatches := 0
	for _, r := range a.Config.Roots.Repos {
		if r.Watch {
			fmt.Printf("    %-20s  %s\n", r.Name, display(r.Path))
			repoWatches++
		}
	}
	if repoWatches == 0 {
		fmt.Println("    (none — set watch: true on a repo root to enable)")
	}

	fmt.Println()
	fmt.Println("  URL sources (re-fetch on interval, default 24h):")
	urlWatches := 0
	for _, u := range a.Config.URLs {
		if u.Watch {
			recursive := ""
			if u.Recursive {
				recursive = " [recursive]"
			}
			interval := parseURLInterval(u.Interval)
			fmt.Printf("    %-20s  %s%s  every %s\n", u.Name, u.URL, recursive, interval)
			urlWatches++
		}
	}
	if urlWatches == 0 {
		fmt.Println("    (none — add urls: entries with watch: true in config.yaml)")
	}

	fmt.Println()
	fmt.Println("  Google Docs (re-sync every 30 min):")
	if a.Config.Gdoc.CredentialsPath != "" {
		docs, err := a.Gdocs.List(context.Background())
		if err != nil || len(docs) == 0 {
			fmt.Println("    (none — add one with: nexus gdoc add <url> --name <name>)")
		} else {
			for _, d := range docs {
				fmt.Printf("    %-20s  %s\n", d.Name, d.DocID)
			}
		}
	} else {
		fmt.Println("    (not configured — set gdoc.credentialsPath in config.yaml)")
	}
	fmt.Println()
}

func init() {
	watchCmd.Flags().BoolVar(&watchList, "list", false, "print configured watchers without starting")
	RootCmd.AddCommand(watchCmd)
}
