package main

import (
	"fmt"
	"os"
	"time"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	gmailagent "github.com/sekia-ai/sekia/internal/gmail"
)

var version = "dev"

func main() {
	var cfgFile string

	rootCmd := &cobra.Command{
		Use:        "sekia-gmail",
		Short:      "sekia Gmail agent — IMAP polling and email commands",
		Deprecated: "use sekia-google instead (OAuth2 + Gmail REST API + Google Calendar)",
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := zerolog.New(
				zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339},
			).With().Timestamp().Logger()

			logger.Warn().Msg("sekia-gmail is deprecated; use sekia-google instead")

			cfg, err := gmailagent.LoadConfig(cfgFile)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			ga := gmailagent.NewAgent(cfg, logger)
			return ga.Run()
		},
	}

	rootCmd.Version = version
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file path")

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
