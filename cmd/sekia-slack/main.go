package main

import (
	"fmt"
	"os"
	"time"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	slackagent "github.com/sekia-ai/sekia/internal/slack"
)

var version = "dev"

func main() {
	var cfgFile string

	rootCmd := &cobra.Command{
		Use:   "sekia-slack",
		Short: "sekia Slack agent — Socket Mode event ingestion and Slack API commands",
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := zerolog.New(
				zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339},
			).With().Timestamp().Logger()

			cfg, err := slackagent.LoadConfig(cfgFile)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			sa := slackagent.NewAgent(cfg, logger)
			return sa.Run()
		},
	}

	rootCmd.Version = version
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file path")

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
