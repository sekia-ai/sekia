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
	var instanceName string

	rootCmd := &cobra.Command{
		Use:   "sekia-slack",
		Short: "sekia Slack agent — Socket Mode event ingestion and Slack API commands",
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := zerolog.New(
				zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339},
			).With().Timestamp().Logger()

			cfg, err := slackagent.LoadConfig(cfgFile, instanceName)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			sa := slackagent.NewAgent(cfg, instanceName, logger)
			return sa.Run()
		},
	}

	rootCmd.Version = version
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file path")
	rootCmd.PersistentFlags().StringVar(&instanceName, "name", "", "instance name for multi-tenant setups (e.g., slack-work); changes config file and NATS registration")

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
