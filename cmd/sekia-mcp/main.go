package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	mcpserver "github.com/sekia-ai/sekia/internal/mcp"
)

var version = "dev"

func main() {
	var cfgFile string

	rootCmd := &cobra.Command{
		Use:   "sekia-mcp",
		Short: "Sekia MCP server — expose Sekia capabilities to AI assistants",
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := zerolog.New(
				zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339},
			).With().Timestamp().Logger()

			cfg, err := mcpserver.LoadConfig(cfgFile)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()

			s := mcpserver.New(cfg, logger)
			return s.Run(ctx, cfg.NATS.URL)
		},
	}

	rootCmd.Version = version
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file path")

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
