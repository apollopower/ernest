package config

import "testing"

func TestAddProvider_New(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AddProvider(ProviderConfig{Name: "openai", Model: "gpt-4.1"})

	if len(cfg.Providers) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(cfg.Providers))
	}
	// Auto-assigned priority should be 2 (max existing is 1)
	if cfg.Providers[1].Priority != 2 {
		t.Errorf("expected priority 2 for new provider, got %d", cfg.Providers[1].Priority)
	}
}

func TestAddProvider_UpdatePreservesFields(t *testing.T) {
	cfg := Config{
		Providers: []ProviderConfig{
			{Name: "anthropic", Model: "claude-opus-4-6", Priority: 1, APIKeyEnv: "MY_KEY"},
		},
	}

	// Update model, don't set priority or APIKeyEnv
	cfg.AddProvider(ProviderConfig{Name: "anthropic", Model: "claude-sonnet-4-6"})

	if len(cfg.Providers) != 1 {
		t.Fatalf("expected 1 provider (update, not add), got %d", len(cfg.Providers))
	}
	if cfg.Providers[0].Model != "claude-sonnet-4-6" {
		t.Errorf("expected updated model, got %q", cfg.Providers[0].Model)
	}
	if cfg.Providers[0].Priority != 1 {
		t.Errorf("expected preserved priority 1, got %d", cfg.Providers[0].Priority)
	}
	if cfg.Providers[0].APIKeyEnv != "MY_KEY" {
		t.Errorf("expected preserved APIKeyEnv, got %q", cfg.Providers[0].APIKeyEnv)
	}
}

func TestRemoveProvider(t *testing.T) {
	cfg := Config{
		Providers: []ProviderConfig{
			{Name: "anthropic", Priority: 1},
			{Name: "openai", Priority: 2},
		},
	}

	cfg.RemoveProvider("anthropic")
	if len(cfg.Providers) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(cfg.Providers))
	}
	if cfg.Providers[0].Name != "openai" {
		t.Error("expected openai to remain")
	}
}

func TestRemoveProvider_CaseInsensitive(t *testing.T) {
	cfg := Config{
		Providers: []ProviderConfig{
			{Name: "anthropic", Priority: 1},
		},
	}
	cfg.RemoveProvider("Anthropic")
	if len(cfg.Providers) != 0 {
		t.Error("expected case-insensitive removal")
	}
}

func TestSetModel(t *testing.T) {
	cfg := Config{
		Providers: []ProviderConfig{
			{Name: "anthropic", Model: "claude-opus-4-6"},
		},
	}

	ok := cfg.SetModel("anthropic", "claude-sonnet-4-6")
	if !ok {
		t.Error("expected SetModel to return true")
	}
	if cfg.Providers[0].Model != "claude-sonnet-4-6" {
		t.Errorf("expected updated model, got %q", cfg.Providers[0].Model)
	}
}

func TestSetModel_NotFound(t *testing.T) {
	cfg := Config{}
	ok := cfg.SetModel("missing", "model")
	if ok {
		t.Error("expected SetModel to return false for missing provider")
	}
}

func TestSortedProviders(t *testing.T) {
	cfg := Config{
		Providers: []ProviderConfig{
			{Name: "c", Priority: 3},
			{Name: "a", Priority: 1},
			{Name: "b", Priority: 2},
		},
	}

	sorted := cfg.SortedProviders()
	if sorted[0].Name != "a" || sorted[1].Name != "b" || sorted[2].Name != "c" {
		t.Errorf("expected sorted by priority, got %s %s %s", sorted[0].Name, sorted[1].Name, sorted[2].Name)
	}

	// Original should not be mutated
	if cfg.Providers[0].Name != "c" {
		t.Error("SortedProviders should not mutate the original")
	}
}
