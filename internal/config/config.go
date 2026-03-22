package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Providers        []ProviderConfig `yaml:"providers"`
	CooldownSeconds  int              `yaml:"cooldown_seconds"`
	MaxContextTokens int              `yaml:"max_context_tokens"`
}

type ProviderConfig struct {
	Name      string `yaml:"name"`
	APIKeyEnv string `yaml:"api_key_env,omitempty"` // deprecated, backward compat
	Model     string `yaml:"model"`
	Priority  int    `yaml:"priority"`
	BaseURL   string `yaml:"base_url,omitempty"` // for OpenAI-compatible providers
}

func DefaultConfig() Config {
	return Config{
		Providers: []ProviderConfig{
			{
				Name:      "anthropic",
				APIKeyEnv: "ANTHROPIC_API_KEY",
				Model:     "claude-opus-4-6",
				Priority:  1,
			},
		},
		CooldownSeconds:  30,
		MaxContextTokens: 180000,
	}
}

func Load() (Config, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return DefaultConfig(), nil
	}

	path := filepath.Join(configDir, "ernest", "config.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return DefaultConfig(), nil
	}

	cfg := DefaultConfig()
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return DefaultConfig(), err
	}

	return cfg, nil
}

// yamlUnmarshalConfig is a testable helper for YAML parsing.
func yamlUnmarshalConfig(data []byte, cfg *Config) error {
	return yaml.Unmarshal(data, cfg)
}

// PrimaryProvider returns the highest-priority provider config.
func (c Config) PrimaryProvider() ProviderConfig {
	if len(c.Providers) == 0 {
		return DefaultConfig().Providers[0]
	}

	best := c.Providers[0]
	for _, p := range c.Providers[1:] {
		if p.Priority < best.Priority {
			best = p
		}
	}
	return best
}
