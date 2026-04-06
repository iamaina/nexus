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
