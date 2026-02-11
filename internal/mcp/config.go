package mcp

import (
	"github.com/spf13/viper"
)

// Config holds all configuration for the MCP server.
type Config struct {
	NATS   NATSConfig   `mapstructure:"nats"`
	Daemon DaemonConfig `mapstructure:"daemon"`
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
	v.SetDefault("daemon.socket", "/tmp/sekiad.sock")

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

	_ = v.ReadInConfig() // config file is optional

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}
