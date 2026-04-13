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
	Name       string   `yaml:"name"`
	Path       string   `yaml:"path"`
	Extensions []string `yaml:"extensions"`
	Exclude    []string `yaml:"exclude"` // path substrings to skip (directories or files)
	Watch      bool     `yaml:"watch"`   // if true, nexus watch re-ingests files on change
}

// Personal holds configuration for the personal document safe (Mode 1).
type Personal struct {
	WatchDirs []string `yaml:"watchDirs"`
	DestDir   string   `yaml:"destDir"`
}

// RepoRoot describes a directory where git repositories are cloned.
// nexus uses it to locate existing clones and suggest placement for new ones.
type RepoRoot struct {
	Name  string   `yaml:"name"`  // e.g. "work", "personal-github", "personal-gitlab"
	Path  string   `yaml:"path"`  // absolute or ~ path to the root directory
	Hosts []string `yaml:"hosts"` // git hosts whose repos belong here (e.g. ["github.com"])
	Watch bool     `yaml:"watch"` // if true, nexus watch registers new .git dirs automatically
}

// Roots holds the workspace OS configuration (Mode 3).
// All fields are optional — omitting this section changes no existing behaviour.
type Roots struct {
	Workspace string     `yaml:"workspace"` // top-level workspace directory to watch for structural changes
	Repos     []RepoRoot `yaml:"repos"`     // repo roots by type and platform
}

// Config is the fully resolved application configuration.
type Config struct {
	Sources  []Source `yaml:"sources"`
	Personal Personal `yaml:"personal"`
	Roots    Roots    `yaml:"roots"` // workspace OS layer — optional, safe to omit
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

	if err := cfg.resolve(); err != nil {
		return nil, err
	}

	return &cfg, nil
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

	password := os.Getenv("PG_PASSWORD")
	c.Postgres.DSN = strings.ReplaceAll(c.Postgres.DSN, "${PG_PASSWORD}", password)

	return nil
}
