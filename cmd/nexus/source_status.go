package nexus

import (
	"fmt"
	"strings"

	"github.com/iamaina/nexus/internal/app"
	"github.com/spf13/cobra"
)

var sourceStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show ingestion status for all configured sources",
	Long: `Show per-source ingestion statistics: document count, chunk count,
last ingest time, watch interval, and default search visibility.

Sources listed in config.yaml but not yet ingested appear with zero counts
so you can see at a glance what still needs to be indexed.

Since: v0.3.0`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		ctx := cmd.Context()
		a, ok := ctx.Value(app.AppKey).(*app.Application)
		if !ok {
			return fmt.Errorf("application not available")
		}

		// Fetch stats for all sources that have been ingested.
		stats, err := a.Documents.SourceStats(ctx)
		if err != nil {
			return fmt.Errorf("query source stats: %w", err)
		}

		// Build a lookup: source name → stat
		byName := make(map[string]*ingestStat)
		for i := range stats {
			byName[stats[i].SourceName] = &ingestStat{
				docCount:   stats[i].DocCount,
				chunkCount: stats[i].ChunkCount,
				lastIngest: stats[i].LastIngest,
			}
		}

		// Build a combined list: all configured sources (file + URL) in order,
		// then any DB sources not in config (e.g. gdocs, workspace-structure).
		type row struct {
			name       string
			sourceType string // "file" | "url" | "other"
			watch      string // interval or "—"
			visibility string // "default" | "opt-in"
			docCount   int
			chunkCount int
			lastIngest string
		}

		seenInConfig := make(map[string]bool)
		var rows []row

		for _, s := range a.Config.Sources {
			seenInConfig[s.Name] = true
			r := row{
				name:       s.Name,
				sourceType: "file",
				watch:      "—",
				visibility: "default",
			}
			if s.Watch {
				r.watch = "5m"
			}
			if !s.IsSearchDefault() {
				r.visibility = "opt-in"
			}
			if st, ok := byName[s.Name]; ok {
				r.docCount = st.docCount
				r.chunkCount = st.chunkCount
				if st.lastIngest != nil {
					r.lastIngest = *st.lastIngest
				}
			}
			rows = append(rows, r)
		}

		for _, u := range a.Config.URLs {
			seenInConfig[u.Name] = true
			interval := parseURLInterval(u.Interval)
			watchStr := "—"
			if u.Watch {
				watchStr = formatDuration(interval)
			}
			r := row{
				name:       u.Name,
				sourceType: "url",
				watch:      watchStr,
				visibility: "default",
			}
			if !u.IsSearchDefault() {
				r.visibility = "opt-in"
			}
			if st, ok := byName[u.Name]; ok {
				r.docCount = st.docCount
				r.chunkCount = st.chunkCount
				if st.lastIngest != nil {
					r.lastIngest = *st.lastIngest
				}
			}
			rows = append(rows, r)
		}

		// Append any ingested sources not in config (gdocs, workspace-structure, etc.)
		for _, s := range stats {
			if seenInConfig[s.SourceName] {
				continue
			}
			r := row{
				name:       s.SourceName,
				sourceType: "other",
				watch:      "—",
				visibility: "default",
				docCount:   s.DocCount,
				chunkCount: s.ChunkCount,
			}
			if s.LastIngest != nil {
				r.lastIngest = *s.LastIngest
			}
			rows = append(rows, r)
		}

		if len(rows) == 0 {
			fmt.Println("  No sources configured. Run 'make setup' to get started.")
			return nil
		}

		// Column widths
		nameW := len("Source")
		for _, r := range rows {
			if len(r.name) > nameW {
				nameW = len(r.name)
			}
		}

		fmt.Println()
		fmt.Printf("  %-*s  %-4s  %7s  %9s  %-16s  %-6s  %s\n",
			nameW, "Source", "Type", "Docs", "Chunks", "Last Ingest", "Watch", "Visibility")
		fmt.Printf("  %s  %s  %s  %s  %s  %s  %s\n",
			strings.Repeat("─", nameW),
			strings.Repeat("─", 4),
			strings.Repeat("─", 7),
			strings.Repeat("─", 9),
			strings.Repeat("─", 16),
			strings.Repeat("─", 6),
			strings.Repeat("─", 10))

		for _, r := range rows {
			ingest := r.lastIngest
			if ingest == "" {
				ingest = "never"
			}
			docs := fmt.Sprintf("%d", r.docCount)
			chunks := fmt.Sprintf("%d", r.chunkCount)
			if r.docCount == 0 {
				docs = "—"
				chunks = "—"
			}
			note := ""
			if r.visibility == "opt-in" {
				note = "opt-in"
			}
			fmt.Printf("  %-*s  %-4s  %7s  %9s  %-16s  %-6s  %s\n",
				nameW, r.name, r.sourceType, docs, chunks, ingest, r.watch, note)
		}
		fmt.Println()

		// Summary
		var totalDocs, totalChunks int
		var notIngested int
		for _, r := range rows {
			totalDocs += r.docCount
			totalChunks += r.chunkCount
			if r.docCount == 0 && r.sourceType != "other" {
				notIngested++
			}
		}
		fmt.Printf("  Total: %d docs · %s chunks", totalDocs, formatChunks(totalChunks))
		if notIngested > 0 {
			fmt.Printf("  ·  %d source(s) not yet ingested — run: nexus ingest", notIngested)
		}
		fmt.Println()
		fmt.Println()
		return nil
	},
}

type ingestStat struct {
	docCount   int
	chunkCount int
	lastIngest *string
}

// formatDuration returns a short human-readable duration string.
func formatDuration(d interface{ String() string }) string {
	s := d.String()
	// Simplify Go's default duration strings: "168h0m0s" → "168h"
	s = strings.TrimSuffix(s, "0s")
	s = strings.TrimSuffix(s, "0m")
	s = strings.TrimSuffix(s, "0h")
	if s == "" {
		return "?"
	}
	return s
}

// formatChunks adds comma separators for readability.
func formatChunks(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%s,%03d", formatChunks(n/1000), n%1000)
}

func init() {
	sourceCmd.AddCommand(sourceStatusCmd)
}
