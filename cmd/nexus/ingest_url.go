package nexus

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/iamaina/nexus/internal/app"
	"github.com/iamaina/nexus/internal/config"
	"github.com/iamaina/nexus/internal/ingestion"
	"github.com/spf13/cobra"
)

var ingestURLCmd = &cobra.Command{
	Use:   "ingest-url <url>",
	Short: "Fetch and ingest a web page (or an entire docs site) into nexus",
	Long: `Fetch a URL, extract its text content, and ingest it into the nexus
knowledge base so it becomes queryable via "nexus query".

With --recursive, nexus crawls all pages whose URL starts with the same
path prefix as the seed URL — useful for ingesting an entire documentation
section in one command.

Examples:

  # Ingest a single page
  nexus ingest-url https://docs.chef.io/workstation/26/tools/knife/

  # Ingest the full knife docs (all sub-pages)
  nexus ingest-url https://docs.chef.io/workstation/26/tools/knife/ --recursive

  # Preview what would be ingested without touching the database
  nexus ingest-url https://docs.chef.io/workstation/26/tools/knife/ --recursive --dry-run

  # Use a custom source name (default: derived from the URL host)
  nexus ingest-url https://docs.chef.io/workstation/26/tools/knife/ --recursive --source chef-knife-docs

  # Limit crawl to 2 levels deep
  nexus ingest-url https://docs.chef.io/workstation/26/tools/knife/ --recursive --depth 2

  # Save to config.yaml so nexus ingest and nexus watch pick it up automatically
  nexus ingest-url https://sre.google/sre-book/ --recursive --source SRE-handbook --save

  # Save and enable watch polling
  nexus ingest-url https://sre.google/sre-book/ --recursive --source SRE-handbook --save --watch

  # Run in the background — returns immediately, logs to ~/.config/nexus/logs/
  nexus ingest-url https://sre.google/sre-book/ --recursive --source SRE-handbook --delay 300ms --save --background

Since: v0.2.0 (--save, --watch, --background added v0.3.0)`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		rawURL := args[0]

		// Validate the URL up front.
		u, err := url.Parse(rawURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return fmt.Errorf("invalid URL %q — must start with http:// or https://", rawURL)
		}

		recursive, _ := cmd.Flags().GetBool("recursive")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		force, _ := cmd.Flags().GetBool("force")
		depth, _ := cmd.Flags().GetInt("depth")
		source, _ := cmd.Flags().GetString("source")
		delayStr, _ := cmd.Flags().GetString("delay")
		save, _ := cmd.Flags().GetBool("save")
		watch, _ := cmd.Flags().GetBool("watch")
		background, _ := cmd.Flags().GetBool("background")
		delay := parseDelay(delayStr)

		if source == "" {
			source = defaultSourceName(rawURL)
		}

		if watch && !save {
			fmt.Println("  Note: --watch has no effect without --save")
		}

		ctx := cmd.Context()
		a := ctx.Value(app.AppKey).(*app.Application)

		// --save: upsert this source into config.yaml before doing anything else.
		// config.Save() rewrites the entire file — YAML comments are not preserved.
		if save {
			newSrc := config.URLSource{
				Name:      source,
				URL:       rawURL,
				Recursive: recursive,
				Depth:     depth,
				Watch:     watch,
				Delay:     delayStr,
			}
			upserted := false
			for i, existing := range a.Config.URLs {
				if existing.Name == newSrc.Name {
					a.Config.URLs[i] = newSrc
					upserted = true
					break
				}
			}
			if !upserted {
				a.Config.URLs = append(a.Config.URLs, newSrc)
			}
			if err := a.Config.Save(); err != nil {
				return fmt.Errorf("save config: %w", err)
			}
			action := "Added"
			if upserted {
				action = "Updated"
			}
			fmt.Printf("  ✓ %s %q in config.yaml", action, source)
			if watch {
				fmt.Printf(" (watch: true — nexus watch will poll this source)")
			}
			fmt.Println()
		}

		// --background: re-exec without --background, detached from terminal.
		// --save has already written config.yaml above, so the child process
		// runs the crawl only — no config writes happen in the child.
		if background {
			return runInBackground(source)
		}

		if dryRun {
			fmt.Printf("Dry run — source: %q\n\n", source)
		}

		if !recursive {
			if dryRun {
				fmt.Printf("  would ingest: %s\n", rawURL)
				return nil
			}
			ok, err := ingestion.IngestURL(ctx, a, rawURL, source, force)
			if err != nil {
				return err
			}
			if ok {
				fmt.Printf("Ingested 1 page into source %q\n", source)
			} else {
				fmt.Printf("Page already up to date — nothing ingested\n")
			}
			return nil
		}

		// Recursive crawl.
		fmt.Printf("Crawling %s (source: %q", rawURL, source)
		if depth > 0 {
			fmt.Printf(", max depth: %d", depth)
		}
		fmt.Println(")")
		fmt.Println()

		count, err := ingestion.CrawlAndIngest(ctx, a, rawURL, source, depth, delay, force, dryRun)
		if err != nil {
			return err
		}

		if dryRun {
			fmt.Printf("\n%d page(s) would be ingested\n", count)
		} else {
			fmt.Printf("\nDone — %d page(s) ingested into source %q\n", count, source)
			fmt.Printf("Run: nexus query \"<your question>\" --source %s\n", source)
		}
		return nil
	},
}

// runInBackground re-execs the current process without the --background flag,
// redirecting stdout and stderr to ~/.config/nexus/logs/ingest-url-<name>.log.
// The child is placed in its own session (Setsid) so it survives terminal close.
func runInBackground(sourceName string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}

	// Rebuild args without --background so the child does not recurse.
	args := make([]string, 0, len(os.Args))
	for _, a := range os.Args[1:] {
		if a == "--background" {
			continue
		}
		args = append(args, a)
	}

	home, _ := os.UserHomeDir()
	logDir := filepath.Join(home, ".config", "nexus", "logs")
	if err := os.MkdirAll(logDir, 0o750); err != nil {
		return fmt.Errorf("create log dir: %w", err)
	}
	logPath := filepath.Join(logDir, "ingest-url-"+sourceName+".log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) //nolint:gosec
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}

	bgCmd := exec.Command(exe, args...) //nolint:gosec
	bgCmd.Stdout = logFile
	bgCmd.Stderr = logFile
	bgCmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := bgCmd.Start(); err != nil {
		_ = logFile.Close()
		return fmt.Errorf("start background process: %w", err)
	}
	_ = logFile.Close()

	fmt.Printf("\n  ⟳ Crawling %q in background [pid %d]\n", sourceName, bgCmd.Process.Pid)
	fmt.Printf("    Log: %s\n", logPath)
	fmt.Printf("    tail -f %s\n\n", logPath)
	return nil
}

func init() {
	ingestURLCmd.Flags().Bool("recursive", false, "Follow links within the same URL path prefix")
	ingestURLCmd.Flags().Bool("dry-run", false, "Show what would be ingested without touching the database")
	ingestURLCmd.Flags().Bool("force", false, "Re-ingest pages even if content hash is unchanged")
	ingestURLCmd.Flags().Int("depth", 0, "Maximum crawl depth when --recursive is set (0 = unlimited)")
	ingestURLCmd.Flags().String("source", "", "Source name for ingested pages (default: derived from URL host)")
	ingestURLCmd.Flags().String("delay", "", "Pause between requests, e.g. 200ms, 1s (default: none)")
	ingestURLCmd.Flags().Bool("save", false, "Persist this source to config.yaml so nexus ingest and nexus watch pick it up automatically")
	ingestURLCmd.Flags().Bool("watch", false, "When used with --save, set watch: true so nexus watch polls this source on its interval")
	ingestURLCmd.Flags().Bool("background", false, "Run the crawl in the background; returns immediately and logs to ~/.config/nexus/logs/")
	RootCmd.AddCommand(ingestURLCmd)
}

// defaultSourceName derives a short source name from the URL.
// "https://docs.chef.io/workstation/26/tools/knife/" → "docs.chef.io"
func defaultSourceName(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "web"
	}
	return strings.TrimPrefix(u.Host, "www.")
}

// parseDelay parses a duration string like "200ms" or "1s".
// Returns 0 if the string is empty or unparseable.
func parseDelay(s string) time.Duration {
	if s == "" {
		return 0
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0
	}
	return d
}
