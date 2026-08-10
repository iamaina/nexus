// Package live executes registered shell commands and returns their output
// for injection into the query prompt at query time.
package live

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/iamaina/nexus/internal/models"
)

// Output holds the result of running a single context source command.
type Output struct {
	Name    string
	Command string
	Text    string
	Err     error
}

// FilterByQuery returns the subset of sources that should be injected for the
// given query. Sources with no tags are always included. Sources with tags are
// included only when the query contains at least one matching tag (case-insensitive
// whole-word substring). This keeps unrelated live context out of the prompt
// without requiring --no-live for every personal or off-topic question.
func FilterByQuery(query string, sources []models.ContextSource) []models.ContextSource {
	lower := strings.ToLower(query)
	var result []models.ContextSource
	for _, s := range sources {
		if s.Tags == "" {
			result = append(result, s)
			continue
		}
		for _, tag := range strings.Split(s.Tags, ",") {
			tag = strings.TrimSpace(strings.ToLower(tag))
			if tag != "" && strings.Contains(lower, tag) {
				result = append(result, s)
				break
			}
		}
	}
	return result
}

// RunAll executes every source concurrently with the given per-command timeout.
// It always returns a result for every source — errors are captured in Output.Err
// so the caller can decide how to handle partial failures.
func RunAll(ctx context.Context, sources []models.ContextSource, timeout time.Duration) []Output {
	if len(sources) == 0 {
		return nil
	}

	results := make([]Output, len(sources))
	done := make(chan struct{}, len(sources))

	for i, src := range sources {
		i, src := i, src
		go func() {
			results[i] = run(ctx, src, timeout)
			done <- struct{}{}
		}()
	}

	for range sources {
		<-done
	}
	return results
}

func run(ctx context.Context, src models.ContextSource, timeout time.Duration) Output {
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Run via shell so the command string can include pipes, flags, etc.
	// The command is stored in the DB by the user themselves — this is intentional.
	cmd := exec.CommandContext(cmdCtx, "sh", "-c", src.Command) //nolint:gosec
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return Output{
			Name:    src.Name,
			Command: src.Command,
			Err:     fmt.Errorf("%s", detail),
		}
	}

	return Output{
		Name:    src.Name,
		Command: src.Command,
		Text:    strings.TrimRight(stdout.String(), "\n"),
	}
}
