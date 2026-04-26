package nexus

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/iamaina/nexus/internal/app"
	"github.com/iamaina/nexus/internal/logger"
	"github.com/jackc/pgx/v5"
	"github.com/spf13/cobra"
)

// Index name is a constant so status, rebuild, and watch all refer to the same object.
const indexName = "chunks_embedding_idx"

// Rebuild thresholds — see docs/commands.md for the rationale.
const (
	growthWarnRatio   = 1.5                    // recommend REINDEX when chunk count grows 50%+ from build time
	listsWarnRatio    = 1.3                    // recommend --resize when optimal lists is 30%+ above current
	minMaintenanceMem = 2 * 1024 * 1024 * 1024 // 2 GB in bytes
	targetBuildMem    = "5GB"                  // set for the current session during rebuild
)

// indexMeta is stored as a JSON comment on chunks_embedding_idx.
// This lets nexus index status compare the current chunk count against
// the count at build time without any extra tables or files.
type indexMeta struct {
	Chunks  int64  `json:"chunks"` // chunk count at build time
	Lists   int    `json:"lists"`  // lists value used
	BuiltAt string `json:"ts"`     // RFC3339 build timestamp
}

var indexResizeFlag bool

var indexCmd = &cobra.Command{
	Use:   "index",
	Short: "Manage the vector search index",
	Long: `Commands for monitoring and rebuilding the IVFFlat vector index on chunks.

The index accelerates semantic search by partitioning all chunk vectors into
buckets (lists) at build time. Queries scan only the few nearest buckets
(controlled by probes=10) instead of all rows — ~400× faster on 4M+ chunks.

The index degrades as the chunk count grows: the original bucket centroids
no longer reflect the current data distribution, recall silently drops, and
some buckets grow very large. nexus watch checks index health every 24 h and
logs a warning when a rebuild is needed.

Since: v0.3.0`,
}

// ── nexus index status ────────────────────────────────────────────────────────

var indexStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show index health and rebuild recommendation",
	Long: `Check whether chunks_embedding_idx is healthy relative to the current
chunk count. Compares the current count against the count recorded at build
time, computes the optimal lists value, and recommends the exact command to
run if action is needed.

Read-only — no database writes.

Since: v0.3.0`,
	Run: func(cmd *cobra.Command, _ []string) {
		ctx := cmd.Context()
		a, ok := ctx.Value(app.AppKey).(*app.Application)
		if !ok {
			logger.Error(ctx, "Application not found in context")
			return
		}
		printIndexStatus(ctx, a)
	},
}

// ── nexus index rebuild ───────────────────────────────────────────────────────

var indexRebuildCmd = &cobra.Command{
	Use:   "rebuild",
	Short: "Rebuild the vector index",
	Long: `Rebuild chunks_embedding_idx to restore full search performance.

Default (no flags):
  REINDEX INDEX CONCURRENTLY — recomputes bucket centroids using the same
  lists value. Queries continue to work during the rebuild. Use when the
  chunk count has grown 50–100% from the build count.

--resize:
  Drops the index and recreates it with an optimal lists value
  (current_count / 1000). Queries fall back to sequential scan during the
  rebuild window (typically 15–30 min at 4–8M chunks). Use when chunk count
  has more than doubled.

Both modes automatically handle maintenance_work_mem:
  - Sets 5 GB for the current session (required for k-means on large datasets)
  - Permanently fixes the system setting to 2 GB if it was below that

After completion the build count and lists value are stored as a comment on
the index so future "nexus index status" calls have a baseline to compare.

Since: v0.3.0`,
	Run: func(cmd *cobra.Command, _ []string) {
		ctx := cmd.Context()
		a, ok := ctx.Value(app.AppKey).(*app.Application)
		if !ok {
			logger.Error(ctx, "Application not found in context")
			return
		}
		runIndexRebuild(ctx, a, indexResizeFlag)
	},
}

// ── printIndexStatus ──────────────────────────────────────────────────────────

func printIndexStatus(ctx context.Context, a *app.Application) {
	info, err := loadIndexInfo(ctx, a)
	if err != nil {
		logger.Error(ctx, fmt.Sprintf("index status: %v", err))
		return
	}

	fmt.Println()

	if !info.exists {
		fmt.Println("  ❌  Index does not exist.")
		fmt.Println()
		fmt.Println("  Run:  nexus index rebuild")
		fmt.Println()
		return
	}
	if !info.valid {
		fmt.Println("  ❌  Index exists but is invalid (build was interrupted or failed).")
		fmt.Println()
		fmt.Println("  Run:  nexus index rebuild")
		fmt.Println()
		return
	}

	fmt.Printf("  Index:  %s  (IVFFlat, lists=%d)\n", indexName, info.lists)

	if info.meta != nil {
		builtAt := info.meta.BuiltAt
		if t, err2 := time.Parse(time.RFC3339, builtAt); err2 == nil {
			builtAt = t.Format("2006-01-02")
		}
		fmt.Printf("  Built:  %s with %s chunks\n", builtAt, fmtInt(info.meta.Chunks))
	} else {
		fmt.Println("  Built:  (no metadata — index predates nexus index rebuild)")
	}

	fmt.Printf("  Now:    %s chunks", fmtInt(info.currentChunks))

	var growthPct float64
	if info.meta != nil && info.meta.Chunks > 0 {
		growthPct = float64(info.currentChunks-info.meta.Chunks) / float64(info.meta.Chunks) * 100
		fmt.Printf("  (+%.0f%%)\n", growthPct)
	} else {
		fmt.Println()
	}

	optLists := optimalListsFor(info.currentChunks)
	listsRatio := float64(optLists) / float64(info.lists)
	needsResize := listsRatio >= listsWarnRatio
	needsReindex := info.meta != nil && info.meta.Chunks > 0 &&
		growthPct >= (growthWarnRatio-1)*100

	fmt.Println()

	switch {
	case needsResize:
		fmt.Printf("  ⚠️   lists=%d is significantly below optimal (%d).\n", info.lists, optLists)
		fmt.Println("      Bucket centroids no longer reflect the current data distribution.")
		fmt.Println()
		fmt.Printf("  Run:  nexus index rebuild --resize   ← drop + recreate with lists=%d\n", optLists)

	case needsReindex:
		fmt.Printf("  ⚠️   Chunk count has grown %.0f%% since the index was built.\n", growthPct)
		fmt.Println("      Bucket centroids are becoming stale. REINDEX recommended.")
		if listsRatio > 1.0 {
			fmt.Printf("      (lists still acceptable at %d; optimal would be %d)\n",
				info.lists, optLists)
		}
		fmt.Println()
		fmt.Println("  Run:  nexus index rebuild")

	default:
		fmt.Println("  ✅  Index is healthy. No action needed.")
		if info.meta != nil && info.meta.Chunks > 0 {
			rebuildAt := int64(float64(info.meta.Chunks) * growthWarnRatio)
			fmt.Printf("      Rebuild recommended when chunks reach ~%s.\n", fmtInt(rebuildAt))
		}
	}

	fmt.Println()
}

// ── runIndexRebuild ───────────────────────────────────────────────────────────

func runIndexRebuild(ctx context.Context, a *app.Application, resize bool) {
	fmt.Println()

	// Ensure maintenance_work_mem is adequate for the k-means build step.
	if err := ensureMaintenanceMem(ctx, a); err != nil {
		logger.Error(ctx, fmt.Sprintf("index rebuild: memory check: %v", err))
		return
	}

	var currentChunks int64
	if err := a.DB.QueryRow(ctx, `SELECT COUNT(*) FROM chunks`).Scan(&currentChunks); err != nil {
		logger.Error(ctx, fmt.Sprintf("index rebuild: chunk count: %v", err))
		return
	}

	if resize {
		newLists := optimalListsFor(currentChunks)
		fmt.Printf("  Rebuilding with lists=%d (%s chunks)...\n", newLists, fmtInt(currentChunks))
		fmt.Println("  Queries fall back to sequential scan during this window.")
		fmt.Println()

		if _, err := a.DB.Exec(ctx, `DROP INDEX IF EXISTS `+indexName); err != nil {
			logger.Error(ctx, fmt.Sprintf("index rebuild: drop: %v", err))
			return
		}

		createSQL := fmt.Sprintf(
			`CREATE INDEX CONCURRENTLY %s ON chunks USING ivfflat (embedding vector_cosine_ops) WITH (lists = %d)`,
			indexName, newLists,
		)
		if _, err := a.DB.Exec(ctx, createSQL); err != nil {
			logger.Error(ctx, fmt.Sprintf("index rebuild: create: %v", err))
			return
		}

	} else {
		fmt.Printf("  Reindexing %s (%s chunks)...\n", indexName, fmtInt(currentChunks))
		fmt.Println("  Queries continue to work during the rebuild.")
		fmt.Println()

		if _, err := a.DB.Exec(ctx, `REINDEX INDEX CONCURRENTLY `+indexName); err != nil {
			logger.Error(ctx, fmt.Sprintf("index rebuild: reindex: %v", err))
			return
		}
	}

	// Record build metadata as a comment on the index so future status checks
	// have a baseline. REINDEX drops and recreates the index object, so the
	// comment is always re-set after the build completes.
	meta := indexMeta{
		Chunks:  currentChunks,
		Lists:   resolveCurrentLists(ctx, a),
		BuiltAt: time.Now().UTC().Format(time.RFC3339),
	}
	if b, err := json.Marshal(meta); err == nil {
		commentSQL := fmt.Sprintf(`COMMENT ON INDEX %s IS '%s'`, indexName, string(b))
		if _, err := a.DB.Exec(ctx, commentSQL); err != nil {
			logger.Warn(ctx, fmt.Sprintf("index rebuild: store metadata: %v", err))
		}
	}

	fmt.Printf("  ✅  Done. Built from %s chunks", fmtInt(currentChunks))
	if resize {
		fmt.Printf(" with lists=%d.\n", meta.Lists)
	} else {
		fmt.Println(".")
	}
	nextRebuild := int64(float64(currentChunks) * growthWarnRatio)
	fmt.Printf("  Next rebuild recommended at ~%s chunks.\n\n", fmtInt(nextRebuild))
}

// ── checkIndexHealth (called by nexus watch) ──────────────────────────────────

// checkIndexHealth is the background health check run by nexus watch every 24 h.
// It logs a warning if a rebuild is recommended — no output to stdout, no rebuild.
func checkIndexHealth(ctx context.Context, a *app.Application) {
	info, err := loadIndexInfo(ctx, a)
	if err != nil {
		logger.Warn(ctx, "index.health_check_failed", slog.Any("err", err))
		return
	}
	if !info.exists {
		logger.Warn(ctx, "index.missing",
			slog.String("hint", "run: nexus index rebuild"))
		return
	}
	if !info.valid {
		logger.Warn(ctx, "index.invalid",
			slog.String("hint", "run: nexus index rebuild"))
		return
	}
	if info.meta == nil || info.meta.Chunks == 0 {
		return // no baseline — index predates metadata tracking
	}

	optLists := optimalListsFor(info.currentChunks)
	listsRatio := float64(optLists) / float64(info.lists)
	growthPct := float64(info.currentChunks-info.meta.Chunks) / float64(info.meta.Chunks) * 100

	switch {
	case listsRatio >= listsWarnRatio:
		logger.Warn(ctx, "index.rebuild_recommended",
			slog.String("reason", "lists significantly below optimal"),
			slog.Int("current_lists", info.lists),
			slog.Int("optimal_lists", optLists),
			slog.String("hint", "run: nexus index rebuild --resize"))
	case growthPct >= (growthWarnRatio-1)*100:
		logger.Warn(ctx, "index.reindex_recommended",
			slog.Float64("growth_pct", growthPct),
			slog.Int64("built_from", info.meta.Chunks),
			slog.Int64("current", info.currentChunks),
			slog.String("hint", "run: nexus index rebuild"))
	}
}

// ── internal helpers ──────────────────────────────────────────────────────────

type indexInfo struct {
	exists        bool
	valid         bool
	lists         int
	currentChunks int64
	meta          *indexMeta
}

// loadIndexInfo reads pg_index for the named index and returns a populated
// indexInfo. Returns indexInfo{exists: false} with no error if the index
// does not exist.
func loadIndexInfo(ctx context.Context, a *app.Application) (indexInfo, error) {
	var info indexInfo

	var valid bool
	var indexDef string
	var comment *string

	err := a.DB.QueryRow(ctx, `
		SELECT i.indisvalid,
		       pg_get_indexdef(i.indexrelid),
		       obj_description(i.indexrelid, 'pg_class')
		FROM pg_index i
		JOIN pg_class c ON c.oid = i.indexrelid
		WHERE c.relname = $1
	`, indexName).Scan(&valid, &indexDef, &comment)

	if err == pgx.ErrNoRows {
		return indexInfo{exists: false}, nil
	}
	if err != nil {
		return info, fmt.Errorf("pg_index lookup: %w", err)
	}

	info.exists = true
	info.valid = valid
	info.lists = parseListsFromDef(indexDef)

	if comment != nil && *comment != "" {
		var m indexMeta
		if json.Unmarshal([]byte(*comment), &m) == nil {
			info.meta = &m
		}
	}

	if err := a.DB.QueryRow(ctx, `SELECT COUNT(*) FROM chunks`).
		Scan(&info.currentChunks); err != nil {
		return info, fmt.Errorf("chunk count: %w", err)
	}

	return info, nil
}

// ensureMaintenanceMem checks the current maintenance_work_mem setting.
// If it is below 2 GB it permanently raises it via ALTER SYSTEM and reloads
// the config. In both cases it sets 5 GB for the current session so the
// index build has plenty of headroom.
func ensureMaintenanceMem(ctx context.Context, a *app.Application) error {
	var current string
	if err := a.DB.QueryRow(ctx, `SHOW maintenance_work_mem`).Scan(&current); err != nil {
		return fmt.Errorf("SHOW maintenance_work_mem: %w", err)
	}

	if parseMemSetting(current) < minMaintenanceMem {
		fmt.Printf("  maintenance_work_mem is %s — raising to 2 GB permanently.\n", current)
		if _, err := a.DB.Exec(ctx, `ALTER SYSTEM SET maintenance_work_mem = '2GB'`); err != nil {
			return fmt.Errorf("ALTER SYSTEM: %w", err)
		}
		if _, err := a.DB.Exec(ctx, `SELECT pg_reload_conf()`); err != nil {
			return fmt.Errorf("pg_reload_conf: %w", err)
		}
		fmt.Println("  ✓ System setting updated (persists across restarts).")
	}

	// Always set 5 GB for the current session — more headroom than the 2 GB
	// permanent setting and avoids a reconnect to pick up the ALTER SYSTEM.
	if _, err := a.DB.Exec(ctx, `SET maintenance_work_mem = '`+targetBuildMem+`'`); err != nil {
		return fmt.Errorf("SET maintenance_work_mem: %w", err)
	}

	return nil
}

// optimalListsFor returns the recommended lists value for a given chunk count.
// Rule: lists ≈ chunk_count / 1000, minimum 100.
func optimalListsFor(chunks int64) int {
	if chunks <= 0 {
		return 100
	}
	lists := int((chunks + 500) / 1000) // integer round-to-nearest
	if lists < 100 {
		return 100
	}
	return lists
}

// resolveCurrentLists reads the lists value from the live index definition
// after a rebuild completes.
func resolveCurrentLists(ctx context.Context, a *app.Application) int {
	var def string
	_ = a.DB.QueryRow(ctx,
		`SELECT pg_get_indexdef(i.indexrelid)
		 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
		 WHERE c.relname = $1`, indexName,
	).Scan(&def)
	return parseListsFromDef(def)
}

// parseListsFromDef extracts the lists value from a pg_get_indexdef string.
// Handles both WITH (lists=4000) and WITH (lists='4000') forms.
func parseListsFromDef(def string) int {
	lower := strings.ToLower(def)
	// Find "lists=" (with or without spaces around =).
	for _, needle := range []string{"lists='", `lists="`, "lists="} {
		idx := strings.Index(lower, needle)
		if idx < 0 {
			continue
		}
		rest := def[idx+len(needle):]
		rest = strings.TrimPrefix(rest, "'")
		rest = strings.TrimPrefix(rest, `"`)
		end := strings.IndexAny(rest, `'"  ),`)
		if end < 0 {
			end = len(rest)
		}
		if n, err := strconv.Atoi(strings.TrimSpace(rest[:end])); err == nil {
			return n
		}
	}
	return 0
}

// parseMemSetting converts a PostgreSQL memory string ("64MB", "2GB", "512kB")
// to bytes.
func parseMemSetting(s string) int64 {
	s = strings.TrimSpace(strings.ToLower(s))
	for suffix, mult := range map[string]int64{
		"gb": 1024 * 1024 * 1024,
		"mb": 1024 * 1024,
		"kb": 1024,
	} {
		if rest, ok := strings.CutSuffix(s, suffix); ok {
			n, _ := strconv.ParseFloat(rest, 64)
			return int64(n * float64(mult))
		}
	}
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

// fmtInt formats a large integer with comma separators for readability.
func fmtInt(n int64) string {
	s := strconv.FormatInt(n, 10)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	offset := len(s) % 3
	if offset > 0 {
		b.WriteString(s[:offset])
	}
	for i := offset; i < len(s); i += 3 {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

func init() {
	indexRebuildCmd.Flags().BoolVar(&indexResizeFlag, "resize", false,
		"drop and recreate with optimal lists value instead of REINDEX")
	indexCmd.AddCommand(indexStatusCmd)
	indexCmd.AddCommand(indexRebuildCmd)
	RootCmd.AddCommand(indexCmd)
}
