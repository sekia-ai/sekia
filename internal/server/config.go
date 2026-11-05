package server

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/viper"

	"github.com/sekia-ai/sekia/internal/secrets"
	"github.com/sekia-ai/sekia/pkg/sockpath"
)

// Config is the top-level daemon configuration.
type Config struct {
	Server    ServerConfig   `mapstructure:"server"`
	NATS      NATSConfig     `mapstructure:"nats"`
	Workflows WorkflowConfig `mapstructure:"workflows"`
	Security  SecurityConfig `mapstructure:"security"`
}

// SecurityConfig holds application-level security settings.
type SecurityConfig struct {
	CommandSecret string `mapstructure:"command_secret"`
}

// ServerConfig holds the control API socket settings.
type ServerConfig struct {
	Socket string `mapstructure:"socket"`
}

// NATSConfig holds embedded NATS settings.
type NATSConfig struct {
	Embedded bool   `mapstructure:"embedded"`
	DataDir  string `mapstructure:"data_dir"`
	Host     string `mapstructure:"host"`
	Port     int    `mapstructure:"port"`
	Token    string `mapstructure:"token"`
}

// WorkflowConfig holds Lua workflow engine settings.
type WorkflowConfig struct {
	Dir             string        `mapstructure:"dir"`
	HotReload       bool          `mapstructure:"hot_reload"`
	HandlerTimeout  time.Duration `mapstructure:"handler_timeout"`
	VerifyIntegrity bool          `mapstructure:"verify_integrity"`
}

// LoadConfig reads configuration from file, env, and flags.
func LoadConfig(cfgFile string) (Config, error) {
	v := viper.New()

	v.SetDefault("server.socket", sockpath.DefaultSocketPath())
	v.SetDefault("nats.embedded", true)

	homeDir, _ := os.UserHomeDir()
	configDir := filepath.Join(homeDir, ".config", "sekia")

	v.SetDefault("nats.data_dir", filepath.Join(homeDir, ".local", "share", "sekia", "nats"))

	v.SetDefault("workflows.dir", filepath.Join(configDir, "workflows"))
	v.SetDefault("workflows.hot_reload", true)
	v.SetDefault("workflows.handler_timeout", 30*time.Second)
	v.SetDefault("workflows.verify_integrity", false)

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

	v.BindEnv("nats.token", "SEKIA_NATS_TOKEN")
	v.BindEnv("security.command_secret", "SEKIA_COMMAND_SECRET")

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
	return cfg, nil
}
