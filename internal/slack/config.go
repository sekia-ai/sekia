package slack

import (
	"fmt"

	"github.com/spf13/viper"

	"github.com/sekia-ai/sekia/internal/secrets"
)

// Config holds all configuration for the Slack agent.
type Config struct {
	NATS     NATSConfig     `mapstructure:"nats"`
	Slack    SlackConfig    `mapstructure:"slack"`
	Security SecurityConfig `mapstructure:"security"`
}

// SecurityConfig holds application-level security settings.
type SecurityConfig struct {
	CommandSecret string `mapstructure:"command_secret"`
}

// NATSConfig holds NATS connection settings.
type NATSConfig struct {
	URL   string `mapstructure:"url"`
	Token string `mapstructure:"token"`
}

// SlackConfig holds Slack API credentials.
type SlackConfig struct {
	BotToken string `mapstructure:"bot_token"`
	AppToken string `mapstructure:"app_token"`
}

// LoadConfig reads the Slack agent configuration from file, env vars, and defaults.
func LoadConfig(cfgFile string) (Config, error) {
	v := viper.New()

	v.SetDefault("nats.url", "nats://127.0.0.1:4222")

	v.SetConfigType("toml")

	if cfgFile != "" {
		v.SetConfigFile(cfgFile)
	} else {
		v.SetConfigName("sekia-slack")
		v.AddConfigPath("/etc/sekia")
		v.AddConfigPath("$HOME/.config/sekia")
		v.AddConfigPath(".")
	}

	v.BindEnv("slack.bot_token", "SLACK_BOT_TOKEN")
	v.BindEnv("slack.app_token", "SLACK_APP_TOKEN")
	v.BindEnv("nats.url", "SEKIA_NATS_URL")
	v.BindEnv("nats.token", "SEKIA_NATS_TOKEN")
	v.BindEnv("security.command_secret", "SEKIA_COMMAND_SECRET")

	_ = v.ReadInConfig() // config file is optional

	// Decrypt any ENC[...] values in config.
	identities, err := secrets.ResolveIdentity(v)
	if err != nil {
		return Config{}, fmt.Errorf("resolve encryption identity: %w", err)
	}
	if identities != nil {
		if err := secrets.DecryptViperConfig(v, identities); err != nil {
			return Config{}, fmt.Errorf("decrypt config: %w", err)
		}
	} else if secrets.HasEncryptedValues(v) {
		return Config{}, fmt.Errorf("config contains encrypted values but no age identity is configured; set SEKIA_AGE_KEY, SEKIA_AGE_KEY_FILE, or secrets.identity")
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return cfg, err
	}

	if cfg.Slack.BotToken == "" {
		return cfg, fmt.Errorf("slack.bot_token is required (set via config file or SLACK_BOT_TOKEN env var)")
	}
	if cfg.Slack.AppToken == "" {
		return cfg, fmt.Errorf("slack.app_token is required (set via config file or SLACK_APP_TOKEN env var)")
	}

	return cfg, nil
}
