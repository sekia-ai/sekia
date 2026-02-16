package ai

// Config holds AI/LLM settings from the [ai] section of sekia.toml.
type Config struct {
	Provider     string  `mapstructure:"provider"`
	APIKey       string  `mapstructure:"api_key"` // #nosec G117 -- config deserialization, not hardcoded
	Model        string  `mapstructure:"model"`
	MaxTokens    int     `mapstructure:"max_tokens"`
	Temperature  float64 `mapstructure:"temperature"`
	SystemPrompt string  `mapstructure:"system_prompt"`
}
