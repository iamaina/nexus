package models

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Repo represents a git repository registered in the nexus database.
type Repo struct {
	ID        int64
	Path      string // absolute local path
	RemoteURL string // normalised: host/org/repo (no protocol, no .git)
	Platform  string // github | gitlab | other
	RepoType  string // work | personal
	RootName  string // which RepoRoot this belongs to
	LastSeen  string // timestamp of last scan
}

// RepoModel handles all database operations for the repos table.
type RepoModel struct {
	DB *pgx.Conn
}

// Upsert inserts or updates a repo record keyed on path.
func (m *RepoModel) Upsert(ctx context.Context, r Repo) error {
	_, err := m.DB.Exec(ctx, `
		INSERT INTO repos (path, remote_url, platform, repo_type, root_name, last_seen)
		VALUES ($1, $2, $3, $4, $5, NOW())
		ON CONFLICT (path) DO UPDATE SET
			remote_url = EXCLUDED.remote_url,
			platform   = EXCLUDED.platform,
			repo_type  = EXCLUDED.repo_type,
			root_name  = EXCLUDED.root_name,
			last_seen  = NOW()
	`, r.Path, r.RemoteURL, r.Platform, r.RepoType, r.RootName)
	if err != nil {
		return fmt.Errorf("upsert repo %s: %w", r.Path, err)
	}
	return nil
}

// FindByRemote looks up a repo by its normalised remote URL.
// Returns nil, nil if not found.
func (m *RepoModel) FindByRemote(ctx context.Context, remoteURL string) (*Repo, error) {
	var r Repo
	err := m.DB.QueryRow(ctx, `
		SELECT id, path, remote_url, platform, repo_type, root_name,
		       to_char(last_seen, 'YYYY-MM-DD HH24:MI') AS last_seen
		FROM repos WHERE remote_url = $1
	`, remoteURL).Scan(&r.ID, &r.Path, &r.RemoteURL, &r.Platform, &r.RepoType, &r.RootName, &r.LastSeen)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find repo by remote: %w", err)
	}
	return &r, nil
}

// FindByRoot returns all repos registered under rootName, ordered by path.
func (m *RepoModel) FindByRoot(ctx context.Context, rootName string) ([]Repo, error) {
	rows, err := m.DB.Query(ctx, `
		SELECT id, path, remote_url, platform, repo_type, root_name,
		       to_char(last_seen, 'YYYY-MM-DD HH24:MI') AS last_seen
		FROM repos WHERE root_name = $1 ORDER BY path
	`, rootName)
	if err != nil {
		return nil, fmt.Errorf("find repos by root: %w", err)
	}
	defer rows.Close()
	return scanRepos(rows)
}

// List returns all known repos ordered by root then path.
func (m *RepoModel) List(ctx context.Context) ([]Repo, error) {
	rows, err := m.DB.Query(ctx, `
		SELECT id, path, remote_url, platform, repo_type, root_name,
		       to_char(last_seen, 'YYYY-MM-DD HH24:MI') AS last_seen
		FROM repos ORDER BY root_name, path
	`)
	if err != nil {
		return nil, fmt.Errorf("list repos: %w", err)
	}
	defer rows.Close()
	return scanRepos(rows)
}

func scanRepos(rows pgx.Rows) ([]Repo, error) {
	var repos []Repo
	for rows.Next() {
		var r Repo
		if err := rows.Scan(&r.ID, &r.Path, &r.RemoteURL, &r.Platform,
			&r.RepoType, &r.RootName, &r.LastSeen); err != nil {
			return nil, fmt.Errorf("scan repo row: %w", err)
		}
		repos = append(repos, r)
	}
	return repos, rows.Err()
}
