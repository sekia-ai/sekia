package main

import (
	"fmt"
	"os"
	"time"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	"github.com/sekia-ai/sekia/internal/server"
)

var version = "dev"

func main() {
	var cfgFile string
	var logLevel string

	rootCmd := &cobra.Command{
		Use:   "sekiad",
		Short: "sekia daemon — multi-agent event bus",
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := zerolog.New(
				zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339},
			).With().Timestamp().Logger()

			cfg, err := server.LoadConfig(cfgFile)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			levelName := cfg.Log.Level
			if logLevel != "" {
				levelName = logLevel
			}
			level, err := zerolog.ParseLevel(levelName)
			if err != nil {
				return fmt.Errorf("invalid log level %q", levelName)
			}
			logger = logger.Level(level)

			d := server.NewDaemon(cfg, logger)
			return d.Run()
		},
	}

	rootCmd.Version = version
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file path")
	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "", "log level: trace, debug, info, warn, error (default: log.level from config, else info)")

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
