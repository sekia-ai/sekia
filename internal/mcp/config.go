package mcp

import (
	"fmt"

	"github.com/spf13/viper"

	"github.com/sekia-ai/sekia/internal/secrets"
	"github.com/sekia-ai/sekia/pkg/sockpath"
)

// Config holds all configuration for the MCP server.
type Config struct {
	NATS     NATSConfig     `mapstructure:"nats"`
	Daemon   DaemonConfig   `mapstructure:"daemon"`
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

// DaemonConfig holds settings for connecting to the sekiad daemon API.
type DaemonConfig struct {
	Socket string `mapstructure:"socket"`
}

// LoadConfig reads the MCP server configuration from file, env vars, and defaults.
func LoadConfig(cfgFile string) (Config, error) {
	v := viper.New()

	v.SetDefault("nats.url", "nats://127.0.0.1:4222")
	v.SetDefault("daemon.socket", sockpath.DefaultSocketPath())

	v.SetConfigType("toml")

	if cfgFile != "" {
		v.SetConfigFile(cfgFile)
	} else {
		v.SetConfigName("sekia-mcp")
		v.AddConfigPath("/etc/sekia")
		v.AddConfigPath("$HOME/.config/sekia")
		v.AddConfigPath(".")
	}

	v.BindEnv("nats.url", "SEKIA_NATS_URL")
	v.BindEnv("nats.token", "SEKIA_NATS_TOKEN")
	v.BindEnv("daemon.socket", "SEKIA_DAEMON_SOCKET")
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
