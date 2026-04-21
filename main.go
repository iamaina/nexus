// Package main is the entry point for the nexus CLI.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"syscall"

	"github.com/iamaina/nexus/cmd/nexus"
	"github.com/iamaina/nexus/internal/app"
	"github.com/iamaina/nexus/internal/logger"
	"github.com/spf13/cobra"
)

func main() {
	// Forward the build-time version into the logger.
	logger.Version = nexus.Version

	// Skip DB/Ollama init for commands that don't need the app.
	// setup-reconfigure loads config directly and needs no DB or Ollama connection.
	skipInit := len(os.Args) > 1 && (os.Args[1] == "completion" || os.Args[1] == "help" || os.Args[1] == "setup-reconfigure") ||
		func() bool {
			for _, arg := range os.Args[1:] {
				if arg == "--version" || arg == "-V" || arg == "--help" || arg == "-h" {
					return true
				}
			}
			return false
		}()

	// Pre-scan for --verbose/-v so app.New() receives the flag before the logger is initialised.
	verboseFlag := false
	for _, arg := range os.Args[1:] {
		if arg == "--verbose" || arg == "-v" {
			verboseFlag = true
			break
		}
	}

	// Write PID file — lets you trace or signal the process easily:
	//   kill $(cat ~/.config/nexus/nexus.pid)
	// Removed on clean exit. Survives kill -9; stale file is overwritten on next start.
	pidPath := writePIDFile()
	defer removePIDFile(pidPath)

	// Catch panics and redirect to graceful shutdown.
	// The Go runtime handles SIGSEGV/SIGBUS/SIGFPE (hardware faults) itself —
	// those are unrecoverable. This defer catches application-level panics only.
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "\nnexus [pid %d]: unexpected panic: %v\n", os.Getpid(), r)
			debug.PrintStack()
			removePIDFile(pidPath)
			os.Exit(2)
		}
	}()

	// Signal context — cancels on any of these signals, triggering graceful
	// shutdown of all in-flight DB queries, Ollama calls, and embeddings.
	// SIGKILL (kill -9) cannot be intercepted; the OS terminates immediately.
	ctx, stop := signal.NotifyContext(context.Background(),
		os.Interrupt,    // SIGINT  (2)  — Ctrl+C
		syscall.SIGTERM, // SIGTERM (15) — kill <pid>
		syscall.SIGHUP,  // SIGHUP  (1)  — terminal hangup / SSH disconnect
		syscall.SIGQUIT, // SIGQUIT (3)  — Ctrl+\
	)
	defer stop()

	var a *app.Application
	if !skipInit {
		var err error
		a, err = app.New(ctx, verboseFlag)
		if err != nil {
			logger.Error(ctx, "App initialization failed", slog.Any("err", err))
			os.Exit(1)
		}
		defer a.Close()
	}

	nexus.RootCmd.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		if a != nil {
			cmd.SetContext(context.WithValue(cmd.Context(), app.AppKey, a))
		}
		return nil
	}

	// Wire the signal context into cobra so every cmd.Context() is cancellable.
	nexus.RootCmd.SetContext(ctx)
	nexus.Execute()
}

// writePIDFile writes the current process ID to ~/.config/nexus/nexus.pid.
// Returns the path so the caller can defer removePIDFile. Silently no-ops on error.
func writePIDFile() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	dir := filepath.Join(home, ".config", "nexus")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return ""
	}
	path := filepath.Join(dir, "nexus.pid")
	_ = os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600)
	return path
}

// removePIDFile deletes the PID file on clean exit.
// Stale files from kill -9 are overwritten on the next start.
func removePIDFile(path string) {
	if path != "" {
		_ = os.Remove(path)
	}
}
