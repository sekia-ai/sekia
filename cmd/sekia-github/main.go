package main

import (
	"fmt"
	"os"
	"time"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	ghagent "github.com/sekia-ai/sekia/internal/github"
)

var version = "dev"

func main() {
	var cfgFile string
	var instanceName string

	rootCmd := &cobra.Command{
		Use:   "sekia-github",
		Short: "sekia GitHub agent — webhook ingestion and GitHub API commands",
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := zerolog.New(
				zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339},
			).With().Timestamp().Logger()

			cfg, err := ghagent.LoadConfig(cfgFile, instanceName)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			ga := ghagent.NewAgent(cfg, instanceName, logger)
			return ga.Run()
		},
	}

	rootCmd.Version = version
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file path")
	rootCmd.PersistentFlags().StringVar(&instanceName, "name", "", "instance name for multi-tenant setups (e.g., github-work); changes config file and NATS registration")

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
