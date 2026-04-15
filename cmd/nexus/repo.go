package nexus

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/iamaina/nexus/internal/app"
	"github.com/iamaina/nexus/internal/config"
	"github.com/iamaina/nexus/internal/logger"
	"github.com/iamaina/nexus/internal/models"
	"github.com/spf13/cobra"
)

const repoScanMaxDepth = 6

var repoCmd = &cobra.Command{
	Use:   "repo",
	Short: "Manage and locate git repositories",
}

// ── scan ─────────────────────────────────────────────────────────────────────

var repoScanCmd = &cobra.Command{
	Use:   "scan",
	Short: "Scan all repo roots and register repositories in the database",
	Long: `Walk every configured repo root, discover git repositories, and upsert
them into the nexus database. Run once after setup, then nexus watch keeps
the database current as new repos are cloned.`,
	Run: func(cmd *cobra.Command, _ []string) {
		ctx := cmd.Context()
		a, ok := ctx.Value(app.AppKey).(*app.Application)
		if !ok {
			logger.Error(ctx, "Application not found in context")
			return
		}

		if len(a.Config.Roots.Repos) == 0 {
			fmt.Println("  No repo roots configured. Add roots.repos to config.yaml.")
			return
		}

		total := 0
		for _, root := range a.Config.Roots.Repos {
			found := scanRepoRoot(ctx, a, root)
			total += found
		}
		fmt.Printf("\n  ✓ %d repositories registered.\n\n", total)
	},
}

func scanRepoRoot(ctx context.Context, a *app.Application, root config.RepoRoot) int {
	fmt.Printf("  Scanning %s (%s)...\n", root.Name, root.Path)
	repos := walkForRepos(root.Path, repoScanMaxDepth)
	repoType := repoTypeFromName(root.Name)

	for _, r := range repos {
		remote := repoNormaliseRemote(repoGitRun(ctx, r, "remote", "get-url", "origin"))
		platform := detectPlatform(remote)
		if err := a.Repos.Upsert(ctx, models.Repo{
			Path:      r,
			RemoteURL: remote,
			Platform:  platform,
			RepoType:  repoType,
			RootName:  root.Name,
		}); err != nil {
			logger.Warn(ctx, fmt.Sprintf("repo.scan: upsert failed for %s: %v", r, err))
			continue
		}
		home, _ := os.UserHomeDir()
		fmt.Printf("    + %s\n", strings.Replace(r, home, "~", 1))
	}
	return len(repos)
}

// ── list ─────────────────────────────────────────────────────────────────────

var repoListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all registered repositories",
	Run: func(cmd *cobra.Command, _ []string) {
		ctx := cmd.Context()
		a, ok := ctx.Value(app.AppKey).(*app.Application)
		if !ok {
			logger.Error(ctx, "Application not found in context")
			return
		}

		repos, err := a.Repos.List(ctx)
		if err != nil {
			logger.Error(ctx, fmt.Sprintf("list repos: %v", err))
			return
		}
		if len(repos) == 0 {
			fmt.Println("  No repositories registered. Run: nexus repo scan")
			return
		}

		home, _ := os.UserHomeDir()
		display := func(p string) string { return strings.Replace(p, home, "~", 1) }

		fmt.Println()
		currentRoot := ""
		for _, r := range repos {
			if r.RootName != currentRoot {
				if currentRoot != "" {
					fmt.Println()
				}
				// Find the root path for context
				rootPath := ""
				for _, root := range a.Config.Roots.Repos {
					if root.Name == r.RootName {
						rootPath = display(root.Path)
						break
					}
				}
				if rootPath != "" {
					fmt.Printf("  %s  (%s)\n", r.RootName, rootPath)
				} else {
					fmt.Printf("  %s\n", r.RootName)
				}
				fmt.Printf("  %s\n", strings.Repeat("─", 60))
				currentRoot = r.RootName
			}

			branch := repoGitRun(context.Background(), r.Path, "rev-parse", "--abbrev-ref", "HEAD")
			dirty := repoGitRun(context.Background(), r.Path, "status", "--porcelain") != ""
			status := "clean"
			if dirty {
				status = "dirty"
			}
			name := filepath.Base(r.Path)
			rel := display(r.Path)
			if branch != "" {
				fmt.Printf("    %-30s  %s  %s  %s\n", name, rel, branch, status)
			} else {
				fmt.Printf("    %-30s  %s\n", name, rel)
			}
		}
		fmt.Println()
	},
}

// ── check ─────────────────────────────────────────────────────────────────────

var repoCheckCmd = &cobra.Command{
	Use:   "check <url>",
	Short: "Find an existing clone or suggest where to put a new one",
	Long: `Given a git URL, nexus looks for an existing local clone. If found,
it shows the path and current status. If not found, it infers a placement
from how your existing repositories are organised and offers to clone it.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()
		a, ok := ctx.Value(app.AppKey).(*app.Application)
		if !ok {
			logger.Error(ctx, "Application not found in context")
			return
		}

		rawURL := args[0]
		normalised := repoNormaliseRemote(rawURL)
		if normalised == "" {
			logger.Error(ctx, fmt.Sprintf("Cannot parse URL: %s", rawURL))
			return
		}

		home, _ := os.UserHomeDir()
		display := func(p string) string { return strings.Replace(p, home, "~", 1) }

		// 1. Look up in DB.
		repo, err := a.Repos.FindByRemote(ctx, normalised)
		if err != nil {
			logger.Error(ctx, fmt.Sprintf("DB lookup failed: %v", err))
			return
		}

		if repo != nil {
			// Found — show live status.
			branch := repoGitRun(ctx, repo.Path, "rev-parse", "--abbrev-ref", "HEAD")
			dirty := repoGitRun(ctx, repo.Path, "status", "--porcelain") != ""
			lastCommit := repoGitRun(ctx, repo.Path, "log", "-1", "--format=%s (%cr)")

			statusIcon := "✅"
			statusLabel := "clean"
			if dirty {
				statusIcon = "⚠️ "
				statusLabel = "uncommitted changes"
			}

			fmt.Printf("\n  %s  %s\n", statusIcon, display(repo.Path))
			if branch != "" {
				fmt.Printf("      Branch: %s  |  %s\n", branch, statusLabel)
			}
			if lastCommit != "" {
				fmt.Printf("      Last commit: %s\n", lastCommit)
			}
			fmt.Println()
			return
		}

		// 2. DB miss — search the workspace root as a fallback before giving up.
		if ws := a.Config.Roots.Workspace; ws != "" {
			if found := findRepoByRemote(ctx, ws, normalised); found != "" {
				// Register it so future lookups are instant.
				root := a.Config.FindRepoRoot(normalised)
				rootName := ""
				if root != nil {
					rootName = root.Name
				}
				_ = a.Repos.Upsert(ctx, models.Repo{
					Path:      found,
					RemoteURL: normalised,
					Platform:  detectPlatform(normalised),
					RepoType:  repoTypeFromName(rootName),
					RootName:  rootName,
				})

				branch := repoGitRun(ctx, found, "rev-parse", "--abbrev-ref", "HEAD")
				dirty := repoGitRun(ctx, found, "status", "--porcelain") != ""
				lastCommit := repoGitRun(ctx, found, "log", "-1", "--format=%s (%cr)")
				statusIcon, statusLabel := "✅", "clean"
				if dirty {
					statusIcon, statusLabel = "⚠️ ", "uncommitted changes"
				}
				fmt.Printf("\n  %s  %s  [found via workspace scan — registered]\n", statusIcon, display(found))
				if branch != "" {
					fmt.Printf("      Branch: %s  |  %s\n", branch, statusLabel)
				}
				if lastCommit != "" {
					fmt.Printf("      Last commit: %s\n", lastCommit)
				}
				fmt.Println()
				return
			}
		}

		// 3. Not found anywhere — suggest placement.
		fmt.Printf("\n  ❌  %s not found in any registered root.\n", normalised)

		root := a.Config.FindRepoRoot(normalised)
		if root == nil {
			fmt.Println("\n  No matching repo root found in config.")
			fmt.Println("  Add a roots.repos entry with the appropriate host substring.")
			fmt.Println()
			return
		}

		// Load existing repos from this root to infer subdir.
		existing, err := a.Repos.FindByRoot(ctx, root.Name)
		if err != nil {
			logger.Warn(ctx, fmt.Sprintf("could not load existing repos: %v", err))
		}
		subdir := inferSubdir(normalised, root.Path, existing)
		suggestedPath := filepath.Join(root.Path, subdir)

		fmt.Printf("\n  Suggested location (%s root):\n", root.Name)
		fmt.Printf("    %s", display(suggestedPath))
		if subdir != filepath.Base(suggestedPath) {
			fmt.Printf("  [inferred from %s/* pattern]", repoOrg(normalised))
		} else {
			fmt.Printf("  [fallback — no pattern found]")
		}
		fmt.Println()

		// If the suggested path already exists as a git repo, register rather than clone.
		if repoIsGitRepo(suggestedPath) {
			existingRemote := repoNormaliseRemote(repoGitRun(ctx, suggestedPath, "remote", "get-url", "origin"))
			if existingRemote == normalised {
				fmt.Printf("\n  Already cloned — registering in nexus.\n")
				_ = a.Repos.Upsert(ctx, models.Repo{
					Path:      suggestedPath,
					RemoteURL: normalised,
					Platform:  detectPlatform(normalised),
					RepoType:  repoTypeFromName(root.Name),
					RootName:  root.Name,
				})
				branch := repoGitRun(ctx, suggestedPath, "rev-parse", "--abbrev-ref", "HEAD")
				dirty := repoGitRun(ctx, suggestedPath, "status", "--porcelain") != ""
				statusLabel := "clean"
				if dirty {
					statusLabel = "uncommitted changes"
				}
				fmt.Printf("  ✅  %s\n", display(suggestedPath))
				if branch != "" {
					fmt.Printf("      Branch: %s  |  %s\n", branch, statusLabel)
				}
				fmt.Println()
				return
			}
			fmt.Printf("\n  ⚠️   A different repository already exists at that path:\n")
			fmt.Printf("      remote: %s\n", existingRemote)
			fmt.Printf("  Did you mean: nexus repo check %s ?\n\n", matchProtocol(rawURL, existingRemote))
			return
		}

		fmt.Printf("\n  Clone here? [Y/n] ")
		reader := bufio.NewReader(os.Stdin)
		answer, _ := reader.ReadString('\n')
		answer = strings.TrimSpace(strings.ToLower(answer))
		if answer != "" && answer != "y" && answer != "yes" {
			fmt.Println("  Cancelled.")
			return
		}

		// Clone.
		cloneURL := toCloneURL(rawURL)
		fmt.Printf("\n  Cloning %s ...\n", cloneURL)
		cloneCmd := exec.CommandContext(ctx, "git", "clone", cloneURL, suggestedPath) //nolint:gosec
		cloneCmd.Stdout = os.Stdout
		cloneCmd.Stderr = os.Stderr
		if err := cloneCmd.Run(); err != nil {
			logger.Error(ctx, fmt.Sprintf("git clone failed: %v", err))
			return
		}

		// Register the newly cloned repo.
		remote := repoNormaliseRemote(repoGitRun(ctx, suggestedPath, "remote", "get-url", "origin"))
		_ = a.Repos.Upsert(ctx, models.Repo{
			Path:      suggestedPath,
			RemoteURL: remote,
			Platform:  detectPlatform(remote),
			RepoType:  repoTypeFromName(root.Name),
			RootName:  root.Name,
		})

		fmt.Printf("\n  ✓ Cloned to %s\n\n", display(suggestedPath))
	},
}

// ── helpers ──────────────────────────────────────────────────────────────────

// walkForRepos recursively discovers git repositories up to maxDepth levels deep.
func walkForRepos(dir string, maxDepth int) []string {
	var repos []string
	repoWalk(dir, 0, maxDepth, &repos)
	return repos
}

func repoWalk(dir string, depth, maxDepth int, repos *[]string) {
	if depth > maxDepth {
		return
	}
	if repoIsGitRepo(dir) {
		*repos = append(*repos, dir)
		return // don't recurse into git repos
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		repoWalk(filepath.Join(dir, e.Name()), depth+1, maxDepth, repos)
	}
}

func repoIsGitRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// repoGitRun runs a git command in dir with a 3s timeout and returns trimmed output.
func repoGitRun(ctx context.Context, dir string, args ...string) string {
	tctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(tctx, "git", args...) //nolint:gosec
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// repoNormaliseRemote converts any git remote URL to host/org/repo form.
func repoNormaliseRemote(raw string) string {
	s := strings.TrimSuffix(strings.TrimSpace(raw), ".git")
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	if rest, ok := strings.CutPrefix(s, "git@"); ok {
		return strings.Replace(rest, ":", "/", 1)
	}
	return s
}

// toCloneURL returns a usable clone URL — preserves SSH if input was SSH,
// otherwise passes through as-is.
func toCloneURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "git@") || strings.HasPrefix(raw, "https://") {
		return raw
	}
	// Bare host/org/repo form — reconstruct HTTPS.
	return "https://" + raw
}

// toSSHURL converts a normalised "host/org/repo" URL to SSH form.
// "gitlab.com/gitlab-com/gl-infra/delivery" → "git@gitlab.com:gitlab-com/gl-infra/delivery.git"
func toSSHURL(normalised string) string {
	idx := strings.Index(normalised, "/")
	if idx < 0 {
		return normalised
	}
	return "git@" + normalised[:idx] + ":" + normalised[idx+1:] + ".git"
}

// matchProtocol returns a usable URL for normalised that matches the
// protocol of the original raw input (SSH or HTTPS).
func matchProtocol(rawInput, normalised string) string {
	if strings.HasPrefix(strings.TrimSpace(rawInput), "git@") {
		return toSSHURL(normalised)
	}
	return "https://" + normalised
}

// detectPlatform returns "github", "gitlab", or "other" from a normalised URL.
func detectPlatform(remoteURL string) string {
	lower := strings.ToLower(remoteURL)
	switch {
	case strings.Contains(lower, "github"):
		return "github"
	case strings.Contains(lower, "gitlab"):
		return "gitlab"
	default:
		return "other"
	}
}

// repoTypeFromName maps a root name to a repo type ("work" or "personal").
func repoTypeFromName(name string) string {
	if strings.HasPrefix(name, "personal") {
		return "personal"
	}
	return "work"
}

// repoOrg extracts the org/group component from a normalised remote URL.
// "gitlab.com/gl-infra/delivery" → "gl-infra"
func repoOrg(normalisedURL string) string {
	parts := strings.SplitN(normalisedURL, "/", 3)
	if len(parts) >= 2 {
		return parts[1]
	}
	return ""
}

// inferSubdir determines where under rootPath a new repo should go,
// based on how existing repos in the same org are placed.
//
// It uses substring matching on the org name to handle nested GitLab namespaces:
// e.g. "gl-infra/delivery" matches existing repos with "gitlab-com/gl-infra/*"
// in their URL and finds they all live under "infrastructure/", returning
// "infrastructure/delivery".
//
// Only the top-level subdir is used as the pattern — we want
// "infrastructure/delivery", not "infrastructure/k8s-workloads/delivery".
func inferSubdir(normalisedURL, rootPath string, existing []models.Repo) string {
	parts := strings.SplitN(normalisedURL, "/", 3)
	if len(parts) < 3 {
		return filepath.Base(normalisedURL)
	}
	org := parts[1]                     // e.g. "gl-infra"
	repoName := filepath.Base(parts[2]) // e.g. "delivery"

	// Substring match — handles both "gl-infra/repo" and "gitlab-com/gl-infra/repo".
	orgSub := "/" + org
	parentCounts := make(map[string]int)
	for _, r := range existing {
		if !strings.Contains(r.RemoteURL, orgSub) {
			continue
		}
		rel, err := filepath.Rel(rootPath, r.Path)
		if err != nil || rel == "." {
			continue
		}
		// Take only the top-level subdir under the root.
		topLevel := strings.SplitN(rel, string(filepath.Separator), 2)[0]
		parentCounts[topLevel]++
	}

	bestParent, bestCount := "", 0
	for parent, count := range parentCounts {
		if count > bestCount {
			bestParent, bestCount = parent, count
		}
	}

	if bestParent != "" {
		return filepath.Join(bestParent, repoName)
	}
	return repoName
}

// findRepoByRemote walks the workspace root and returns the local path of the
// first git repo whose remote URL matches normalised. Empty string if not found.
func findRepoByRemote(ctx context.Context, workspaceRoot, normalised string) string {
	repos := walkForRepos(workspaceRoot, repoScanMaxDepth)
	for _, r := range repos {
		remote := repoNormaliseRemote(repoGitRun(ctx, r, "remote", "get-url", "origin"))
		if remote == normalised {
			return r
		}
	}
	return ""
}

func init() {
	repoCmd.AddCommand(repoScanCmd)
	repoCmd.AddCommand(repoListCmd)
	repoCmd.AddCommand(repoCheckCmd)
	RootCmd.AddCommand(repoCmd)
}
