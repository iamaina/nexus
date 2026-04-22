package nexus

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/iamaina/nexus/internal/app"
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
  nexus ingest-url https://docs.chef.io/workstation/26/tools/knife/ --recursive --depth 2`,
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
		delay := parseDelay(delayStr)

		if source == "" {
			source = defaultSourceName(rawURL)
		}

		ctx := cmd.Context()
		a := ctx.Value(app.AppKey).(*app.Application)

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

func init() {
	ingestURLCmd.Flags().Bool("recursive", false, "Follow links within the same URL path prefix")
	ingestURLCmd.Flags().Bool("dry-run", false, "Show what would be ingested without touching the database")
	ingestURLCmd.Flags().Bool("force", false, "Re-ingest pages even if content hash is unchanged")
	ingestURLCmd.Flags().Int("depth", 0, "Maximum crawl depth when --recursive is set (0 = unlimited)")
	ingestURLCmd.Flags().String("source", "", "Source name for ingested pages (default: derived from URL host)")
	ingestURLCmd.Flags().String("delay", "", "Pause between requests, e.g. 200ms, 1s (default: none)")
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
