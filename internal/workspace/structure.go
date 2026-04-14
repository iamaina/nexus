// Package workspace generates and maintains a structural snapshot of the
// workspace directory tree for ingestion into nexus.
package workspace

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// maxDepth is how many levels below the workspace root to descend.
// Git repos stop recursion at their own level regardless of depth.
const maxDepth = 5

// repoEntry holds metadata for a discovered git repository.
type repoEntry struct {
	name     string
	fullPath string
	remote   string
	branch   string
	status   string
}

// Generate walks workspaceRoot up to maxDepth levels deep and returns a
// Markdown document with two sections:
//   - Directory Tree: indented view, each git repo shows its full path inline
//   - Repository Index: one self-contained line per repo so semantic search
//     can find any repo by name or path without relying on indentation context
func Generate(workspaceRoot string) (string, error) {
	home, _ := os.UserHomeDir()
	var buf bytes.Buffer
	var repos []repoEntry

	displayRoot := strings.Replace(workspaceRoot, home, "~", 1)
	fmt.Fprintf(&buf, "# Workspace Structure\n\nRoot: `%s`\n\n## Directory Tree\n\n", displayRoot)
	walkDir(&buf, workspaceRoot, home, 0, &repos)

	// Repository Index — one self-contained line per repo.
	// Each line includes name + full path + remote + branch + status so a
	// single chunk can answer "where is X cloned" without surrounding context.
	if len(repos) > 0 {
		fmt.Fprintf(&buf, "\n## Repository Index\n\n")
		for _, r := range repos {
			displayPath := strings.Replace(r.fullPath, home, "~", 1)
			remote := r.remote
			if remote == "" {
				remote = "no-remote"
			}
			fmt.Fprintf(&buf, "- repo:%s  path:%s  remote:%s  branch:%s  status:%s\n",
				r.name, displayPath, remote, r.branch, r.status)
		}
	}

	return buf.String(), nil
}

// WriteTo writes the snapshot to <workspaceRoot>/dir_structure.md and returns
// the absolute file path. Callers should ingest the file after calling this.
func WriteTo(workspaceRoot string) (string, error) {
	content, err := Generate(workspaceRoot)
	if err != nil {
		return "", err
	}
	outPath := filepath.Join(workspaceRoot, "dir_structure.md")
	if err := os.WriteFile(outPath, []byte(content), 0o644); err != nil { //nolint:gosec
		return "", fmt.Errorf("write dir_structure.md: %w", err)
	}
	return outPath, nil
}

func walkDir(buf *bytes.Buffer, dir, home string, depth int, repos *[]repoEntry) {
	if depth > maxDepth {
		return
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return // skip unreadable directories silently
	}

	indent := strings.Repeat("  ", depth)

	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		fullPath := filepath.Join(dir, entry.Name())

		if isGitRepo(fullPath) {
			remote, branch, dirty := repoDetails(fullPath)
			status := "clean"
			if dirty {
				status = "dirty"
			}
			displayPath := strings.Replace(fullPath, home, "~", 1)

			// Include the full path inline so this tree line is self-contained.
			if remote != "" {
				fmt.Fprintf(buf, "%s- **%s** (`%s`) — %s | %s | %s\n",
					indent, entry.Name(), displayPath, remote, branch, status)
			} else {
				fmt.Fprintf(buf, "%s- **%s** (`%s`) — %s | %s\n",
					indent, entry.Name(), displayPath, branch, status)
			}

			*repos = append(*repos, repoEntry{
				name:     entry.Name(),
				fullPath: fullPath,
				remote:   remote,
				branch:   branch,
				status:   status,
			})
			continue
		}

		fmt.Fprintf(buf, "%s- %s/\n", indent, entry.Name())
		walkDir(buf, fullPath, home, depth+1, repos)
	}
}

func isGitRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// repoDetails returns the normalised remote URL, current branch, and dirty flag.
func repoDetails(repoPath string) (remote, branch string, dirty bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	remote = normaliseRemote(gitRun(ctx, repoPath, "remote", "get-url", "origin"))
	branch = gitRun(ctx, repoPath, "rev-parse", "--abbrev-ref", "HEAD")
	dirty = gitRun(ctx, repoPath, "status", "--porcelain") != ""
	return
}

func normaliseRemote(raw string) string {
	s := strings.TrimSuffix(raw, ".git")
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	if rest, ok := strings.CutPrefix(s, "git@"); ok {
		return strings.Replace(rest, ":", "/", 1)
	}
	return s
}

func gitRun(ctx context.Context, dir string, args ...string) string {
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // args are controlled literals
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// test
