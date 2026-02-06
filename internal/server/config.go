package server

import (
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

// Config is the top-level daemon configuration.
type Config struct {
	Server ServerConfig `mapstructure:"server"`
	NATS   NATSConfig   `mapstructure:"nats"`
}

// ServerConfig holds HTTP/socket settings.
type ServerConfig struct {
	Listen string `mapstructure:"listen"`
	Socket string `mapstructure:"socket"`
}

// NATSConfig holds embedded NATS settings.
type NATSConfig struct {
	Embedded bool   `mapstructure:"embedded"`
	DataDir  string `mapstructure:"data_dir"`
}

// LoadConfig reads configuration from file, env, and flags.
func LoadConfig(cfgFile string) (Config, error) {
	v := viper.New()

	v.SetDefault("server.listen", "127.0.0.1:7600")
	v.SetDefault("server.socket", "/tmp/sekiad.sock")
	v.SetDefault("nats.embedded", true)

	homeDir, _ := os.UserHomeDir()
	v.SetDefault("nats.data_dir", filepath.Join(homeDir, ".local", "share", "sekia", "nats"))

	v.SetConfigType("toml")

	if cfgFile != "" {
		v.SetConfigFile(cfgFile)
	} else {
		v.SetConfigName("sekia")
		v.AddConfigPath("/etc/sekia")
		v.AddConfigPath("$HOME/.config/sekia")
		v.AddConfigPath(".")
	}

	v.SetEnvPrefix("SEKIA")
	v.AutomaticEnv()

	// Config file is optional.
	_ = v.ReadInConfig()

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}
