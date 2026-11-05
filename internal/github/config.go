package github

import (
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"github.com/spf13/viper"

	"github.com/sekia-ai/sekia/internal/secrets"
)

// Config is the top-level GitHub agent configuration.
type Config struct {
	NATS     NATSConfig     `mapstructure:"nats"`
	GitHub   GitHubConfig   `mapstructure:"github"`
	Webhook  WebhookConfig  `mapstructure:"webhook"`
	Poll     PollConfig     `mapstructure:"poll"`
	Security SecurityConfig `mapstructure:"security"`
	Log      LogConfig      `mapstructure:"log"`

	// ConfigFile is the config file that was read, if any. Config reload re-reads it.
	ConfigFile string `mapstructure:"-"`
}

// LogConfig holds logging settings.
type LogConfig struct {
	Level string `mapstructure:"level"` // zerolog level name: trace, debug, info, warn, error
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
	Secret string `mapstructure:"secret"` // #nosec G117 -- config deserialization, not hardcoded
	Path   string `mapstructure:"path"`
}

// PollConfig holds GitHub REST API polling settings.
type PollConfig struct {
	Enabled       bool          `mapstructure:"enabled"`
	Interval      time.Duration `mapstructure:"interval"`
	SweepInterval time.Duration `mapstructure:"sweep_interval"` // label/PR-match modes: how often to re-emit every matching item (0 = only at startup)
	Repos         []string      `mapstructure:"repos"`
	Labels        []string      `mapstructure:"labels"`
	State         string        `mapstructure:"state"`
	MatchPRs      bool          `mapstructure:"match_prs"`
}

// LoadConfig reads configuration from file, env, and defaults.
// When instanceName is non-empty and cfgFile is empty, the config file name
// becomes "sekia-github-<instanceName>" (e.g., sekia-github-work.toml).
func LoadConfig(cfgFile, instanceName string) (Config, error) {
	v := viper.New()

	v.SetDefault("nats.url", "nats://127.0.0.1:4222")
	v.SetDefault("webhook.listen", ":8080")
	v.SetDefault("webhook.path", "/webhook")
	v.SetDefault("poll.enabled", false)
	v.SetDefault("poll.interval", "30s")
	v.SetDefault("poll.sweep_interval", "1h")
	v.SetDefault("poll.repos", []string{})
	v.SetDefault("poll.labels", []string{})
	v.SetDefault("poll.state", "open")
	v.SetDefault("poll.match_prs", false)
	v.SetDefault("log.level", "info")

	v.SetConfigType("toml")

	if cfgFile != "" {
		v.SetConfigFile(cfgFile)
	} else {
		configName := "sekia-github"
		if instanceName != "" {
			configName = "sekia-github-" + instanceName
		}
		v.SetConfigName(configName)
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
	v.BindEnv("log.level", "SEKIA_LOG_LEVEL")

	// Config file is optional.
	_ = v.ReadInConfig()

	// Resolve any KMS[...] encrypted values.
	if err := secrets.ResolveViperConfig(v); err != nil {
		return Config{}, fmt.Errorf("resolve config secrets: %w", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return cfg, err
	}
	cfg.ConfigFile = v.ConfigFileUsed()

	if cfg.GitHub.Token == "" {
		return cfg, fmt.Errorf("github.token is required (set via config file or GITHUB_TOKEN env var)")
	}

	if cfg.Poll.Enabled && len(cfg.Poll.Repos) == 0 {
		return cfg, fmt.Errorf("poll.repos is required when poll.enabled is true")
	}

	if cfg.Poll.Enabled && cfg.Poll.Interval < time.Second {
		return cfg, fmt.Errorf("poll.interval must be at least 1s")
	}

	if cfg.Poll.SweepInterval < 0 {
		return cfg, fmt.Errorf("poll.sweep_interval must not be negative")
	}

	switch cfg.Poll.State {
	case "open", "closed", "all":
		// valid
	default:
		return cfg, fmt.Errorf("poll.state must be one of: open, closed, all")
	}

	if cfg.Webhook.Listen != "" && cfg.Webhook.Secret == "" {
		return cfg, fmt.Errorf("webhook.secret is required when webhook listener is enabled (set via config file or GITHUB_WEBHOOK_SECRET env var)")
	}

	if cfg.Webhook.Listen == "" && !cfg.Poll.Enabled {
		return cfg, fmt.Errorf("at least one of webhook.listen or poll.enabled must be configured")
	}

	if _, err := zerolog.ParseLevel(cfg.Log.Level); err != nil {
		return cfg, fmt.Errorf("log.level %q is not a valid level", cfg.Log.Level)
	}

	return cfg, nil
}
