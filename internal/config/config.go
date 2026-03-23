package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

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

// SaveConfig writes the config to ~/.config/ernest/config.yaml.
func SaveConfig(cfg Config) error {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return fmt.Errorf("cannot determine config directory: %w", err)
	}

	dir := filepath.Join(configDir, "ernest")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("cannot create config directory: %w", err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("cannot marshal config: %w", err)
	}

	path := filepath.Join(dir, "config.yaml")
	return os.WriteFile(path, data, 0o644)
}

// ConfigPath returns the path to the config file.
func ConfigPath() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "config.yaml"
	}
	return filepath.Join(configDir, "ernest", "config.yaml")
}

// AddProvider adds or updates a provider in the config.
func (c *Config) AddProvider(pc ProviderConfig) {
	for i, p := range c.Providers {
		if strings.EqualFold(p.Name, pc.Name) {
			// Preserve existing fields when not explicitly set in the new config
			if pc.Priority == 0 {
				pc.Priority = p.Priority
			}
			if pc.APIKeyEnv == "" {
				pc.APIKeyEnv = p.APIKeyEnv
			}
			if pc.BaseURL == "" {
				pc.BaseURL = p.BaseURL
			}
			c.Providers[i] = pc
			return
		}
	}
	// Auto-assign priority if not set
	if pc.Priority == 0 {
		maxPriority := 0
		for _, p := range c.Providers {
			if p.Priority > maxPriority {
				maxPriority = p.Priority
			}
		}
		pc.Priority = maxPriority + 1
	}
	c.Providers = append(c.Providers, pc)
}

// RemoveProvider removes a provider by name (case-insensitive).
func (c *Config) RemoveProvider(name string) {
	for i, p := range c.Providers {
		if strings.EqualFold(p.Name, name) {
			c.Providers = append(c.Providers[:i], c.Providers[i+1:]...)
			return
		}
	}
}

// SetModel updates the model for a provider (case-insensitive name match).
func (c *Config) SetModel(providerName, model string) bool {
	for i, p := range c.Providers {
		if strings.EqualFold(p.Name, providerName) {
			c.Providers[i].Model = model
			return true
		}
	}
	return false
}

// SortedProviders returns providers sorted by priority (ascending).
func (c *Config) SortedProviders() []ProviderConfig {
	sorted := make([]ProviderConfig, len(c.Providers))
	copy(sorted, c.Providers)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Priority < sorted[j].Priority
	})
	return sorted
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
