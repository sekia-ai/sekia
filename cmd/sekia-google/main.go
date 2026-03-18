package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	googleagent "github.com/sekia-ai/sekia/internal/google"
)

var version = "dev"

func main() {
	var cfgFile string
	var instanceName string

	rootCmd := &cobra.Command{
		Use:   "sekia-google",
		Short: "sekia Google agent — Gmail and Calendar via REST API",
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := zerolog.New(
				zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339},
			).With().Timestamp().Logger()

			cfg, err := googleagent.LoadConfig(cfgFile, instanceName)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			if err := googleagent.ValidateForRun(cfg); err != nil {
				return fmt.Errorf("invalid config: %w", err)
			}

			ga, err := googleagent.NewAgent(cfg, instanceName, logger)
			if err != nil {
				return fmt.Errorf("create agent: %w", err)
			}

			return ga.Run()
		},
	}

	authCmd := &cobra.Command{
		Use:   "auth",
		Short: "Authorize sekia-google with your Google account",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := googleagent.LoadConfig(cfgFile, instanceName)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			if err := googleagent.ValidateForAuth(cfg); err != nil {
				return fmt.Errorf("invalid config: %w", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()

			token, err := googleagent.AuthFlow(ctx, cfg.Google.ClientID, cfg.Google.ClientSecret, cfg.Google.AuthPort)
			if err != nil {
				return fmt.Errorf("authorization failed: %w", err)
			}

			if err := googleagent.SaveTokenToFile(cfg.Google.TokenPath, token); err != nil {
				return fmt.Errorf("save token: %w", err)
			}

			fmt.Printf("\nAuthorization successful! Token saved to %s\n", cfg.Google.TokenPath)
			return nil
		},
	}

	rootCmd.AddCommand(authCmd)
	rootCmd.Version = version
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file path")
	rootCmd.PersistentFlags().StringVar(&instanceName, "name", "", "instance name for multi-tenant setups (e.g., google-work); changes config file and NATS registration")

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
