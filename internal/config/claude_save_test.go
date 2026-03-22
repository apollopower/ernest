package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveAllowedTool_NewFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
		t.Fatalf("failed to create .claude dir: %v", err)
	}

	if err := SaveAllowedTool(dir, "bash"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".claude", "settings.local.json"))
	if err != nil {
		t.Fatalf("failed to read settings: %v", err)
	}

	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("failed to parse settings: %v", err)
	}

	allowed, ok := settings["allowedTools"].([]any)
	if !ok || len(allowed) != 1 {
		t.Fatalf("expected 1 allowed tool, got %v", settings["allowedTools"])
	}
	if allowed[0] != "bash" {
		t.Errorf("expected 'bash', got %v", allowed[0])
	}
}

func TestSaveAllowedTool_AppendToExisting(t *testing.T) {
	dir := t.TempDir()
	claudeDir := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("failed to create .claude dir: %v", err)
	}

	// Pre-populate with an existing tool
	initial := map[string]any{
		"allowedTools": []string{"read_file"},
		"otherSetting": true,
	}
	data, err := json.MarshalIndent(initial, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal initial settings: %v", err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.local.json"), data, 0o644); err != nil {
		t.Fatalf("failed to write initial settings: %v", err)
	}

	if err := SaveAllowedTool(dir, "bash"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err = os.ReadFile(filepath.Join(claudeDir, "settings.local.json"))
	if err != nil {
		t.Fatalf("failed to read settings: %v", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("failed to parse settings: %v", err)
	}

	allowed := settings["allowedTools"].([]any)
	if len(allowed) != 2 {
		t.Fatalf("expected 2 allowed tools, got %d", len(allowed))
	}

	// Verify other settings preserved
	if settings["otherSetting"] != true {
		t.Error("expected otherSetting to be preserved")
	}
}

func TestSaveAllowedTool_Idempotent(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
		t.Fatalf("failed to create .claude dir: %v", err)
	}

	if err := SaveAllowedTool(dir, "bash"); err != nil {
		t.Fatalf("first save failed: %v", err)
	}
	if err := SaveAllowedTool(dir, "bash"); err != nil {
		t.Fatalf("second save failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".claude", "settings.local.json"))
	if err != nil {
		t.Fatalf("failed to read settings: %v", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("failed to parse settings: %v", err)
	}

	allowed := settings["allowedTools"].([]any)
	if len(allowed) != 1 {
		t.Errorf("expected 1 tool (idempotent), got %d", len(allowed))
	}
}

func TestSaveAllowedTool_CreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	// Don't create .claude dir — SaveAllowedTool should create it

	if err := SaveAllowedTool(dir, "bash"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, ".claude", "settings.local.json")); err != nil {
		t.Error("expected settings.local.json to be created")
	}
}

func TestSaveAllowedTool_CorruptJSON(t *testing.T) {
	dir := t.TempDir()
	claudeDir := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("failed to create .claude dir: %v", err)
	}

	// Write corrupt JSON
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.local.json"), []byte("{invalid json"), 0o644); err != nil {
		t.Fatalf("failed to write corrupt file: %v", err)
	}

	err := SaveAllowedTool(dir, "bash")
	if err == nil {
		t.Fatal("expected error for corrupt JSON")
	}
}
