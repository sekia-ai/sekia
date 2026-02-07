package main

import (
	"fmt"
	"os"
	"time"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	linearagent "github.com/sekia-ai/sekia/internal/linear"
)

var version = "dev"

func main() {
	var cfgFile string

	rootCmd := &cobra.Command{
		Use:   "sekia-linear",
		Short: "Sekia Linear agent — GraphQL polling and Linear API commands",
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := zerolog.New(
				zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339},
			).With().Timestamp().Logger()

			cfg, err := linearagent.LoadConfig(cfgFile)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			la := linearagent.NewAgent(cfg, logger)
			return la.Run()
		},
	}

	rootCmd.Version = version
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file path")

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
