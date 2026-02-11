package github

import (
	"fmt"
	"time"

	"github.com/spf13/viper"
)

// Config is the top-level GitHub agent configuration.
type Config struct {
	NATS     NATSConfig     `mapstructure:"nats"`
	GitHub   GitHubConfig   `mapstructure:"github"`
	Webhook  WebhookConfig  `mapstructure:"webhook"`
	Poll     PollConfig     `mapstructure:"poll"`
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

// PollConfig holds GitHub REST API polling settings.
type PollConfig struct {
	Enabled  bool          `mapstructure:"enabled"`
	Interval time.Duration `mapstructure:"interval"`
	Repos    []string      `mapstructure:"repos"`
	PerTick  int           `mapstructure:"per_tick"`
	Labels   []string      `mapstructure:"labels"`
	State    string        `mapstructure:"state"`
}

// LoadConfig reads configuration from file, env, and defaults.
func LoadConfig(cfgFile string) (Config, error) {
	v := viper.New()

	v.SetDefault("nats.url", "nats://127.0.0.1:4222")
	v.SetDefault("webhook.listen", ":8080")
	v.SetDefault("webhook.path", "/webhook")
	v.SetDefault("poll.enabled", false)
	v.SetDefault("poll.interval", "30s")
	v.SetDefault("poll.repos", []string{})
	v.SetDefault("poll.per_tick", 100)
	v.SetDefault("poll.labels", []string{})
	v.SetDefault("poll.state", "open")

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
	v.BindEnv("nats.token", "SEKIA_NATS_TOKEN")
	v.BindEnv("security.command_secret", "SEKIA_COMMAND_SECRET")

	// Config file is optional.
	_ = v.ReadInConfig()

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return cfg, err
	}

	if cfg.GitHub.Token == "" {
		return cfg, fmt.Errorf("github.token is required (set via config file or GITHUB_TOKEN env var)")
	}

	if cfg.Poll.Enabled && len(cfg.Poll.Repos) == 0 {
		return cfg, fmt.Errorf("poll.repos is required when poll.enabled is true")
	}

	if cfg.Poll.Enabled && cfg.Poll.PerTick < 1 {
		return cfg, fmt.Errorf("poll.per_tick must be at least 1")
	}

	switch cfg.Poll.State {
	case "open", "closed", "all":
		// valid
	default:
		return cfg, fmt.Errorf("poll.state must be one of: open, closed, all")
	}

	if cfg.Webhook.Listen == "" && !cfg.Poll.Enabled {
		return cfg, fmt.Errorf("at least one of webhook.listen or poll.enabled must be configured")
	}

	return cfg, nil
}
