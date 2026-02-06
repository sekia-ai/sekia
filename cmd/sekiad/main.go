package main

import (
	"fmt"
	"os"
	"time"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	"github.com/sekia-ai/sekia/internal/server"
)

func main() {
	var cfgFile string

	rootCmd := &cobra.Command{
		Use:   "sekiad",
		Short: "Sekia daemon — multi-agent event bus",
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := zerolog.New(
				zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339},
			).With().Timestamp().Logger()

			cfg, err := server.LoadConfig(cfgFile)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			d := server.NewDaemon(cfg, logger)
			return d.Run()
		},
	}

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file path")

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
