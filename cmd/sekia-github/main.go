package main

import (
	"fmt"
	"os"
	"time"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	ghagent "github.com/sekia-ai/sekia/internal/github"
)

func main() {
	var cfgFile string

	rootCmd := &cobra.Command{
		Use:   "sekia-github",
		Short: "Sekia GitHub agent — webhook ingestion and GitHub API commands",
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := zerolog.New(
				zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339},
			).With().Timestamp().Logger()

			cfg, err := ghagent.LoadConfig(cfgFile)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			ga := ghagent.NewAgent(cfg, logger)
			return ga.Run()
		},
	}

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file path")

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
