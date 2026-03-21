package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if len(cfg.Providers) != 1 {
		t.Fatalf("expected 1 default provider, got %d", len(cfg.Providers))
	}

	p := cfg.Providers[0]
	if p.Name != "anthropic" {
		t.Errorf("expected default provider 'anthropic', got %q", p.Name)
	}
	if p.Model != "claude-opus-4-6-20250610" {
		t.Errorf("expected default model 'claude-opus-4-6-20250610', got %q", p.Model)
	}
	if p.APIKeyEnv != "ANTHROPIC_API_KEY" {
		t.Errorf("expected default api_key_env 'ANTHROPIC_API_KEY', got %q", p.APIKeyEnv)
	}
	if cfg.CooldownSeconds != 30 {
		t.Errorf("expected cooldown 30, got %d", cfg.CooldownSeconds)
	}
	if cfg.MaxContextTokens != 180000 {
		t.Errorf("expected max context tokens 180000, got %d", cfg.MaxContextTokens)
	}
}

func TestPrimaryProvider(t *testing.T) {
	cfg := Config{
		Providers: []ProviderConfig{
			{Name: "openai", Priority: 2},
			{Name: "anthropic", Priority: 1},
			{Name: "gemini", Priority: 3},
		},
	}

	primary := cfg.PrimaryProvider()
	if primary.Name != "anthropic" {
		t.Errorf("expected primary provider 'anthropic', got %q", primary.Name)
	}
}

func TestPrimaryProviderEmpty(t *testing.T) {
	cfg := Config{}
	primary := cfg.PrimaryProvider()
	if primary.Name != "anthropic" {
		t.Errorf("expected fallback to default provider, got %q", primary.Name)
	}
}

func TestLoadMissingConfig(t *testing.T) {
	// Load should return defaults when config file doesn't exist
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Providers) == 0 {
		t.Fatal("expected default providers")
	}
}

func TestLoadValidConfig(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, "ernest")
	os.MkdirAll(configDir, 0o755)

	configContent := `providers:
  - name: openai
    api_key_env: OPENAI_API_KEY
    model: gpt-4.1
    priority: 1
cooldown_seconds: 60
max_context_tokens: 200000
`
	os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(configContent), 0o644)

	// We can't easily override UserConfigDir, so test the YAML parsing directly
	cfg := DefaultConfig()
	err := yamlUnmarshalConfig([]byte(configContent), &cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cfg.Providers) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(cfg.Providers))
	}
	if cfg.Providers[0].Name != "openai" {
		t.Errorf("expected provider 'openai', got %q", cfg.Providers[0].Name)
	}
	if cfg.CooldownSeconds != 60 {
		t.Errorf("expected cooldown 60, got %d", cfg.CooldownSeconds)
	}
	if cfg.MaxContextTokens != 200000 {
		t.Errorf("expected max context tokens 200000, got %d", cfg.MaxContextTokens)
	}
}
