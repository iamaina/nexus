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
	services, err := app.New()
	if err != nil {
		logger.Error(context.Background(), "App initialization failed", slog.Any("err", err))
		os.Exit(1)
	}
	defer services.Close()

	nexus.RootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		// Store in cmd context so subcommands can access it
		cmd.SetContext(context.WithValue(cmd.Context(), "services", services))
		return nil
	}

	// Graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		services.Close()
		os.Exit(0)
	}()
	nexus.Execute()
}
