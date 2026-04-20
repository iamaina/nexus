package nexus

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/iamaina/nexus/internal/app"
	"github.com/iamaina/nexus/internal/ingestion"
	"github.com/iamaina/nexus/internal/logger"
	"github.com/iamaina/nexus/internal/models"
	"github.com/spf13/cobra"
)

var gdocCmd = &cobra.Command{
	Use:   "gdoc",
	Short: "Manage Google Doc sources",
	Long: `Register Google Docs as nexus sources. Fetched docs are saved as Markdown
files and ingested into the search index. nexus watch re-syncs them every 30 minutes.

Prerequisites:
  1. Create a Google Cloud project and enable the Google Docs API.
  2. Create OAuth 2.0 credentials (Desktop app type) and download credentials.json.
  3. Add the credentials path to config.yaml under gdoc.credentialsPath.
  4. Run: nexus gdoc auth`,
}

var gdocAuthCmd = &cobra.Command{
	Use:   "auth",
	Short: "Authenticate with Google (run once)",
	Long: `Open a browser window to authorise nexus to read your Google Docs.
The resulting token is saved to the configured tokenPath and reused automatically.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		ctx := cmd.Context()
		a := ctx.Value(app.AppKey).(*app.Application)

		credsPath, _, err := gdocResolvePaths(a)
		if err != nil {
			return err
		}

		tokenPath := gdocTokenPath(a)
		fmt.Println("Opening browser for Google authorisation…")
		if err := gdocRunScript(ctx, "auth", credsPath, tokenPath); err != nil {
			return fmt.Errorf("auth failed: %w", err)
		}
		fmt.Printf("✅  Token saved. You can now run: nexus gdoc add <url> --name <name>\n")
		return nil
	},
}

var gdocAddName string

var gdocAddCmd = &cobra.Command{
	Use:   "add <url>",
	Short: "Register a Google Doc and ingest it immediately",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		a := ctx.Value(app.AppKey).(*app.Application)

		docID := gdocExtractID(args[0])
		if docID == "" {
			return fmt.Errorf("could not parse doc ID from %q — pass the full Google Docs URL", args[0])
		}

		// Check for existing token
		tokenPath := gdocTokenPath(a)
		if _, err := os.Stat(tokenPath); os.IsNotExist(err) {
			return fmt.Errorf("not authenticated — run: nexus gdoc auth")
		}

		existing, err := a.Gdocs.Get(ctx, gdocAddName)
		if err != nil {
			return err
		}
		if existing != nil {
			return fmt.Errorf("a doc named %q is already registered — use 'nexus gdoc sync %s' to refresh it", gdocAddName, gdocAddName)
		}

		if err := a.Gdocs.Add(ctx, gdocAddName, docID); err != nil {
			return err
		}

		d, _ := a.Gdocs.Get(ctx, gdocAddName)
		fmt.Printf("  Fetching %q from Google Docs…\n", gdocAddName)
		if err := syncGdoc(ctx, a, *d); err != nil {
			// Roll back the DB entry so the user can retry cleanly
			_ = a.Gdocs.Remove(ctx, gdocAddName)
			return fmt.Errorf("initial fetch failed: %w\n\nFix the error above then re-run: nexus gdoc add %s --name %s",
				err, args[0], gdocAddName)
		}
		fmt.Printf("  ✅  %q registered and ingested. Query it with: nexus query \"...\"\n", gdocAddName)
		return nil
	},
}

var gdocSyncCmd = &cobra.Command{
	Use:   "sync [name]",
	Short: "Re-fetch and re-ingest one or all registered docs",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		a := ctx.Value(app.AppKey).(*app.Application)

		if len(args) == 1 {
			d, err := a.Gdocs.Get(ctx, args[0])
			if err != nil {
				return err
			}
			if d == nil {
				return fmt.Errorf("no doc named %q — use 'nexus gdoc list' to see registered docs", args[0])
			}
			fmt.Printf("  Syncing %q…\n", d.Name)
			if err := syncGdoc(ctx, a, *d); err != nil {
				return err
			}
			fmt.Printf("  ✅  %q synced.\n", d.Name)
			return nil
		}

		// Sync all
		docs, err := a.Gdocs.List(ctx)
		if err != nil {
			return err
		}
		if len(docs) == 0 {
			fmt.Println("No docs registered. Add one with: nexus gdoc add <url> --name <name>")
			return nil
		}
		for _, d := range docs {
			fmt.Printf("  Syncing %q…\n", d.Name)
			if err := syncGdoc(ctx, a, d); err != nil {
				fmt.Fprintf(os.Stderr, "  ⚠️  %s: %v\n", d.Name, err)
			} else {
				fmt.Printf("  ✅  %q synced.\n", d.Name)
			}
		}
		return nil
	},
}

var gdocListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all registered Google Doc sources",
	RunE: func(cmd *cobra.Command, _ []string) error {
		ctx := cmd.Context()
		a := ctx.Value(app.AppKey).(*app.Application)

		docs, err := a.Gdocs.List(ctx)
		if err != nil {
			return err
		}
		if len(docs) == 0 {
			fmt.Println("No Google Docs registered. Add one with: nexus gdoc add <url> --name <name>")
			return nil
		}

		fmt.Println()
		fmt.Printf("  %-24s  %-44s  %s\n", "NAME", "DOC ID", "LAST SYNCED")
		fmt.Printf("  %-24s  %-44s  %s\n",
			strings.Repeat("─", 24), strings.Repeat("─", 44), strings.Repeat("─", 16))
		for _, d := range docs {
			synced := "never"
			if d.LastSynced != nil {
				synced = d.LastSynced.Format("2006-01-02 15:04")
			}
			fmt.Printf("  %-24s  %-44s  %s\n", d.Name, d.DocID, synced)
		}
		fmt.Println()
		return nil
	},
}

var gdocRmCmd = &cobra.Command{
	Use:   "rm <name>",
	Short: "Remove a registered Google Doc source and purge it from the search index",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		a := ctx.Value(app.AppKey).(*app.Application)

		name := args[0]

		// Resolve the sync file path before removing the DB record.
		_, syncDir, _ := gdocResolvePaths(a)
		if syncDir == "" {
			home, _ := os.UserHomeDir()
			syncDir = filepath.Join(home, ".local", "share", "nexus", "gdocs")
		}
		syncFile := filepath.Join(syncDir, name+".md")

		// Remove from gdocs registry.
		if err := a.Gdocs.Remove(ctx, name); err != nil {
			return err
		}

		// Purge from the search index (chunks cascade automatically).
		if err := a.Documents.DeleteByPath(ctx, syncFile); err != nil {
			fmt.Fprintf(os.Stderr, "  ⚠️  removed from registry but could not purge index: %v\n", err)
		}

		// Delete the local Markdown file.
		_ = os.Remove(syncFile)

		fmt.Printf("✅  %q removed from registry and purged from search index.\n", name)
		return nil
	},
}

// syncGdoc fetches a Google Doc, writes it to syncDir/{name}.md, and ingests it.
// Called by add/sync commands and by nexus watch on the 30-min ticker.
func syncGdoc(ctx context.Context, a *app.Application, d models.Gdoc) error {
	credsPath, syncDir, err := gdocResolvePaths(a)
	if err != nil {
		return err
	}
	tokenPath := gdocTokenPath(a)

	if _, err := os.Stat(tokenPath); os.IsNotExist(err) {
		return fmt.Errorf("not authenticated — run: nexus gdoc auth")
	}

	if err := os.MkdirAll(syncDir, 0o750); err != nil {
		return fmt.Errorf("create sync dir %s: %w", syncDir, err)
	}

	// Fetch doc text via Python script
	out, err := gdocRunScriptOutput(ctx, "fetch", d.DocID, credsPath, tokenPath)
	if err != nil {
		return fmt.Errorf("fetch %q: %w", d.Name, err)
	}

	// Write to file
	destPath := filepath.Join(syncDir, d.Name+".md")
	if err := os.WriteFile(destPath, []byte(out), 0o600); err != nil { //nolint:gosec
		return fmt.Errorf("write %s: %w", destPath, err)
	}

	// Ingest — force=false: hash check skips unchanged content
	if _, err := ingestion.IngestFile(ctx, a, destPath, "gdocs", false, nil); err != nil {
		return fmt.Errorf("ingest %s: %w", destPath, err)
	}

	if err := a.Gdocs.UpdateLastSynced(ctx, d.ID); err != nil {
		logger.Warn(ctx, "gdoc: update last_synced failed", "name", d.Name, "err", err)
	}
	return nil
}

// gdocResolvePaths returns (credentialsPath, syncDir) from config, with defaults.
func gdocResolvePaths(a *app.Application) (credsPath, syncDir string, err error) {
	credsPath = a.Config.Gdoc.CredentialsPath
	if credsPath == "" {
		return "", "", fmt.Errorf(
			"gdoc.credentialsPath not set in config.yaml\n\n" +
				"  1. Create a Google Cloud project, enable Docs API, download credentials.json\n" +
				"  2. Add to config.yaml:\n" +
				"       gdoc:\n" +
				"         credentialsPath: ~/path/to/credentials.json\n" +
				"  3. Run: nexus gdoc auth",
		)
	}

	syncDir = a.Config.Gdoc.SyncDir
	if syncDir == "" {
		home, _ := os.UserHomeDir()
		syncDir = filepath.Join(home, ".local", "share", "nexus", "gdocs")
	}
	return credsPath, syncDir, nil
}

// gdocTokenPath returns the resolved token cache path, with a sensible default.
func gdocTokenPath(a *app.Application) string {
	if a.Config.Gdoc.TokenPath != "" {
		return a.Config.Gdoc.TokenPath
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "nexus", "gdoc_token.json")
}

// gdocExtractID parses a Google Doc ID from a full URL or returns the input as-is.
func gdocExtractID(urlOrID string) string {
	re := regexp.MustCompile(`/document/d/([a-zA-Z0-9_-]+)`)
	if m := re.FindStringSubmatch(urlOrID); len(m) == 2 {
		return m[1]
	}
	// Looks like a bare ID already
	if matched, _ := regexp.MatchString(`^[a-zA-Z0-9_-]{10,}$`, urlOrID); matched {
		return urlOrID
	}
	return ""
}

// gdocRunScript runs scripts/fetch_gdoc.py for side effects (auth flow).
func gdocRunScript(ctx context.Context, subcmd string, args ...string) error {
	if _, err := os.Stat(".venv/bin/python"); os.IsNotExist(err) {
		return fmt.Errorf("python environment not set up — run: make setup-python")
	}
	cmdArgs := append([]string{"scripts/fetch_gdoc.py", subcmd}, args...)
	cmd := exec.CommandContext(ctx, ".venv/bin/python", cmdArgs...) //nolint:gosec
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// gdocRunScriptOutput runs scripts/fetch_gdoc.py and captures stdout.
func gdocRunScriptOutput(ctx context.Context, subcmd string, args ...string) (string, error) {
	if _, err := os.Stat(".venv/bin/python"); os.IsNotExist(err) {
		return "", fmt.Errorf("python environment not set up — run: make setup-python")
	}
	cmdArgs := append([]string{"scripts/fetch_gdoc.py", subcmd}, args...)
	cmd := exec.CommandContext(ctx, ".venv/bin/python", cmdArgs...) //nolint:gosec
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func init() {
	gdocAddCmd.Flags().StringVar(&gdocAddName, "name", "", "short name to identify this doc (required)")
	_ = gdocAddCmd.MarkFlagRequired("name")

	gdocCmd.AddCommand(gdocAuthCmd, gdocAddCmd, gdocSyncCmd, gdocListCmd, gdocRmCmd)
	RootCmd.AddCommand(gdocCmd)
}
