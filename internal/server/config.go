package server

import (
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/viper"

	"github.com/sekia-ai/sekia/internal/ai"
	"github.com/sekia-ai/sekia/pkg/sockpath"
)

// Config is the top-level daemon configuration.
type Config struct {
	Server    ServerConfig   `mapstructure:"server"`
	NATS      NATSConfig     `mapstructure:"nats"`
	Workflows WorkflowConfig `mapstructure:"workflows"`
	Web       WebConfig      `mapstructure:"web"`
	AI        ai.Config      `mapstructure:"ai"`
	Security  SecurityConfig `mapstructure:"security"`
}

// SecurityConfig holds application-level security settings.
type SecurityConfig struct {
	CommandSecret string `mapstructure:"command_secret"`
}

// WebConfig holds web dashboard settings.
type WebConfig struct {
	Listen   string `mapstructure:"listen"`
	Username string `mapstructure:"username"`
	Password string `mapstructure:"password"` // #nosec G117 -- config deserialization, not hardcoded
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

	v.SetDefault("server.listen", "127.0.0.1:7600")
	v.SetDefault("server.socket", sockpath.DefaultSocketPath())
	v.SetDefault("nats.embedded", true)

	homeDir, _ := os.UserHomeDir()
	v.SetDefault("nats.data_dir", filepath.Join(homeDir, ".local", "share", "sekia", "nats"))

	v.SetDefault("workflows.dir", filepath.Join(homeDir, ".config", "sekia", "workflows"))
	v.SetDefault("workflows.hot_reload", true)
	v.SetDefault("workflows.handler_timeout", 30*time.Second)
	v.SetDefault("workflows.verify_integrity", false)

	v.SetDefault("ai.provider", "anthropic")
	v.SetDefault("ai.model", "claude-sonnet-4-20250514")
	v.SetDefault("ai.max_tokens", 1024)
	v.SetDefault("ai.temperature", 0.0)

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
	v.BindEnv("web.username", "SEKIA_WEB_USERNAME")
	v.BindEnv("web.password", "SEKIA_WEB_PASSWORD")
	v.BindEnv("security.command_secret", "SEKIA_COMMAND_SECRET")

	// Config file is optional.
	_ = v.ReadInConfig()

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}
