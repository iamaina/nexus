// Package main is the entry point for the nexus CLI.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/iamaina/nexus/cmd/nexus"
	"github.com/iamaina/nexus/internal/app"
	"github.com/iamaina/nexus/internal/logger"
	"github.com/spf13/cobra"
)

func main() {
	a, err := app.New()
	if err != nil {
		logger.Error(context.Background(), "App initialization failed", slog.Any("err", err))
		os.Exit(1)
	}
	defer a.Close()

	nexus.RootCmd.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		cmd.SetContext(context.WithValue(cmd.Context(), app.AppKey, a))
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
