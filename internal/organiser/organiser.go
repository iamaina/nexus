// Package organiser classifies files, plans their destination, and executes moves.
// It powers the nexus organise command (Phase 3 of Workspace OS).
package organiser

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/iamaina/nexus/internal/app"
	"github.com/iamaina/nexus/internal/classifier"
	"github.com/iamaina/nexus/internal/config"
	"github.com/iamaina/nexus/internal/ingestion"
	"github.com/iamaina/nexus/internal/models"
)

// skipKind classifies why a file was not indexed.
type skipKind int

const (
	skipNoContent skipKind = iota // no extractable text (scanned PDF, etc.)
	skipDuplicate                 // same content already indexed at a different path
)

// FilePlan describes what will happen to one file.
type FilePlan struct {
	SrcPath  string
	DestPath string
	DestDir  string
	IsNew    bool // true if DestDir does not yet exist
	DocType  string
	Language string
	cl       *classifier.Classification
}

// Plan is the full set of FilePlans for one organise run.
type Plan struct {
	Items []FilePlan
}

// SupportedExtensions are the file types nexus organise handles.
var SupportedExtensions = map[string]bool{
	".pdf": true,
	".md":  true,
	".txt": true,
}

// technicalTypes are the only doc_types that route via the topic matcher to
// source directories. Everything else (including "report" which covers payslips,
// financial reports, etc.) routes to personal.destDir.
var technicalTypes = map[string]bool{
	"book": true, "article": true,
}

// Organiser classifies files and resolves their destinations.
type Organiser struct {
	cfg         *config.Config
	dirMap      map[string][]string // lowercase dir name → []absolute paths
	sourceRoots []string            // non-personal source roots, used as fallback for technical docs
}

// New builds an Organiser from the application config.
// It walks all configured source paths once to build the directory lookup map.
func New(cfg *config.Config) *Organiser {
	personalBase := cfg.Personal.DestDir

	var sourceRoots []string
	var walkRoots []string
	for _, src := range cfg.Sources {
		walkRoots = append(walkRoots, src.Path)
		if !strings.HasPrefix(src.Path, personalBase) {
			sourceRoots = append(sourceRoots, src.Path)
		}
	}

	return &Organiser{
		cfg:         cfg,
		dirMap:      buildDirMap(walkRoots),
		sourceRoots: sourceRoots,
	}
}

// BuildPlan classifies each file and resolves its destination.
// Classification errors are reported inline; the file is omitted from the plan.
func (o *Organiser) BuildPlan(ctx context.Context, a *app.Application, files []string) (Plan, error) {
	personalBase := o.cfg.Personal.DestDir
	var plan Plan

	for _, srcPath := range files {
		// Skip files that are already ingested at their current path — they were
		// organised in a previous run. We don't need the hash to match; presence
		// in the documents table at this exact path is enough to know they're filed.
		if alreadyIngested(ctx, a, srcPath) {
			continue
		}

		fmt.Printf("  Classifying %s ...\n", filepath.Base(srcPath))
		cl, err := a.Classifier.Classify(ctx, srcPath)
		if err != nil {
			fmt.Printf("  ! %s — classification failed, skipping: %v\n", filepath.Base(srcPath), err)
			continue
		}

		ext := strings.ToLower(filepath.Ext(srcPath))
		var destDir string
		var isNew bool

		if !technicalTypes[cl.DocType] {
			// Personal doc — route to PersonalDocs using the LLM's dest_dir suggestion.
			// Clean dest_dir to prevent path traversal from LLM output.
			safeDestDir := filepath.Clean(cl.DestDir)
			safeDestDir = strings.TrimPrefix(safeDestDir, "/")
			if strings.HasPrefix(safeDestDir, "..") || safeDestDir == "." {
				safeDestDir = "other"
			}
			destDir = filepath.Join(personalBase, safeDestDir)
		} else {
			// Technical doc — find an existing directory matching the topic.
			topic := cl.Topic
			if topic == "" {
				topic = cl.Institution
			}
			if matched, ok := o.matchTopic(topic); ok {
				destDir = matched
			} else {
				// No existing match — suggest under the first non-personal source root.
				if len(o.sourceRoots) > 0 {
					destDir = filepath.Join(o.sourceRoots[0], normaliseTopic(topic))
				} else {
					destDir = filepath.Join(personalBase, "library", normaliseTopic(topic))
				}
				isNew = true
			}
		}

		// Check if destDir exists (for personal types we didn't set isNew above).
		if !isNew {
			if _, err := os.Stat(destDir); os.IsNotExist(err) {
				isNew = true
			}
		}

		plan.Items = append(plan.Items, FilePlan{
			SrcPath:  srcPath,
			DestPath: filepath.Join(destDir, cl.Filename+ext),
			DestDir:  destDir,
			IsNew:    isNew,
			DocType:  cl.DocType,
			Language: cl.Language,
			cl:       cl,
		})
	}

	return plan, nil
}

// Execute moves each file to its planned destination and ingests it.
// Files that fail to move are logged and skipped; the run continues.
func Execute(ctx context.Context, a *app.Application, plan Plan, force bool) {
	home, _ := os.UserHomeDir()
	for _, item := range plan.Items {
		if err := os.MkdirAll(item.DestDir, 0o750); err != nil { //nolint:gosec
			fmt.Printf("  ! %s — mkdir failed: %v\n", filepath.Base(item.SrcPath), err)
			continue
		}
		if err := moveFile(item.SrcPath, item.DestPath); err != nil {
			fmt.Printf("  ! %s — move failed: %v\n", filepath.Base(item.SrcPath), err)
			continue
		}
		meta := &models.DocMeta{
			DocType:     item.cl.DocType,
			Language:    item.cl.Language,
			Institution: item.cl.Institution,
			DocDate:     item.cl.Date,
		}
		ingested, err := ingestion.IngestFile(ctx, a, item.DestPath, "personal", force, meta)
		if err != nil {
			fmt.Printf("  ! %s — ingest failed: %v\n", filepath.Base(item.DestPath), err)
		}
		displayDest := strings.Replace(item.DestPath, home, "~", 1)
		if !ingested && err == nil {
			fmt.Printf("  ✓ %s → %s  ⚠ not indexed (no text extracted — scanned document?)\n", filepath.Base(item.SrcPath), displayDest)
		} else {
			fmt.Printf("  ✓ %s → %s\n", filepath.Base(item.SrcPath), displayDest)
		}
	}
}

// classifyMissing hashes path and checks whether it is a duplicate of an already-indexed
// file (same content, different path) or genuinely un-indexed content.
// Called only after alreadyIngested returns false for the file's current path.
func classifyMissing(ctx context.Context, a *app.Application, path string) (skipKind, string, error) {
	hash, err := ingestion.HashFile(path)
	if err != nil {
		return skipNoContent, "", err
	}
	dup, err := a.Documents.FindDuplicate(ctx, path, hash)
	if err != nil {
		return skipNoContent, "", err
	}
	if dup != "" {
		return skipDuplicate, dup, nil
	}
	return skipNoContent, "", nil
}

// ReindexUnindexed walks dir recursively, finds every supported file that is
// absent from the documents table, and retries IngestFile without re-classifying
// or moving anything. This recovers files that were organised in a previous run
// but silently skipped (e.g. scanned PDFs with no text layer at the time).
// When dryRun is true it lists what would be indexed without making any changes.
func ReindexUnindexed(ctx context.Context, a *app.Application, dir string, dryRun bool) error {
	home, _ := os.UserHomeDir()
	var found, indexed, noText, dupes, failed int

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if !SupportedExtensions[ext] {
			return nil
		}
		found++
		if alreadyIngested(ctx, a, path) {
			return nil
		}

		display := strings.Replace(path, home, "~", 1)

		kind, dupPath, err := classifyMissing(ctx, a, path)
		if err != nil {
			fmt.Printf("  ! %s — check failed: %v\n", display, err)
			failed++
			return nil
		}
		if kind == skipDuplicate {
			dupDisplay := strings.Replace(dupPath, home, "~", 1)
			fmt.Printf("  ↪ %s — duplicate of %s\n", display, dupDisplay)
			dupes++
			return nil
		}

		if dryRun {
			fmt.Printf("  ~ %s — would be re-indexed\n", display)
			indexed++
			return nil
		}

		ingested, err := ingestion.IngestFile(ctx, a, path, "personal", false, nil)
		if err != nil {
			fmt.Printf("  ! %s — ingest failed: %v\n", display, err)
			failed++
			return nil
		}
		if ingested {
			fmt.Printf("  ✓ %s — indexed\n", display)
			indexed++
		} else {
			fmt.Printf("  ⚠ %s — no text extracted (scanned document?)\n", display)
			noText++
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk %s: %w", dir, err)
	}

	if dryRun {
		fmt.Printf("\n  %d file(s) found — %d would be re-indexed, %d duplicate(s)\n",
			found, indexed, dupes)
	} else {
		fmt.Printf("\n  %d file(s) checked — %d newly indexed, %d no text, %d duplicate(s), %d failed\n",
			found, indexed, noText, dupes, failed)
	}
	return nil
}

// StatusCheck walks dir and reports index coverage: how many supported files
// exist, how many are indexed, and how many are absent from the index — broken
// down by extension and skip reason. Read-only; makes no changes.
func StatusCheck(ctx context.Context, a *app.Application, dir string) error {
	home, _ := os.UserHomeDir()

	type missingEntry struct {
		display string
		kind    skipKind
		dupOf   string
	}
	type extStats struct {
		total   int
		indexed int
		missing []missingEntry
	}
	byExt := map[string]*extStats{}

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if !SupportedExtensions[ext] {
			return nil
		}
		if byExt[ext] == nil {
			byExt[ext] = &extStats{}
		}
		byExt[ext].total++
		if alreadyIngested(ctx, a, path) {
			byExt[ext].indexed++
			return nil
		}
		display := strings.Replace(path, home, "~", 1)
		kind, dupPath, _ := classifyMissing(ctx, a, path)
		dupDisplay := strings.Replace(dupPath, home, "~", 1)
		byExt[ext].missing = append(byExt[ext].missing, missingEntry{display, kind, dupDisplay})
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk %s: %w", dir, err)
	}

	if len(byExt) == 0 {
		fmt.Println("  No supported files found.")
		return nil
	}

	var totalFiles, totalIndexed, totalDupes, totalNoText int
	for _, ext := range []string{".pdf", ".md", ".txt"} {
		s := byExt[ext]
		if s == nil {
			continue
		}
		totalFiles += s.total
		totalIndexed += s.indexed
		missing := len(s.missing)
		if missing == 0 {
			fmt.Printf("  %s  %d total — all indexed\n", ext, s.total)
			continue
		}
		fmt.Printf("  %s  %d total — %d indexed, %d not indexed\n",
			ext, s.total, s.indexed, missing)
		for _, m := range s.missing {
			if m.kind == skipDuplicate {
				fmt.Printf("      ↪ %s  (duplicate of %s)\n", m.display, m.dupOf)
				totalDupes++
			} else {
				fmt.Printf("      ✗ %s\n", m.display)
				totalNoText++
			}
		}
	}

	totalMissing := totalDupes + totalNoText
	fmt.Printf("\n  Total: %d file(s) — %d indexed (%.0f%%), %d not indexed\n",
		totalFiles, totalIndexed,
		float64(totalIndexed)/float64(totalFiles)*100,
		totalMissing)

	if totalDupes > 0 {
		fmt.Printf("\n  %d duplicate(s) — content already searchable under the original filename.\n", totalDupes)
	}
	if totalNoText > 0 {
		fmt.Printf("\n  %d file(s) with no text extracted (scanned documents).\n", totalNoText)
		fmt.Println("  Run `nexus organise --reindex` to retry once a text layer is available.")
	}
	return nil
}

// consolidatePlan describes one DB re-point: the file lives at CurrentPath on
// disk but the documents table still records its pre-move location as OldPath.
type consolidatePlan struct {
	currentPath string
	oldPath     string
}

// Consolidate walks dir, identifies files whose content is already in the DB
// under a different path (duplicates produced by nexus organise renaming files),
// and — when the original path no longer exists on disk — re-points the DB
// record to the current location without re-ingesting or re-embedding anything.
//
// Files where both the current path and the original DB path exist on disk are
// genuine duplicates (two copies); those are reported but left untouched.
//
// When dryRun is true the plan is printed but nothing is written to the DB.
func Consolidate(ctx context.Context, a *app.Application, dir string, dryRun bool) error {
	home, _ := os.UserHomeDir()

	var plan []consolidatePlan
	var genuineDupes []string

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if !SupportedExtensions[ext] {
			return nil
		}
		if alreadyIngested(ctx, a, path) {
			return nil
		}
		kind, oldPath, err := classifyMissing(ctx, a, path)
		if err != nil || kind != skipDuplicate {
			return nil
		}
		if _, statErr := os.Stat(oldPath); statErr == nil {
			// Original file still exists — genuine duplicate, leave it alone.
			genuineDupes = append(genuineDupes, strings.Replace(path, home, "~", 1))
			return nil
		}
		plan = append(plan, consolidatePlan{currentPath: path, oldPath: oldPath})
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk %s: %w", dir, err)
	}

	if len(plan) == 0 {
		fmt.Println("  Nothing to consolidate.")
		if len(genuineDupes) > 0 {
			fmt.Printf("  %d genuine duplicate(s) skipped (both copies exist on disk):\n", len(genuineDupes))
			for _, d := range genuineDupes {
				fmt.Printf("      %s\n", d)
			}
		}
		return nil
	}

	fmt.Printf("  Consolidation plan (%d record(s)):\n\n", len(plan))
	for _, p := range plan {
		oldDisplay := strings.Replace(p.oldPath, home, "~", 1)
		newDisplay := strings.Replace(p.currentPath, home, "~", 1)
		fmt.Printf("    %s\n      → %s\n\n", oldDisplay, newDisplay)
	}

	if dryRun {
		fmt.Println("  [dry-run] No changes made.")
		return nil
	}

	fmt.Print("  Apply? [Y/n] ")
	reader := bufio.NewReader(os.Stdin)
	answer, _ := reader.ReadString('\n')
	answer = strings.TrimSpace(strings.ToLower(answer))
	if answer != "" && answer != "y" && answer != "yes" {
		fmt.Println("  Cancelled.")
		return nil
	}

	fmt.Println()
	var succeeded, failed int
	for _, p := range plan {
		if err := a.Documents.RePoint(ctx, p.oldPath, p.currentPath); err != nil {
			display := strings.Replace(p.currentPath, home, "~", 1)
			fmt.Printf("  ! %s — failed: %v\n", display, err)
			failed++
		} else {
			newDisplay := strings.Replace(p.currentPath, home, "~", 1)
			fmt.Printf("  ✓ %s\n", newDisplay)
			succeeded++
		}
	}

	fmt.Printf("\n  %d record(s) re-pointed, %d failed\n", succeeded, failed)
	if len(genuineDupes) > 0 {
		fmt.Printf("  %d genuine duplicate(s) left untouched (both copies exist on disk)\n", len(genuineDupes))
	}
	return nil
}

// cleanupPair is a genuine duplicate: both the organised path and the original
// path exist on disk with identical content.
type cleanupPair struct {
	keepPath   string // organised/canonical path — stays on disk, DB re-pointed here
	deletePath string // original path — will be deleted after confirmation
}

// Cleanup finds files in dir whose content is already indexed under a different
// path AND whose original path still exists on disk (genuine duplicates). It
// shows a plan of what will be kept and what will be deleted, asks for
// confirmation, deletes the originals, and re-points the DB records so the
// canonical organised path becomes the authoritative location. The original
// filename is preserved in the documents.original_path column.
func Cleanup(ctx context.Context, a *app.Application, dir string) error {
	home, _ := os.UserHomeDir()

	var pairs []cleanupPair

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if !SupportedExtensions[ext] {
			return nil
		}
		if alreadyIngested(ctx, a, path) {
			return nil
		}
		kind, oldPath, err := classifyMissing(ctx, a, path)
		if err != nil || kind != skipDuplicate {
			return nil
		}
		// Only handle pairs where both copies are still on disk.
		if _, statErr := os.Stat(oldPath); statErr != nil {
			return nil
		}
		pairs = append(pairs, cleanupPair{keepPath: path, deletePath: oldPath})
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk %s: %w", dir, err)
	}

	if len(pairs) == 0 {
		fmt.Println("  No duplicate originals found.")
		return nil
	}

	fmt.Printf("  Cleanup plan (%d duplicate(s)):\n\n", len(pairs))
	for _, p := range pairs {
		keepDisplay := strings.Replace(p.keepPath, home, "~", 1)
		delDisplay := strings.Replace(p.deletePath, home, "~", 1)
		fmt.Printf("    keep    %s\n", keepDisplay)
		fmt.Printf("    delete  %s\n\n", delDisplay)
	}
	fmt.Println("  Original filenames will be saved in the database before deletion.")
	fmt.Println("  Deleted files cannot be recovered — make sure you have reviewed the plan.")
	fmt.Println()

	fmt.Print("  Apply? [Y/n] ")
	reader := bufio.NewReader(os.Stdin)
	answer, _ := reader.ReadString('\n')
	answer = strings.TrimSpace(strings.ToLower(answer))
	if answer != "" && answer != "y" && answer != "yes" {
		fmt.Println("  Cancelled.")
		return nil
	}

	fmt.Println()
	var succeeded, failed int
	for _, p := range pairs {
		// Re-point first — if this fails, don't delete.
		if err := a.Documents.RePoint(ctx, p.deletePath, p.keepPath); err != nil {
			display := strings.Replace(p.keepPath, home, "~", 1)
			fmt.Printf("  ! %s — DB update failed, skipping delete: %v\n", display, err)
			failed++
			continue
		}
		if err := os.Remove(p.deletePath); err != nil {
			display := strings.Replace(p.deletePath, home, "~", 1)
			fmt.Printf("  ! could not delete %s: %v\n", display, err)
			failed++
			continue
		}
		keepDisplay := strings.Replace(p.keepPath, home, "~", 1)
		fmt.Printf("  ✓ %s\n", keepDisplay)
		succeeded++
	}

	fmt.Printf("\n  %d duplicate(s) cleaned up, %d failed\n", succeeded, failed)
	return nil
}

// alreadyIngested reports whether srcPath is already present in the documents
// table. If it is, the file was organised and ingested in a previous run — no
// need to re-classify it again.
func alreadyIngested(ctx context.Context, a *app.Application, srcPath string) bool {
	var exists bool
	_ = a.DB.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM documents WHERE file_path = $1)`, srcPath,
	).Scan(&exists)
	return exists
}

// matchTopic looks up an existing directory whose name matches the topic
// (case-insensitive). Returns the first match and true, or empty and false.
func (o *Organiser) matchTopic(topic string) (string, bool) {
	if topic == "" {
		return "", false
	}
	key := strings.ToLower(strings.TrimSpace(topic))
	if paths, ok := o.dirMap[key]; ok && len(paths) > 0 {
		return paths[0], true
	}
	return "", false
}

// buildDirMap walks each root and maps lowercase directory names to their paths.
func buildDirMap(roots []string) map[string][]string {
	dirMap := make(map[string][]string)
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || !d.IsDir() {
				return nil
			}
			key := strings.ToLower(d.Name())
			dirMap[key] = append(dirMap[key], path)
			return nil
		})
	}
	return dirMap
}

// normaliseTopic converts a topic string into a safe directory name.
func normaliseTopic(topic string) string {
	s := strings.TrimSpace(topic)
	if s == "" {
		return "unsorted"
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// moveFile moves src to dst, falling back to copy+delete for cross-device moves.
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	// Cross-device move: copy then delete.
	in, err := os.Open(src) //nolint:gosec
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer func() { _ = in.Close() }()

	out, err := os.Create(dst) //nolint:gosec
	if err != nil {
		return fmt.Errorf("create dest: %w", err)
	}
	defer func() { _ = out.Close() }()

	if _, err := io.Copy(out, in); err != nil {
		_ = os.Remove(dst)
		return fmt.Errorf("copy: %w", err)
	}
	_ = in.Close()
	_ = out.Close()
	return os.Remove(src)
}
