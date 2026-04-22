// Package main is the entry point for the nexus CLI.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"fmt"

	"github.com/iamaina/nexus/cmd/nexus"
	"github.com/iamaina/nexus/internal/app"
	"github.com/iamaina/nexus/internal/logger"
	"github.com/spf13/cobra"
)

func main() {
	fmt.Println("Starting Nexus...")
	// Forward the build-time version into the logger so JSON logs carry the correct version.
	logger.Version = nexus.Version

	// Skip DB/Ollama init for commands that don't need the app.
	// -V = --version (short), -v = --verbose (does need the app).
	skipInit := len(os.Args) > 1 && (os.Args[1] == "completion" || os.Args[1] == "help") ||
		func() bool {
			for _, arg := range os.Args[1:] {
				if arg == "--version" || arg == "-V" || arg == "--help" || arg == "-h" {
					return true
				}
			}
			return false
		}()

	// Pre-scan for --verbose/-v so app.New() receives the flag before initialising the logger.
	verboseFlag := false
	for _, arg := range os.Args[1:] {
		if arg == "--verbose" || arg == "-v" {
			verboseFlag = true
			break
		}
	}

	var a *app.Application
	if !skipInit {
		var err error
		a, err = app.New(verboseFlag)
		if err != nil {
			logger.Error(context.Background(), "App initialization failed", slog.Any("err", err))
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

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		a.Close()
		os.Exit(0)
	}()

	nexus.Execute()
}
