package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadClaudeConfigEmpty(t *testing.T) {
	dir := t.TempDir()
	cfg, err := LoadClaudeConfig(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.SystemPrompt != "" {
		t.Errorf("expected empty system prompt, got %q", cfg.SystemPrompt)
	}
}

func TestLoadClaudeConfigProjectCLAUDE(t *testing.T) {
	dir := t.TempDir()
	claudeDir := filepath.Join(dir, ".claude")
	os.MkdirAll(claudeDir, 0o755)

	os.WriteFile(filepath.Join(claudeDir, "CLAUDE.md"), []byte("You are a helpful assistant."), 0o644)

	cfg, err := LoadClaudeConfig(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(cfg.SystemPrompt, "You are a helpful assistant.") {
		t.Error("expected system prompt to contain project CLAUDE.md content")
	}
	if !strings.Contains(cfg.SystemPrompt, "# Project Instructions") {
		t.Error("expected system prompt to have Project Instructions header")
	}
}

func TestLoadClaudeConfigRootCLAUDE(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("Legacy instructions."), 0o644)

	cfg, err := LoadClaudeConfig(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(cfg.SystemPrompt, "Legacy instructions.") {
		t.Error("expected system prompt to contain root CLAUDE.md content")
	}
}

func TestLoadClaudeConfigRules(t *testing.T) {
	dir := t.TempDir()
	rulesDir := filepath.Join(dir, ".claude", "rules")
	os.MkdirAll(rulesDir, 0o755)

	os.WriteFile(filepath.Join(rulesDir, "coding.md"), []byte("Always write tests."), 0o644)
	os.WriteFile(filepath.Join(rulesDir, "style.md"), []byte("Use gofmt."), 0o644)
	os.WriteFile(filepath.Join(rulesDir, "ignored.txt"), []byte("Not a rule."), 0o644)

	cfg, err := LoadClaudeConfig(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Rules) != 2 {
		t.Errorf("expected 2 rules, got %d", len(cfg.Rules))
	}
	if !strings.Contains(cfg.SystemPrompt, "Always write tests.") {
		t.Error("expected system prompt to contain coding rule")
	}
	if !strings.Contains(cfg.SystemPrompt, "Use gofmt.") {
		t.Error("expected system prompt to contain style rule")
	}
	if strings.Contains(cfg.SystemPrompt, "Not a rule.") {
		t.Error("should not include non-.md files")
	}
}

func TestLoadClaudeConfigSettings(t *testing.T) {
	dir := t.TempDir()
	claudeDir := filepath.Join(dir, ".claude")
	os.MkdirAll(claudeDir, 0o755)

	settings := `{
		"allowedTools": ["read_file", "write_file"],
		"deniedTools": ["bash"],
		"permissions": {"mode": "auto"}
	}`
	os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(settings), 0o644)

	cfg, err := LoadClaudeConfig(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.AllowedTools) != 2 {
		t.Errorf("expected 2 allowed tools, got %d", len(cfg.AllowedTools))
	}
	if len(cfg.DeniedTools) != 1 {
		t.Errorf("expected 1 denied tool, got %d", len(cfg.DeniedTools))
	}
	if cfg.PermissionMode != "auto" {
		t.Errorf("expected permission mode 'auto', got %q", cfg.PermissionMode)
	}
}

func TestLoadClaudeConfigCombined(t *testing.T) {
	dir := t.TempDir()
	claudeDir := filepath.Join(dir, ".claude")
	rulesDir := filepath.Join(claudeDir, "rules")
	os.MkdirAll(rulesDir, 0o755)

	os.WriteFile(filepath.Join(claudeDir, "CLAUDE.md"), []byte("Project instructions."), 0o644)
	os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("Root instructions."), 0o644)
	os.WriteFile(filepath.Join(rulesDir, "test.md"), []byte("Test rule."), 0o644)

	cfg, err := LoadClaudeConfig(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should contain all parts separated by ---
	if !strings.Contains(cfg.SystemPrompt, "Project instructions.") {
		t.Error("missing project instructions")
	}
	if !strings.Contains(cfg.SystemPrompt, "Root instructions.") {
		t.Error("missing root instructions")
	}
	if !strings.Contains(cfg.SystemPrompt, "Test rule.") {
		t.Error("missing test rule")
	}
	if !strings.Contains(cfg.SystemPrompt, "---") {
		t.Error("expected sections to be separated by ---")
	}
}
