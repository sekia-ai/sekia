package linear

import (
	"fmt"
	"time"

	"github.com/spf13/viper"

	"github.com/sekia-ai/sekia/internal/secrets"
)

// Config holds all configuration for the Linear agent.
type Config struct {
	NATS     NATSConfig     `mapstructure:"nats"`
	Linear   LinearConfig   `mapstructure:"linear"`
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

// LinearConfig holds Linear API credentials.
type LinearConfig struct {
	APIKey string `mapstructure:"api_key"` // #nosec G117 -- config deserialization, not hardcoded
}

// PollConfig holds polling settings.
type PollConfig struct {
	Interval   time.Duration `mapstructure:"interval"`
	TeamFilter string        `mapstructure:"team_filter"`
}

// LoadConfig reads the Linear agent configuration from file, env vars, and defaults.
func LoadConfig(cfgFile string) (Config, error) {
	v := viper.New()

	v.SetDefault("nats.url", "nats://127.0.0.1:4222")
	v.SetDefault("poll.interval", "30s")
	v.SetDefault("poll.team_filter", "")

	v.SetConfigType("toml")

	if cfgFile != "" {
		v.SetConfigFile(cfgFile)
	} else {
		v.SetConfigName("sekia-linear")
		v.AddConfigPath("/etc/sekia")
		v.AddConfigPath("$HOME/.config/sekia")
		v.AddConfigPath(".")
	}

	v.BindEnv("linear.api_key", "LINEAR_API_KEY")
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

	if cfg.Linear.APIKey == "" {
		return cfg, fmt.Errorf("linear.api_key is required (set via config file or LINEAR_API_KEY env var)")
	}

	return cfg, nil
}
