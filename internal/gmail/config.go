package gmail

import (
	"fmt"
	"time"

	"github.com/spf13/viper"
)

// Config holds all configuration for the Gmail agent.
type Config struct {
	NATS     NATSConfig     `mapstructure:"nats"`
	IMAP     IMAPConfig     `mapstructure:"imap"`
	SMTP     SMTPConfig     `mapstructure:"smtp"`
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

// IMAPConfig holds IMAP connection credentials.
type IMAPConfig struct {
	Server   string `mapstructure:"server"`
	Username string `mapstructure:"username"`
	Password string `mapstructure:"password"`
}

// SMTPConfig holds SMTP connection credentials.
type SMTPConfig struct {
	Server   string `mapstructure:"server"`
	Username string `mapstructure:"username"`
	Password string `mapstructure:"password"`
}

// PollConfig holds polling settings.
type PollConfig struct {
	Interval time.Duration `mapstructure:"interval"`
	Folder   string        `mapstructure:"folder"`
}

// LoadConfig reads the Gmail agent configuration from file, env vars, and defaults.
func LoadConfig(cfgFile string) (Config, error) {
	v := viper.New()

	v.SetDefault("nats.url", "nats://127.0.0.1:4222")
	v.SetDefault("imap.server", "imap.gmail.com:993")
	v.SetDefault("smtp.server", "smtp.gmail.com:587")
	v.SetDefault("poll.interval", "60s")
	v.SetDefault("poll.folder", "INBOX")

	v.SetConfigType("toml")

	if cfgFile != "" {
		v.SetConfigFile(cfgFile)
	} else {
		v.SetConfigName("sekia-gmail")
		v.AddConfigPath("/etc/sekia")
		v.AddConfigPath("$HOME/.config/sekia")
		v.AddConfigPath(".")
	}

	v.BindEnv("imap.username", "GMAIL_ADDRESS")
	v.BindEnv("imap.password", "GMAIL_APP_PASSWORD")
	v.BindEnv("smtp.username", "GMAIL_ADDRESS")
	v.BindEnv("smtp.password", "GMAIL_APP_PASSWORD")
	v.BindEnv("nats.url", "SEKIA_NATS_URL")
	v.BindEnv("nats.token", "SEKIA_NATS_TOKEN")
	v.BindEnv("security.command_secret", "SEKIA_COMMAND_SECRET")

	_ = v.ReadInConfig() // config file is optional

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return cfg, err
	}

	if cfg.IMAP.Username == "" {
		return cfg, fmt.Errorf("imap.username is required (set via config file or GMAIL_ADDRESS env var)")
	}
	if cfg.IMAP.Password == "" {
		return cfg, fmt.Errorf("imap.password is required (set via config file or GMAIL_APP_PASSWORD env var)")
	}

	// Default SMTP credentials to IMAP credentials if not set.
	if cfg.SMTP.Username == "" {
		cfg.SMTP.Username = cfg.IMAP.Username
	}
	if cfg.SMTP.Password == "" {
		cfg.SMTP.Password = cfg.IMAP.Password
	}

	return cfg, nil
}
