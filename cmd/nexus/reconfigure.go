package nexus

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/iamaina/nexus/internal/config"
	"github.com/spf13/cobra"
)

// reconfigureCmd is a standalone config editor — it does not require the database
// or Ollama (it is added to the skipInit list in main.go). It loads config.yaml
// directly, shows current values, prompts for changes, and saves back.
var reconfigureCmd = &cobra.Command{
	Use:   "setup-reconfigure",
	Short: "Interactively update sections of config.yaml",
	Long: `Opens a menu to update individual sections of config.yaml without
re-running the full 'make setup'. Changes are applied immediately.

Does not require the database or Ollama to be running.

Since: v0.1.0  (URL source editing added v0.2.0)`,
	RunE: func(_ *cobra.Command, _ []string) error {
		cfg, err := config.Load("")
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}

		scanner := bufio.NewScanner(os.Stdin)
		readline := func(prompt string) string {
			fmt.Print(prompt)
			scanner.Scan()
			return strings.TrimSpace(scanner.Text())
		}

		for {
			fmt.Printf("\nnexus setup-reconfigure — %s\n\n", cfg.ConfigPath())
			fmt.Println("  [1] Models       — generation and classification models")
			fmt.Println("  [2] Sources      — file sources (add/remove)")
			fmt.Println("  [3] Database     — Postgres DSN")
			fmt.Println("  [q] Quit")
			fmt.Println()

			choice := readline("  Select [1-3 or q]: ")
			switch choice {
			case "1":
				if err := reconfigureModels(cfg, readline); err != nil {
					fmt.Printf("  Error: %v\n", err)
				}
			case "2":
				reconfigureSources(cfg, readline)
			case "3":
				if err := reconfigureDatabase(cfg, readline); err != nil {
					fmt.Printf("  Error: %v\n", err)
				}
			case "q", "Q", "quit", "exit", "":
				fmt.Println("\nDone.")
				return nil
			default:
				fmt.Printf("  Unknown option: %q\n", choice)
			}
		}
	},
}

// ── Models ──────────────────────────────────────────────────────────────────

func reconfigureModels(cfg *config.Config, readline func(string) string) error {
	fmt.Printf("\n  Current models:\n")
	fmt.Printf("    Embedding:      %s  (fixed — changing requires DB migration)\n", cfg.Ollama.EmbeddingModel)
	fmt.Printf("    Generation:     %s\n", cfg.Ollama.GenerationModel)
	fmt.Printf("    Classification: %s\n\n", cfg.Ollama.ClassificationModel)

	fmt.Println("  Tiers:")
	fmt.Println("    [1] Balanced     — llama3.2:3b + qwen2.5:1.5b   (~3.5 GB total, fastest)")
	fmt.Println("    [2] Recommended  — llama3.2:3b + qwen2.5:3b     (~4.6 GB total, better accuracy)")
	fmt.Println("    [3] Large        — llama3.1:8b + qwen2.5:7b     (~10 GB total, best quality)")
	fmt.Println("    [4] Custom       — enter model names individually")
	fmt.Println("    [Enter] Keep current")
	fmt.Println()

	choice := readline("  Select tier [1-4 or Enter]: ")
	var gen, cls string
	switch choice {
	case "1":
		gen, cls = "llama3.2:3b", "qwen2.5:1.5b"
	case "2":
		gen, cls = "llama3.2:3b", "qwen2.5:3b"
	case "3":
		gen, cls = "llama3.1:8b", "qwen2.5:7b"
	case "4":
		gen = readline(fmt.Sprintf("  Generation model [%s]: ", cfg.Ollama.GenerationModel))
		if gen == "" {
			gen = cfg.Ollama.GenerationModel
		}
		cls = readline(fmt.Sprintf("  Classification model [%s]: ", cfg.Ollama.ClassificationModel))
		if cls == "" {
			cls = cfg.Ollama.ClassificationModel
		}
	case "":
		fmt.Println("  No change.")
		return nil
	default:
		fmt.Printf("  Unknown option %q — no change.\n", choice)
		return nil
	}

	cfg.Ollama.GenerationModel = gen
	cfg.Ollama.ClassificationModel = cls

	confirm := readline(fmt.Sprintf("  Set generation=%s classification=%s and save? [Y/n]: ", gen, cls))
	if confirm != "" && !strings.HasPrefix(strings.ToLower(confirm), "y") {
		fmt.Println("  Cancelled.")
		return nil
	}

	if err := cfg.Save(); err != nil {
		return err
	}
	fmt.Printf("  ✅  Saved. Remember to pull the new models:\n")
	fmt.Printf("      ollama pull %s\n", gen)
	fmt.Printf("      ollama pull %s\n", cls)
	return nil
}

// ── Sources ──────────────────────────────────────────────────────────────────

// reconfigureSources shows all configured sources (file + URL) and allows
// editing search_by_default / category, or removing a source.
func reconfigureSources(cfg *config.Config, readline func(string) string) {
	total := len(cfg.Sources) + len(cfg.URLs)
	if total == 0 {
		fmt.Println("\n  No sources configured.")
		fmt.Println("  Use 'nexus source scan' to discover repo directories,")
		fmt.Println("  or 'nexus ingest-url <url>' to add a web source.")
		return
	}

	for {
		fmt.Printf("\n  Configured sources — %d file, %d URL\n\n", len(cfg.Sources), len(cfg.URLs))

		idx := 1
		// File sources
		if len(cfg.Sources) > 0 {
			fmt.Println("  File sources:")
			for _, s := range cfg.Sources {
				searchLabel := ""
				if !s.IsSearchDefault() {
					searchLabel = "  search_by_default: false"
				}
				catLabel := ""
				if s.Category != "" {
					catLabel = fmt.Sprintf("  category: %s", s.Category)
				}
				fmt.Printf("    [%d] %-20s  %s%s%s\n", idx, s.Name, s.Path, catLabel, searchLabel)
				idx++
			}
		}
		// URL sources
		if len(cfg.URLs) > 0 {
			fmt.Println("  URL sources:")
			for _, u := range cfg.URLs {
				searchLabel := ""
				if !u.IsSearchDefault() {
					searchLabel = "  search_by_default: false"
				}
				catLabel := ""
				if u.Category != "" {
					catLabel = fmt.Sprintf("  category: %s", u.Category)
				}
				fmt.Printf("    [%d] %-20s  %s%s%s\n", idx, u.Name, u.URL, catLabel, searchLabel)
				idx++
			}
		}

		fmt.Println()
		fmt.Println("  Enter a number to edit (category / search_by_default).")
		fmt.Printf("  Prefix with 'r' to remove (e.g. r2). Press Enter to go back.\n\n")

		choice := readline("  Select: ")
		if choice == "" {
			return
		}

		remove := false
		if strings.HasPrefix(choice, "r") || strings.HasPrefix(choice, "R") {
			remove = true
			choice = choice[1:]
		}

		var n int
		if _, err := fmt.Sscanf(choice, "%d", &n); err != nil || n < 1 || n >= idx {
			fmt.Printf("  Invalid selection: %q\n", choice)
			continue
		}
		n-- // 0-based

		// Determine whether it's a file or URL source.
		if n < len(cfg.Sources) {
			// File source
			if remove {
				removedName := cfg.Sources[n].Name
				removedPath := cfg.Sources[n].Path
				confirm := readline(fmt.Sprintf("  Remove file source %q (%s)? [Y/n]: ", removedName, removedPath))
				if confirm != "" && !strings.HasPrefix(strings.ToLower(confirm), "y") {
					fmt.Println("  Cancelled.")
					continue
				}
				cfg.Sources = append(cfg.Sources[:n], cfg.Sources[n+1:]...)
				if err := cfg.Save(); err != nil {
					fmt.Printf("  Error saving config: %v\n", err)
					continue
				}
				fmt.Printf("  ✅  Removed source %q.\n", removedName)
				fmt.Printf("  Note: ingested documents remain in DB. To purge:\n")
				fmt.Printf("    DELETE FROM documents WHERE source_name = '%s';\n", removedName)
			} else {
				editFileSource(cfg, n, readline)
			}
		} else {
			// URL source
			urlIdx := n - len(cfg.Sources)
			if remove {
				removedName := cfg.URLs[urlIdx].Name
				removedURL := cfg.URLs[urlIdx].URL
				confirm := readline(fmt.Sprintf("  Remove URL source %q (%s)? [Y/n]: ", removedName, removedURL))
				if confirm != "" && !strings.HasPrefix(strings.ToLower(confirm), "y") {
					fmt.Println("  Cancelled.")
					continue
				}
				cfg.URLs = append(cfg.URLs[:urlIdx], cfg.URLs[urlIdx+1:]...)
				if err := cfg.Save(); err != nil {
					fmt.Printf("  Error saving config: %v\n", err)
					continue
				}
				fmt.Printf("  ✅  Removed URL source %q.\n", removedName)
				fmt.Printf("  Note: ingested documents remain in DB. To purge:\n")
				fmt.Printf("    DELETE FROM documents WHERE source_name = '%s';\n", removedName)
			} else {
				editURLSource(cfg, urlIdx, readline)
			}
		}
	}
}

func editFileSource(cfg *config.Config, idx int, readline func(string) string) {
	s := &cfg.Sources[idx]
	currentDefault := s.IsSearchDefault()
	fmt.Printf("\n  Editing file source: %s (%s)\n", s.Name, s.Path)
	fmt.Printf("    category:          %q\n", s.Category)
	fmt.Printf("    search_by_default: %v\n\n", currentDefault)

	fmt.Println("  [1] Toggle search_by_default")
	fmt.Println("  [2] Set category")
	fmt.Println("  [Enter] Done")
	fmt.Println()

	choice := readline("  Select [1-2 or Enter]: ")
	switch choice {
	case "1":
		if currentDefault {
			f := false
			s.SearchByDefault = &f
			fmt.Println("  search_by_default → false (excluded from default search)")
		} else {
			t := true
			s.SearchByDefault = &t
			fmt.Println("  search_by_default → true (included in default search)")
		}
	case "2":
		cat := readline(fmt.Sprintf("  Category [%s]: ", s.Category))
		if cat != "" {
			s.Category = cat
		}
	case "":
		return
	default:
		fmt.Printf("  Unknown option %q\n", choice)
		return
	}

	if err := cfg.Save(); err != nil {
		fmt.Printf("  Error saving config: %v\n", err)
		return
	}
	fmt.Println("  ✅  Saved.")
}

func editURLSource(cfg *config.Config, idx int, readline func(string) string) {
	u := &cfg.URLs[idx]
	currentDefault := u.IsSearchDefault()
	fmt.Printf("\n  Editing URL source: %s (%s)\n", u.Name, u.URL)
	fmt.Printf("    category:          %q\n", u.Category)
	fmt.Printf("    search_by_default: %v\n\n", currentDefault)

	fmt.Println("  [1] Toggle search_by_default")
	fmt.Println("  [2] Set category")
	fmt.Println("  [Enter] Done")
	fmt.Println()

	choice := readline("  Select [1-2 or Enter]: ")
	switch choice {
	case "1":
		if currentDefault {
			f := false
			u.SearchByDefault = &f
			fmt.Println("  search_by_default → false (excluded from default search)")
		} else {
			t := true
			u.SearchByDefault = &t
			fmt.Println("  search_by_default → true (included in default search)")
		}
	case "2":
		cat := readline(fmt.Sprintf("  Category [%s]: ", u.Category))
		if cat != "" {
			u.Category = cat
		}
	case "":
		return
	default:
		fmt.Printf("  Unknown option %q\n", choice)
		return
	}

	if err := cfg.Save(); err != nil {
		fmt.Printf("  Error saving config: %v\n", err)
		return
	}
	fmt.Println("  ✅  Saved.")
}

// ── Database ─────────────────────────────────────────────────────────────────

func reconfigureDatabase(cfg *config.Config, readline func(string) string) error {
	current := maskDSNReconfigure(cfg.Postgres.DSN)
	fmt.Printf("\n  Current DSN: %s\n\n", current)
	fmt.Println("  Use ${PG_PASSWORD} in the DSN — nexus substitutes it at runtime.")
	fmt.Println("  Example: postgres://vaultuser:${PG_PASSWORD}@localhost:5432/opsnexus?sslmode=disable")
	fmt.Println()

	newDSN := readline("  New DSN [Enter to keep current]: ")
	if newDSN == "" {
		fmt.Println("  No change.")
		return nil
	}

	cfg.Postgres.DSN = newDSN
	if err := cfg.Save(); err != nil {
		return err
	}
	fmt.Println("  ✅  DSN updated.")
	return nil
}

func maskDSNReconfigure(dsn string) string {
	if idx := strings.Index(dsn, "@"); idx != -1 {
		return dsn[:strings.Index(dsn, ":")+1] + "*****" + dsn[idx:]
	}
	return dsn
}

func init() {
	RootCmd.AddCommand(reconfigureCmd)
}
