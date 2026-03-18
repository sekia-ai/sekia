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
	var instanceName string

	rootCmd := &cobra.Command{
		Use:   "sekia-linear",
		Short: "sekia Linear agent — GraphQL polling and Linear API commands",
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := zerolog.New(
				zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339},
			).With().Timestamp().Logger()

			cfg, err := linearagent.LoadConfig(cfgFile, instanceName)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			la := linearagent.NewAgent(cfg, instanceName, logger)
			return la.Run()
		},
	}

	rootCmd.Version = version
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file path")
	rootCmd.PersistentFlags().StringVar(&instanceName, "name", "", "instance name for multi-tenant setups (e.g., linear-work); changes config file and NATS registration")

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
