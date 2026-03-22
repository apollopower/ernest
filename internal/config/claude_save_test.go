package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveAllowedTool_NewFile(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".claude"), 0o755)

	err := SaveAllowedTool(dir, "bash")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".claude", "settings.local.json"))
	if err != nil {
		t.Fatalf("failed to read settings: %v", err)
	}

	var settings map[string]any
	json.Unmarshal(data, &settings)

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
	os.MkdirAll(claudeDir, 0o755)

	// Pre-populate with an existing tool
	initial := map[string]any{
		"allowedTools": []string{"read_file"},
		"otherSetting": true,
	}
	data, _ := json.MarshalIndent(initial, "", "  ")
	os.WriteFile(filepath.Join(claudeDir, "settings.local.json"), data, 0o644)

	err := SaveAllowedTool(dir, "bash")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ = os.ReadFile(filepath.Join(claudeDir, "settings.local.json"))
	var settings map[string]any
	json.Unmarshal(data, &settings)

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
	os.MkdirAll(filepath.Join(dir, ".claude"), 0o755)

	SaveAllowedTool(dir, "bash")
	SaveAllowedTool(dir, "bash") // second call should be no-op

	data, _ := os.ReadFile(filepath.Join(dir, ".claude", "settings.local.json"))
	var settings map[string]any
	json.Unmarshal(data, &settings)

	allowed := settings["allowedTools"].([]any)
	if len(allowed) != 1 {
		t.Errorf("expected 1 tool (idempotent), got %d", len(allowed))
	}
}

func TestSaveAllowedTool_CreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	// Don't create .claude dir — SaveAllowedTool should create it

	err := SaveAllowedTool(dir, "bash")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, ".claude", "settings.local.json")); err != nil {
		t.Error("expected settings.local.json to be created")
	}
}
