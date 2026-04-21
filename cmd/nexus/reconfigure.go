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

Does not require the database or Ollama to be running.`,
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

func reconfigureSources(cfg *config.Config, readline func(string) string) {
	if len(cfg.Sources) == 0 {
		fmt.Println("\n  No sources configured. Use 'nexus source scan' to discover repo directories.")
		return
	}

	fmt.Printf("\n  Configured sources (%d):\n\n", len(cfg.Sources))
	for i, s := range cfg.Sources {
		fmt.Printf("    [%d] %-20s  %s\n", i+1, s.Name, s.Path)
	}
	fmt.Println()
	fmt.Println("  Enter a number to remove a source, or press Enter to go back.")
	fmt.Println("  (To add sources, run: nexus source scan)")
	fmt.Println()

	choice := readline("  Remove source number [Enter to cancel]: ")
	if choice == "" {
		return
	}

	idx := 0
	if _, err := fmt.Sscanf(choice, "%d", &idx); err != nil || idx < 1 || idx > len(cfg.Sources) {
		fmt.Printf("  Invalid selection: %q\n", choice)
		return
	}
	idx-- // convert to 0-based

	removed := cfg.Sources[idx]
	confirm := readline(fmt.Sprintf("  Remove source %q (%s)? [Y/n]: ", removed.Name, removed.Path))
	if confirm != "" && !strings.HasPrefix(strings.ToLower(confirm), "y") {
		fmt.Println("  Cancelled.")
		return
	}

	cfg.Sources = append(cfg.Sources[:idx], cfg.Sources[idx+1:]...)
	if err := cfg.Save(); err != nil {
		fmt.Printf("  Error saving config: %v\n", err)
		return
	}
	fmt.Printf("  ✅  Removed source %q.\n", removed.Name)
	fmt.Println("  Note: existing ingested documents from this source remain in the DB.")
	fmt.Println("  To remove them: DELETE FROM documents WHERE source_name = '" + removed.Name + "';")
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
