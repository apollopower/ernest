package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCredentials_SaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("APPDATA", dir)
	t.Setenv("HOME", dir)

	creds := &Credentials{
		Providers: []ProviderCredential{
			{Name: "anthropic", APIKey: "sk-ant-test"},
			{Name: "openai", APIKey: "sk-test"},
		},
	}

	if err := SaveCredentials(creds); err != nil {
		t.Fatalf("failed to save: %v", err)
	}

	// Verify file exists with correct permissions
	path := CredentialsPath()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("file not found: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("expected 0600 permissions, got %o", info.Mode().Perm())
	}

	// Load and verify
	loaded, err := LoadCredentials()
	if err != nil {
		t.Fatalf("failed to load: %v", err)
	}
	if len(loaded.Providers) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(loaded.Providers))
	}
	if loaded.Providers[0].APIKey != "sk-ant-test" {
		t.Errorf("expected anthropic key, got %q", loaded.Providers[0].APIKey)
	}
}

func TestCredentials_GetKey(t *testing.T) {
	creds := &Credentials{
		Providers: []ProviderCredential{
			{Name: "anthropic", APIKey: "sk-ant-test"},
		},
	}

	if creds.GetKey("anthropic") != "sk-ant-test" {
		t.Error("expected to find anthropic key")
	}
	if creds.GetKey("missing") != "" {
		t.Error("expected empty for missing provider")
	}
}

func TestCredentials_SetKey(t *testing.T) {
	creds := &Credentials{}

	// Add new
	creds.SetKey("anthropic", "key1")
	if creds.GetKey("anthropic") != "key1" {
		t.Error("expected key1")
	}

	// Update existing
	creds.SetKey("anthropic", "key2")
	if creds.GetKey("anthropic") != "key2" {
		t.Error("expected key2")
	}
	if len(creds.Providers) != 1 {
		t.Error("expected 1 provider (update, not duplicate)")
	}
}

func TestCredentials_Remove(t *testing.T) {
	creds := &Credentials{
		Providers: []ProviderCredential{
			{Name: "anthropic", APIKey: "key1"},
			{Name: "openai", APIKey: "key2"},
		},
	}

	creds.Remove("anthropic")
	if len(creds.Providers) != 1 {
		t.Fatalf("expected 1 provider after remove, got %d", len(creds.Providers))
	}
	if creds.Providers[0].Name != "openai" {
		t.Error("expected openai to remain")
	}

	// Remove nonexistent — no-op
	creds.Remove("missing")
	if len(creds.Providers) != 1 {
		t.Error("expected no change for missing provider")
	}
}

func TestCredentials_LoadNonexistent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("APPDATA", dir)
	t.Setenv("HOME", dir)

	creds, err := LoadCredentials()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(creds.Providers) != 0 {
		t.Error("expected empty credentials for nonexistent file")
	}
}

func TestCredentials_AtomicWrite(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("APPDATA", dir)
	t.Setenv("HOME", dir)

	// Save initial credentials
	creds := &Credentials{
		Providers: []ProviderCredential{
			{Name: "anthropic", APIKey: "key1"},
		},
	}
	if err := SaveCredentials(creds); err != nil {
		t.Fatalf("failed to save initial: %v", err)
	}

	// Save updated — should not leave temp files
	creds.SetKey("openai", "key2")
	if err := SaveCredentials(creds); err != nil {
		t.Fatalf("failed to save updated: %v", err)
	}

	// Check no temp files remain
	entries, _ := os.ReadDir(filepath.Dir(CredentialsPath()))
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}

	// Verify final content
	loaded, _ := LoadCredentials()
	if len(loaded.Providers) != 2 {
		t.Errorf("expected 2 providers, got %d", len(loaded.Providers))
	}
}
