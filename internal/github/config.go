package github

import (
	"fmt"

	"github.com/spf13/viper"
)

// Config is the top-level GitHub agent configuration.
type Config struct {
	NATS    NATSConfig    `mapstructure:"nats"`
	GitHub  GitHubConfig  `mapstructure:"github"`
	Webhook WebhookConfig `mapstructure:"webhook"`
}

// NATSConfig holds NATS connection settings.
type NATSConfig struct {
	URL string `mapstructure:"url"`
}

// GitHubConfig holds GitHub API settings.
type GitHubConfig struct {
	Token string `mapstructure:"token"`
}

// WebhookConfig holds webhook HTTP server settings.
type WebhookConfig struct {
	Listen string `mapstructure:"listen"`
	Secret string `mapstructure:"secret"`
	Path   string `mapstructure:"path"`
}

// LoadConfig reads configuration from file, env, and defaults.
func LoadConfig(cfgFile string) (Config, error) {
	v := viper.New()

	v.SetDefault("nats.url", "nats://127.0.0.1:4222")
	v.SetDefault("webhook.listen", ":8080")
	v.SetDefault("webhook.path", "/webhook")

	v.SetConfigType("toml")

	if cfgFile != "" {
		v.SetConfigFile(cfgFile)
	} else {
		v.SetConfigName("sekia-github")
		v.AddConfigPath("/etc/sekia")
		v.AddConfigPath("$HOME/.config/sekia")
		v.AddConfigPath(".")
	}

	// Bind env vars with ergonomic names.
	v.BindEnv("github.token", "GITHUB_TOKEN")
	v.BindEnv("webhook.secret", "GITHUB_WEBHOOK_SECRET")
	v.BindEnv("nats.url", "SEKIA_NATS_URL")

	// Config file is optional.
	_ = v.ReadInConfig()

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return cfg, err
	}

	if cfg.GitHub.Token == "" {
		return cfg, fmt.Errorf("github.token is required (set via config file or GITHUB_TOKEN env var)")
	}

	return cfg, nil
}
