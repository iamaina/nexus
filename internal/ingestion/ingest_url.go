package ingestion

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/iamaina/nexus/internal/app"
	"github.com/iamaina/nexus/internal/layout"
	"github.com/iamaina/nexus/internal/logger"
	"github.com/iamaina/nexus/internal/models"
)

// IngestURL fetches a single URL, extracts its content, and runs the full
// ingestion pipeline. The URL is used as the document path for deduplication.
// Returns true if the page was ingested, false if skipped (up to date).
func IngestURL(ctx context.Context, a *app.Application, rawURL, source string, force bool) (bool, error) {
	body, err := fetchURL(ctx, rawURL)
	if err != nil {
		return false, fmt.Errorf("fetch %s: %w", rawURL, err)
	}
	return ingestURLBody(ctx, a, rawURL, source, force, body)
}

// ingestURLBody runs the ingestion pipeline on a pre-fetched page body.
// Separating fetch from ingest allows CrawlAndIngest to reuse the body
// for link extraction without a second HTTP request.
func ingestURLBody(ctx context.Context, a *app.Application, rawURL, source string, force bool, body []byte) (bool, error) {
	start := time.Now()
	hash := hashBytes(body)

	if !force {
		upToDate, err := a.Documents.IsUpToDate(ctx, rawURL, hash)
		if err != nil {
			return false, fmt.Errorf("dedup check: %w", err)
		}
		if upToDate {
			logger.Info(ctx, "url.skipped",
				slog.String("component", "ingestion"),
				slog.String("event", "url.skipped"),
				slog.String("source", source),
				slog.String("url", rawURL),
				slog.String("reason", "up_to_date"),
			)
			return false, nil
		}
	}

	logger.Info(ctx, "url.start",
		slog.String("component", "ingestion"),
		slog.String("event", "url.start"),
		slog.String("source", source),
		slog.String("url", rawURL),
	)

	spans, err := layout.ExtractHTML(bytes.NewReader(body))
	if err != nil {
		return false, fmt.Errorf("extract HTML %s: %w", rawURL, err)
	}
	if len(spans) == 0 {
		logger.Info(ctx, "url.skipped",
			slog.String("component", "ingestion"),
			slog.String("event", "url.skipped"),
			slog.String("source", source),
			slog.String("url", rawURL),
			slog.String("reason", "no_content"),
		)
		return false, nil
	}

	lines := layout.GroupSpansIntoLines(spans, 2.0)
	bodyFont, freq := layout.AnalyzeFonts(lines)
	fontLevels := layout.BuildFontLevels(freq, bodyFont)
	headings := layout.DetectHeadings(lines, bodyFont, fontLevels)
	headings = layout.MergeWrappedHeadings(headings)
	blocks := layout.BuildBlocks(lines, bodyFont)
	tree := layout.BuildHeadingTree(headings)
	tree = layout.TrimFrontMatter(tree)
	layout.AttachBlocks(tree, blocks)
	layout.MergeNodeLists(tree)
	sections := layout.BuildSections(tree)

	chunks := layout.ChunkSections(sections, 5)
	if len(chunks) == 0 {
		if len(blocks) == 0 {
			return false, nil
		}
		title := pageTitleFromURL(rawURL)
		flat := layout.Section{Title: title, Level: 1, Content: blocks}
		chunks = layout.ChunkSections([]layout.Section{flat}, 5)
	}

	enriched := make([]models.EnrichedChunk, len(chunks))
	texts := make([]string, len(chunks))
	charCount := 0
	for i, c := range chunks {
		text := layout.ChunkToText(c)
		enriched[i] = models.EnrichedChunk{Text: text, Chapter: c.Title, Level: c.Level}
		embeddedText := text
		if c.Title != "" {
			embeddedText = c.Title + "\n" + text
		}
		texts[i] = embeddedText
		charCount += len(text)
	}

	docID, err := a.Documents.Insert(ctx, source, rawURL, hash, charCount, len(chunks), nil)
	if err != nil {
		return false, fmt.Errorf("store document: %w", err)
	}
	if err := a.Chunks.Store(ctx, docID, enriched); err != nil {
		return false, fmt.Errorf("store chunks: %w", err)
	}
	embeddings, err := a.Embedder.Embed(ctx, texts)
	if err != nil {
		return false, fmt.Errorf("embed chunks: %w", err)
	}
	if err := a.Chunks.StoreEmbeddings(ctx, docID, embeddings); err != nil {
		return false, fmt.Errorf("store embeddings: %w", err)
	}

	logger.Info(ctx, "url.done",
		slog.String("component", "ingestion"),
		slog.String("event", "url.done"),
		slog.String("source", source),
		slog.String("url", rawURL),
		slog.Int64("doc_id", docID),
		slog.Int("chunk_count", len(chunks)),
		slog.Int("char_count", charCount),
		slog.Int64("duration_ms", time.Since(start).Milliseconds()),
	)
	return true, nil
}

// CrawlAndIngest fetches seedURL and, when recursive is true, follows all links
// within the same scheme+host+path prefix. Each page is fetched once — the body
// is reused for both ingestion and link extraction. delay is inserted between
// requests to avoid hammering the server (0 = no delay).
// excludePatterns are URL path substrings; any discovered link whose URL contains
// one of these strings is skipped entirely (never fetched or queued).
// Returns the number of pages ingested (dry-run: pages that would be ingested).
func CrawlAndIngest(ctx context.Context, a *app.Application, seedURL, source string, maxDepth int, delay time.Duration, force, dryRun bool, excludePatterns []string) (int, error) {
	seed, err := url.Parse(seedURL)
	if err != nil {
		return 0, fmt.Errorf("parse seed URL: %w", err)
	}
	seed = seed.JoinPath()

	type entry struct {
		rawURL string
		depth  int
	}

	// Pre-seed visited from the database so restarts skip pages that are
	// already ingested without fetching them (each fetch costs a polite delay).
	// Skipped when force=true so --force always re-fetches everything.
	visited := map[string]bool{seed.String(): true}
	if !force {
		for u := range loadVisitedURLs(ctx, a, source) {
			visited[u] = true
		}
		if len(visited) > 1 {
			logger.Info(ctx, "url.crawl_resume",
				slog.String("source", source),
				slog.Int("already_visited", len(visited)-1),
			)
		}
	}

	queue := []entry{{seed.String(), 0}}
	ingested := 0
	fetched := 0

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		// Polite crawling — pause between requests after the first.
		if fetched > 0 && delay > 0 {
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return ingested, nil
			}
		}

		// Fetch once — reuse for both ingestion and link discovery.
		body, fetchErr := fetchURL(ctx, cur.rawURL)
		fetched++
		if fetchErr != nil {
			logger.Warn(ctx, "url.fetch_error",
				slog.String("component", "ingestion"),
				slog.String("url", cur.rawURL),
				slog.Any("err", fetchErr),
			)
			continue
		}

		if dryRun {
			fmt.Printf("  would ingest: %s\n", cur.rawURL)
			ingested++
		} else {
			ok, err := ingestURLBody(ctx, a, cur.rawURL, source, force, body)
			if err != nil {
				logger.Warn(ctx, "url.ingest_error",
					slog.String("component", "ingestion"),
					slog.String("url", cur.rawURL),
					slog.Any("err", err),
				)
			} else if ok {
				ingested++
			}
		}

		// Discover links for the next depth level.
		if maxDepth > 0 && cur.depth >= maxDepth {
			continue
		}

		links, err := layout.ExtractLinks(bytes.NewReader(body))
		if err != nil {
			continue
		}

		base, _ := url.Parse(cur.rawURL)
		for _, href := range links {
			resolved, err := resolveLink(base, seed, href)
			if err != nil || visited[resolved] {
				continue
			}
			if isExcluded(resolved, excludePatterns) {
				continue
			}
			visited[resolved] = true
			queue = append(queue, entry{resolved, cur.depth + 1})
		}
	}

	return ingested, nil
}

// loadVisitedURLs queries the documents table for all file_paths ingested
// under source. The returned map is used to pre-seed the visited set in
// CrawlAndIngest so restarts do not re-fetch already-ingested pages.
func loadVisitedURLs(ctx context.Context, a *app.Application, source string) map[string]bool {
	rows, err := a.DB.Query(ctx,
		`SELECT file_path FROM documents WHERE source_name = $1`, source)
	if err != nil {
		return nil
	}
	defer rows.Close()

	visited := make(map[string]bool)
	for rows.Next() {
		var u string
		if rows.Scan(&u) == nil {
			visited[u] = true
		}
	}
	return visited
}

// isExcluded reports whether rawURL contains any of the given substrings.
func isExcluded(rawURL string, patterns []string) bool {
	for _, p := range patterns {
		if strings.Contains(rawURL, p) {
			return true
		}
	}
	return false
}

// resolveLink resolves href relative to base, returning the absolute URL only
// if it falls within the seed's scheme+host+path prefix.
func resolveLink(base, seed *url.URL, href string) (string, error) {
	if strings.HasPrefix(href, "#") ||
		strings.HasPrefix(href, "mailto:") ||
		strings.HasPrefix(href, "javascript:") {
		return "", fmt.Errorf("skip")
	}
	ref, err := url.Parse(href)
	if err != nil {
		return "", err
	}
	resolved := base.ResolveReference(ref)
	resolved.Fragment = ""
	if resolved.Scheme != seed.Scheme || resolved.Host != seed.Host {
		return "", fmt.Errorf("external")
	}
	if !strings.HasPrefix(resolved.Path, seed.Path) {
		return "", fmt.Errorf("outside prefix")
	}
	// Reject malformed paths — e.g. docs that contain broken links like
	// href="https//github.com/..." (missing colon) which resolve to a path
	// containing an embedded double-slash after the first character.
	if strings.Contains(resolved.Path[1:], "//") {
		return "", fmt.Errorf("malformed path")
	}
	return resolved.String(), nil
}

// fetchURL performs an HTTP GET and returns the response body.
func fetchURL(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "nexus/ingest-url (+https://github.com/iamaina/nexus)")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// hashBytes returns the SHA-256 hex digest of b.
func hashBytes(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// pageTitleFromURL derives a readable title from the last non-empty path segment.
func pageTitleFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i] != "" {
			return parts[i]
		}
	}
	return u.Host
}
