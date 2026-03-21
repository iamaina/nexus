// Package config handles loading and validation of nexus configuration from YAML.
package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/iamaina/nexus/internal/logger"
	"gopkg.in/yaml.v3"
)

// Source represents a configured source of documents to ingest, including its name, file path, and allowed extensions.
type Source struct {
	Name       string   `yaml:"name"`
	Path       string   `yaml:"path"`
	Extensions []string `yaml:"extensions"`
}

// Config represents the overall configuration for the nexus application, including document sources, database connection info, logging level, and relevance threshold.
type Config struct {
	Sources  []Source `yaml:"sources"`
	Postgres struct {
		DSN string `yaml:"dsn"`
	} `yaml:"postgres"`
	LogLevel           *string `yaml:"log_level"`
	RelevanceThreshold float32 `yaml:"relevanceThreshold"`
}

// C is the global configuration instance loaded at application startup.
var C Config

// Load loads the configuration from the specified file path or the default location if none is provided, and unmarshals it into the global C variable.
func Load(cfgPath string) error {
	if cfgPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		cfgPath = filepath.Join(home, "ops-nexus/nexus", "config.yaml")
	}

	data, err := os.ReadFile(cfgPath) //nolint:gosec // Safe: cfgPath is always from our controlled config.yaml
	if err != nil {
		return fmt.Errorf("cannot read config %s: %w", cfgPath, err)
	}

	if err := yaml.Unmarshal(data, &C); err != nil {
		return fmt.Errorf("invalid yaml: %w", err)
	}

	// Expand ~ in paths
	home, _ := os.UserHomeDir()
	for i := range C.Sources {
		p := &C.Sources[i].Path
		if strings.HasPrefix(*p, "~/") {
			*p = filepath.Join(home, (*p)[2:])
		}
	}

	if len(C.Sources) == 0 {
		return fmt.Errorf("no sources defined in config.yaml")
	}

	return nil
}

// ResolveSecrets replaces placeholders in the configuration with actual secret values from environment variables, such as the PostgreSQL password.
func (c *Config) ResolveSecrets() error {
	if c.Postgres.DSN == "" {
		return nil // no postgres → skip
	}

	password := os.Getenv("PG_PASSWORD")
	if strings.Contains(c.Postgres.DSN, "${PG_PASSWORD}") && password == "" {
		logger.Warn(context.Background(), "PG_PASSWORD env var not set — DSN will use empty password (peer auth or .pgpass fallback)")
	}

	c.Postgres.DSN = strings.ReplaceAll(c.Postgres.DSN, "${PG_PASSWORD}", password)
	return nil
}
