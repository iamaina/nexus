// Package config handles loading and validation of nexus configuration from YAML.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Source represents a configured source of documents to ingest.
type Source struct {
	Name            string   `yaml:"name"`
	Path            string   `yaml:"path"`
	Extensions      []string `yaml:"extensions"`
	Exclude         []string `yaml:"exclude"`           // path substrings to skip (directories or files)
	Watch           bool     `yaml:"watch"`             // if true, nexus watch re-ingests files on change
	SearchByDefault *bool    `yaml:"search_by_default"` // nil/true = included in all queries; false = opt-in only
	Category        string   `yaml:"category"`          // logical group, e.g. "reference", "work", "personal"
}

// IsSearchDefault reports whether this source is included in queries by default.
// Returns true when search_by_default is omitted (nil) or explicitly true.
func (s Source) IsSearchDefault() bool {
	return s.SearchByDefault == nil || *s.SearchByDefault
}

// Personal holds configuration for the personal document safe (Mode 1).
type Personal struct {
	WatchDirs []string `yaml:"watchDirs"`
	DestDir   string   `yaml:"destDir"`
}

// RepoRoot describes a directory where git repositories are cloned.
// nexus uses it to locate existing clones and suggest placement for new ones.
//
// Matching uses most-specific-wins:
//   - A root with matching host AND matching group wins over host-only.
//   - Personal roots carry your username as the group (e.g. "amaina", "iamaina").
//   - Work roots have no groups — they catch everything the personal roots don't claim.
//
// Example: gitlab.com/amaina/my-project → personal-gitlab (host+group match)
//
//	gitlab.com/gl-infra/delivery  → work           (host-only, personal doesn't match)
type RepoRoot struct {
	Name   string   `yaml:"name"`   // e.g. "work", "personal-github", "personal-gitlab"
	Path   string   `yaml:"path"`   // absolute or ~ path to the root directory
	Hosts  []string `yaml:"hosts"`  // git host substrings — "gitlab" matches all gitlab-based hosts
	Groups []string `yaml:"groups"` // personal namespace(s) only — omit on work roots to act as fallback
	Watch  bool     `yaml:"watch"`  // if true, nexus watch registers new .git dirs automatically
}

// Roots holds the workspace OS configuration (Mode 3).
// All fields are optional — omitting this section changes no existing behaviour.
type Roots struct {
	Workspace string     `yaml:"workspace"` // top-level workspace directory to watch for structural changes
	Repos     []RepoRoot `yaml:"repos"`     // repo roots by type and platform
}

// GdocConfig holds Google Docs integration settings.
// All fields are optional — omit the section entirely to disable Google Docs support.
type GdocConfig struct {
	CredentialsPath string `yaml:"credentialsPath"` // path to Google OAuth credentials.json (from Google Cloud Console)
	TokenPath       string `yaml:"tokenPath"`       // token cache — defaults to ~/.config/nexus/gdoc_token.json
	SyncDir         string `yaml:"syncDir"`         // where fetched docs are saved as .md files
}

// URLSource represents a web URL (or a docs site to crawl) that nexus ingests
// and optionally re-checks on a schedule.
type URLSource struct {
	Name            string `yaml:"name"`              // source label used in nexus query results
	URL             string `yaml:"url"`               // seed URL to fetch
	Recursive       bool   `yaml:"recursive"`         // if true, follow links within the same path prefix
	Depth           int    `yaml:"depth"`             // max crawl depth (0 = unlimited)
	Watch           bool   `yaml:"watch"`             // if true, nexus watch re-checks on Interval
	Interval        string `yaml:"interval"`          // polling interval, e.g. "24h", "6h" (default: "24h")
	Delay           string `yaml:"delay"`             // pause between requests, e.g. "200ms", "1s" (default: none)
	SearchByDefault *bool  `yaml:"search_by_default"` // nil/true = included by default; false = opt-in only
	Category        string `yaml:"category"`          // logical group, e.g. "reference"
}

// IsSearchDefault reports whether this URL source is included in queries by default.
func (u URLSource) IsSearchDefault() bool {
	return u.SearchByDefault == nil || *u.SearchByDefault
}

// Config is the fully resolved application configuration.
type Config struct {
	cfgPath  string      // not exported — set by Load, used by Save
	Sources  []Source    `yaml:"sources"`
	URLs     []URLSource `yaml:"urls"` // web URLs / docs sites to ingest — optional
	Personal Personal    `yaml:"personal"`
	Roots    Roots       `yaml:"roots"` // workspace OS layer — optional, safe to omit
	Gdoc     GdocConfig  `yaml:"gdoc"`  // Google Docs integration — optional, safe to omit
	Postgres struct {
		DSN string `yaml:"dsn"`
	} `yaml:"postgres"`
	Ollama struct {
		BaseURL             string `yaml:"baseURL"`
		EmbeddingModel      string `yaml:"embeddingModel"`
		GenerationModel     string `yaml:"generationModel"`
		ClassificationModel string `yaml:"classificationModel"`
	} `yaml:"ollama"`
	LogLevel           *string `yaml:"log_level"`
	RelevanceThreshold float64 `yaml:"relevanceThreshold"`
}

// Load reads and parses the config file at cfgPath, expands ~ in paths,
// resolves ${PG_PASSWORD}, and returns the config. cfgPath may be empty
// (uses ~/ops-nexus/nexus/config.yaml).
func Load(cfgPath string) (*Config, error) {
	if cfgPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve home dir: %w", err)
		}
		cfgPath = filepath.Join(home, "ops-nexus/nexus", "config.yaml")
	}

	data, err := os.ReadFile(cfgPath) //nolint:gosec // cfgPath is always our controlled config.yaml
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", cfgPath, err)
	}

	// Use gopkg.in/yaml.v3 via an import alias to avoid a direct import at
	// package level — keeps the config package dependency-light.
	var cfg Config
	if err := unmarshalYAML(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	cfg.cfgPath = cfgPath // remember for Save()

	if err := cfg.resolve(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// ConfigPath returns the file path this Config was loaded from.
// Returns an empty string if the config was not loaded via Load.
func (c *Config) ConfigPath() string { return c.cfgPath }

// Save writes the current configuration back to the file it was loaded from.
// Comments and hand-written formatting are not preserved — the file is fully
// re-marshaled from the in-memory struct. Inform the user before calling this
// so they are not surprised to lose comments.
func (c *Config) Save() error {
	if c.cfgPath == "" {
		return fmt.Errorf("config path not set — was this Config loaded via config.Load?")
	}
	data, err := marshalYAML(c)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(c.cfgPath, data, 0o600); err != nil { //nolint:gosec
		return fmt.Errorf("write config %s: %w", c.cfgPath, err)
	}
	return nil
}

// resolve expands ~ in path fields and substitutes ${PG_PASSWORD}.
func (c *Config) resolve() error {
	home, _ := os.UserHomeDir()

	expandHome := func(p string) string {
		if strings.HasPrefix(p, "~/") {
			return filepath.Join(home, p[2:])
		}
		return p
	}

	for i := range c.Sources {
		c.Sources[i].Path = expandHome(c.Sources[i].Path)
		for j, excl := range c.Sources[i].Exclude {
			c.Sources[i].Exclude[j] = expandHome(excl)
		}
	}
	c.Personal.DestDir = expandHome(c.Personal.DestDir)
	for i, d := range c.Personal.WatchDirs {
		c.Personal.WatchDirs[i] = expandHome(d)
	}

	c.Roots.Workspace = expandHome(c.Roots.Workspace)
	for i := range c.Roots.Repos {
		c.Roots.Repos[i].Path = expandHome(c.Roots.Repos[i].Path)
	}

	c.Gdoc.CredentialsPath = expandHome(c.Gdoc.CredentialsPath)
	c.Gdoc.TokenPath = expandHome(c.Gdoc.TokenPath)
	c.Gdoc.SyncDir = expandHome(c.Gdoc.SyncDir)

	password := os.Getenv("PG_PASSWORD")
	c.Postgres.DSN = strings.ReplaceAll(c.Postgres.DSN, "${PG_PASSWORD}", password)

	return nil
}

// FindRepoRoot returns the best-matching RepoRoot for a normalised remote URL
// using most-specific-wins: host+group match beats host-only match.
// Returns nil if no root matches.
func (c *Config) FindRepoRoot(normalisedURL string) *RepoRoot {
	lower := strings.ToLower(normalisedURL)
	var best *RepoRoot
	bestScore := 0

	for i := range c.Roots.Repos {
		r := &c.Roots.Repos[i]
		score := 0
		for _, host := range r.Hosts {
			if strings.Contains(lower, strings.ToLower(host)) {
				score++
			}
		}
		if score == 0 {
			continue
		}
		for _, group := range r.Groups {
			if strings.Contains(lower, strings.ToLower(group)) {
				score += 10 // group match decisively wins
			}
		}
		if score > bestScore {
			bestScore = score
			best = r
		}
	}
	return best
}
