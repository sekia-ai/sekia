package google

import (
	"fmt"
	"time"

	"github.com/spf13/viper"

	"github.com/sekia-ai/sekia/internal/secrets"
)

// Config holds all configuration for the Google agent.
type Config struct {
	NATS     NATSConfig     `mapstructure:"nats"`
	Google   GoogleConfig   `mapstructure:"google"`
	Gmail    GmailConfig    `mapstructure:"gmail"`
	Calendar CalendarConfig `mapstructure:"calendar"`
	Security SecurityConfig `mapstructure:"security"`
}

// NATSConfig holds NATS connection settings.
type NATSConfig struct {
	URL   string `mapstructure:"url"`
	Token string `mapstructure:"token"`
}

// GoogleConfig holds OAuth2 credentials and token path.
type GoogleConfig struct {
	ClientID     string `mapstructure:"client_id"`
	ClientSecret string `mapstructure:"client_secret"` // #nosec G117 -- config deserialization, not hardcoded
	TokenPath    string `mapstructure:"token_path"`
}

// GmailConfig holds Gmail polling settings.
type GmailConfig struct {
	Enabled      bool          `mapstructure:"enabled"`
	PollInterval time.Duration `mapstructure:"poll_interval"`
	UserID       string        `mapstructure:"user_id"`
	Query        string        `mapstructure:"query"`
	MaxMessages  int64         `mapstructure:"max_messages"`
}

// CalendarConfig holds Calendar polling settings.
type CalendarConfig struct {
	Enabled      bool          `mapstructure:"enabled"`
	PollInterval time.Duration `mapstructure:"poll_interval"`
	CalendarID   string        `mapstructure:"calendar_id"`
	UpcomingMins int           `mapstructure:"upcoming_mins"`
}

// SecurityConfig holds application-level security settings.
type SecurityConfig struct {
	CommandSecret string `mapstructure:"command_secret"`
}

// LoadConfig reads the Google agent configuration from file, env vars, and defaults.
func LoadConfig(cfgFile string) (Config, error) {
	v := viper.New()

	v.SetDefault("nats.url", "nats://127.0.0.1:4222")
	v.SetDefault("google.token_path", "~/.config/sekia/google-token.json")
	v.SetDefault("gmail.enabled", true)
	v.SetDefault("gmail.poll_interval", "30s")
	v.SetDefault("gmail.user_id", "me")
	v.SetDefault("gmail.max_messages", 20)
	v.SetDefault("calendar.enabled", false)
	v.SetDefault("calendar.poll_interval", "60s")
	v.SetDefault("calendar.calendar_id", "primary")
	v.SetDefault("calendar.upcoming_mins", 0)

	v.SetConfigType("toml")

	if cfgFile != "" {
		v.SetConfigFile(cfgFile)
	} else {
		v.SetConfigName("sekia-google")
		v.AddConfigPath("/etc/sekia")
		v.AddConfigPath("$HOME/.config/sekia")
		v.AddConfigPath(".")
	}

	v.BindEnv("google.client_id", "GOOGLE_CLIENT_ID")
	v.BindEnv("google.client_secret", "GOOGLE_CLIENT_SECRET")
	v.BindEnv("google.token_path", "GOOGLE_TOKEN_PATH")
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

	return cfg, nil
}

// ValidateForRun checks that the config is valid for running the agent.
func ValidateForRun(cfg Config) error {
	if cfg.Google.ClientID == "" {
		return fmt.Errorf("google.client_id is required (set via config file or GOOGLE_CLIENT_ID env var)")
	}
	if cfg.Google.ClientSecret == "" {
		return fmt.Errorf("google.client_secret is required (set via config file or GOOGLE_CLIENT_SECRET env var)")
	}
	if !cfg.Gmail.Enabled && !cfg.Calendar.Enabled {
		return fmt.Errorf("at least one of gmail.enabled or calendar.enabled must be true")
	}
	return nil
}

// ValidateForAuth checks that the config is valid for the auth subcommand.
func ValidateForAuth(cfg Config) error {
	if cfg.Google.ClientID == "" {
		return fmt.Errorf("google.client_id is required (set via config file or GOOGLE_CLIENT_ID env var)")
	}
	if cfg.Google.ClientSecret == "" {
		return fmt.Errorf("google.client_secret is required (set via config file or GOOGLE_CLIENT_SECRET env var)")
	}
	return nil
}
