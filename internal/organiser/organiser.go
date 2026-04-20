// Package organiser classifies files, plans their destination, and executes moves.
// It powers the nexus organise command (Phase 3 of Workspace OS).
package organiser

import (
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
		if _, err := ingestion.IngestFile(ctx, a, item.DestPath, "personal", force, meta); err != nil {
			fmt.Printf("  ! %s — ingest failed: %v\n", filepath.Base(item.DestPath), err)
		}
		displayDest := strings.Replace(item.DestPath, home, "~", 1)
		fmt.Printf("  ✓ %s → %s\n", filepath.Base(item.SrcPath), displayDest)
	}
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
