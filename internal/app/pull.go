package app

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ollama/ollama/api"
)

// modelStatusDir returns the directory that holds per-model pull status files.
func modelStatusDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "nexus", "models")
}

// safeModelName converts a model name like "qwen2.5:3b" to a filesystem-safe
// string "qwen2.5_3b" for use as a status file name.
func safeModelName(model string) string {
	return strings.NewReplacer(":", "_", "/", "_").Replace(model)
}

// readModelPullStatus returns the last-written pull status for a model.
// Returns "" if no status file exists (model was never pulled via make setup).
func readModelPullStatus(model string) string {
	path := filepath.Join(modelStatusDir(), safeModelName(model)+".status")
	b, err := os.ReadFile(path) //nolint:gosec // path is constructed from UserHomeDir + safe model name, not user input
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// checkRequiredModels verifies that all three Ollama models are available.
// If any are still downloading (kicked off by make setup), it streams a
// live progress bar with ETA and waits for completion.
// The ctx should be the signal context from main so Ctrl+C cancels the wait.
func checkRequiredModels(ctx context.Context, ollamaURL, embModel, genModel, classifyModel string) error {
	u, err := url.Parse(ollamaURL)
	if err != nil {
		return fmt.Errorf("invalid ollama URL: %w", err)
	}
	client := api.NewClient(u, &http.Client{})

	// List what Ollama already has.
	listResp, err := client.List(ctx)
	if err != nil {
		return fmt.Errorf("list ollama models: %w", err)
	}

	available := make(map[string]bool, len(listResp.Models))
	for _, m := range listResp.Models {
		// Ollama appends ":latest" when a model is pulled without a tag;
		// normalise so "llama3.2:3b" matches in both forms.
		available[m.Name] = true
		available[strings.TrimSuffix(m.Name, ":latest")] = true
	}

	required := []string{embModel, genModel, classifyModel}
	var missing []string
	for _, m := range required {
		if !available[m] {
			missing = append(missing, m)
		}
	}
	if len(missing) == 0 {
		return nil
	}

	// One or more models are not yet available.
	fmt.Printf("\n⏳  %d model(s) not yet downloaded.\n\n", len(missing))

	for _, model := range missing {
		status := readModelPullStatus(model)

		switch status {
		case "failed":
			return fmt.Errorf(
				"model %s: all download attempts failed.\n"+
					"  Retry:  ollama pull %s\n"+
					"  Or re-run: make setup",
				model, model,
			)

		case "":
			// No status file — setup was never run or model was never requested.
			return fmt.Errorf(
				"model %s is not available.\n"+
					"  Pull it:  ollama pull %s\n"+
					"  Or run:   make setup",
				model, model,
			)

		default:
			// "pulling" or "retry:N" — a background pull is in progress.
			// Streaming client.Pull joins the existing download; Ollama deduplicates layers.
			fmt.Printf("  Downloading  %s\n", model)
			if err := pullWithProgress(ctx, client, model); err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					fmt.Printf("\n  ⏹  Interrupted. Run nexus again to see progress.\n\n")
					return err
				}
				return fmt.Errorf("pull %s: %w", model, err)
			}
			fmt.Printf("  ✅  %s ready\n\n", model)
		}
	}

	return nil
}

// pullWithProgress calls the Ollama pull API and renders a progress bar with
// an ETA derived from an exponential moving average of the download rate.
func pullWithProgress(ctx context.Context, client *api.Client, model string) error {
	type point struct {
		bytes int64
		at    time.Time
	}

	var (
		started    bool
		last       point
		rateEMA    float64 // exponential moving average bytes/s
		onProgLine bool    // true if the last print was on a \r progress line
	)

	return client.Pull(ctx, &api.PullRequest{Model: model}, func(resp api.ProgressResponse) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		switch {
		case resp.Status == "success":
			if onProgLine {
				fmt.Print("\r\033[K") // clear the progress line
			}
			return nil

		case resp.Total > 0:
			now := time.Now()
			if !started {
				started = true
				last = point{resp.Completed, now}
			}

			// Recompute EMA rate every 300ms to reduce jitter.
			if elapsed := now.Sub(last.at); elapsed >= 300*time.Millisecond {
				if elapsed > 0 && resp.Completed > last.bytes {
					rate := float64(resp.Completed-last.bytes) / elapsed.Seconds()
					if rateEMA == 0 {
						rateEMA = rate
					} else {
						rateEMA = 0.8*rateEMA + 0.2*rate
					}
					last = point{resp.Completed, now}
				}
			}

			pct := float64(resp.Completed) / float64(resp.Total)
			bar := renderBar(pct, 28)
			done := humanBytes(resp.Completed)
			total := humanBytes(resp.Total)

			eta := "--"
			if rateEMA > 1024 { // need at least 1 KB/s for a meaningful ETA
				secs := float64(resp.Total-resp.Completed) / rateEMA
				eta = "~" + humanDuration(time.Duration(secs)*time.Second)
			}

			fmt.Printf("\r\033[K  %s %3.0f%%  %s / %s  %s",
				bar, pct*100, done, total, eta)
			onProgLine = true

		default:
			// Status messages: "pulling manifest", "verifying sha256 digest", etc.
			if onProgLine {
				fmt.Println()
				onProgLine = false
			}
			fmt.Printf("  %s\n", resp.Status)
		}
		return nil
	})
}

// renderBar returns a fixed-width ASCII progress bar: [████░░░░░░░░░░░░░░░░░░░░░░░░]
func renderBar(pct float64, width int) string {
	filled := min(int(math.Round(pct*float64(width))), width)
	return "[" + strings.Repeat("█", filled) + strings.Repeat("░", width-filled) + "]"
}

// humanBytes formats a byte count as a human-readable string (B, KB, MB, GB).
func humanBytes(b int64) string {
	const (
		kb = 1 << 10
		mb = 1 << 20
		gb = 1 << 30
	)
	switch {
	case b >= gb:
		return fmt.Sprintf("%.1f GB", float64(b)/gb)
	case b >= mb:
		return fmt.Sprintf("%.0f MB", float64(b)/mb)
	case b >= kb:
		return fmt.Sprintf("%.0f KB", float64(b)/kb)
	default:
		return fmt.Sprintf("%d B", b)
	}
}

// humanDuration formats a duration as a human-readable string (e.g. "3m 42s").
func humanDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh %02dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm %02ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}
