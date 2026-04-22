package workspace

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// RepoEntry holds metadata for one git repository parsed from dir_structure.md.
type RepoEntry struct {
	Name   string
	Path   string // absolute local path (~ expanded)
	Remote string // normalised remote URL — may be empty
	Branch string
	Status string // "clean" or "dirty"
}

// RepoGroup is a local directory that directly contains one or more git repos.
// Each group is a candidate nexus source.
type RepoGroup struct {
	DirPath string      // absolute path to the parent directory
	Repos   []RepoEntry // repos whose immediate parent is DirPath
}

// repoLineRe matches lines emitted by workspace.Generate:
//
//   - **name** (`~/path`) — remote | branch | status   (repo with remote)
//   - **name** (`~/path`) — branch | status            (repo without remote)
//
// The backtick cannot appear inside a Go raw string literal, so the pattern is a
// regular interpreted string with \x60 (backtick) instead.
var repoLineRe = regexp.MustCompile("^\\s*-\\s+\\*\\*(.+?)\\*\\*\\s+\\(\\x60(.+?)\\x60\\)\\s+—\\s+(.+)$") //nolint:staticcheck // raw string impossible: pattern contains a literal backtick

// ParseRepos reads dir_structure.md at structurePath and returns all discovered
// repository entries. ~ in paths is expanded using os.UserHomeDir.
func ParseRepos(structurePath string) ([]RepoEntry, error) {
	f, err := os.Open(structurePath) //nolint:gosec
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	home, _ := os.UserHomeDir()
	var repos []RepoEntry

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		m := repoLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		name := m[1]
		rawPath := m[2]
		rest := m[3]

		// Expand ~ in embedded path
		fullPath := rawPath
		if strings.HasPrefix(rawPath, "~/") {
			fullPath = filepath.Join(home, rawPath[2:])
		}

		// rest is "remote | branch | status" (3 parts) or "branch | status" (2 parts)
		parts := strings.SplitN(rest, " | ", 3)
		var remote, branch, status string
		switch len(parts) {
		case 3:
			remote = strings.TrimSpace(parts[0])
			branch = strings.TrimSpace(parts[1])
			status = strings.TrimSpace(parts[2])
		case 2:
			branch = strings.TrimSpace(parts[0])
			status = strings.TrimSpace(parts[1])
		default:
			branch = strings.TrimSpace(rest)
		}

		repos = append(repos, RepoEntry{
			Name:   name,
			Path:   fullPath,
			Remote: remote,
			Branch: branch,
			Status: status,
		})
	}
	return repos, scanner.Err()
}

// GroupByDirectory groups repos by their immediate parent directory, preserving
// discovery order so the output matches the dir_structure.md top-to-bottom order.
func GroupByDirectory(repos []RepoEntry) []RepoGroup {
	seen := map[string]*RepoGroup{}
	var order []string

	for i := range repos {
		dir := filepath.Dir(repos[i].Path)
		if _, ok := seen[dir]; !ok {
			seen[dir] = &RepoGroup{DirPath: dir}
			order = append(order, dir)
		}
		seen[dir].Repos = append(seen[dir].Repos, repos[i])
	}

	groups := make([]RepoGroup, 0, len(order))
	for _, dir := range order {
		groups = append(groups, *seen[dir])
	}
	return groups
}
