package mcp

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMCPConfig_ProjectFile(t *testing.T) {
	dir := t.TempDir()
	mcpJSON := `{
		"mcpServers": {
			"test-server": {
				"command": "echo",
				"args": ["hello"]
			}
		}
	}`
	os.WriteFile(filepath.Join(dir, ".mcp.json"), []byte(mcpJSON), 0o644)

	config, err := LoadMCPConfig(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	srv, ok := config.Servers["test-server"]
	if !ok {
		t.Fatal("expected test-server in config")
	}
	if srv.Command != "echo" {
		t.Errorf("expected command 'echo', got %q", srv.Command)
	}
	if len(srv.Args) != 1 || srv.Args[0] != "hello" {
		t.Errorf("expected args ['hello'], got %v", srv.Args)
	}
}

func TestLoadMCPConfig_UserFile(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	claudeJSON := `{
		"mcpServers": {
			"global-server": {
				"command": "global-cmd"
			}
		}
	}`
	os.WriteFile(filepath.Join(home, ".claude.json"), []byte(claudeJSON), 0o644)

	config, err := LoadMCPConfig(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := config.Servers["global-server"]; !ok {
		t.Error("expected global-server from ~/.claude.json")
	}
}

func TestLoadMCPConfig_ClaudeJSONProjectScoped(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	// ~/.claude.json with both top-level and project-scoped servers
	claudeJSON := fmt.Sprintf(`{
		"mcpServers": {
			"global-server": {"command": "global-cmd"}
		},
		"projects": {
			%q: {
				"mcpServers": {
					"project-server": {"command": "proj-cmd"},
					"global-server": {"command": "proj-override"}
				}
			}
		}
	}`, dir)
	os.WriteFile(filepath.Join(home, ".claude.json"), []byte(claudeJSON), 0o644)

	config, err := LoadMCPConfig(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Project-scoped server should be present
	if srv, ok := config.Servers["project-server"]; !ok {
		t.Error("expected project-server from project-scoped config")
	} else if srv.Command != "proj-cmd" {
		t.Errorf("expected proj-cmd, got %q", srv.Command)
	}

	// Project-scoped should override top-level on name collision
	if srv, ok := config.Servers["global-server"]; !ok {
		t.Error("expected global-server")
	} else if srv.Command != "proj-override" {
		t.Errorf("expected project scope to override top-level, got %q", srv.Command)
	}
}

func TestLoadMCPConfig_ProjectOverridesUser(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	// User config
	os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{
		"mcpServers": {
			"server": {"command": "user-cmd"}
		}
	}`), 0o644)

	// Project config (should override)
	os.WriteFile(filepath.Join(dir, ".mcp.json"), []byte(`{
		"mcpServers": {
			"server": {"command": "project-cmd"}
		}
	}`), 0o644)

	config, err := LoadMCPConfig(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if config.Servers["server"].Command != "project-cmd" {
		t.Errorf("expected project-cmd to override user-cmd, got %q", config.Servers["server"].Command)
	}
}

func TestLoadMCPConfig_RejectsDoubleUnderscore(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".mcp.json"), []byte(`{
		"mcpServers": {
			"bad__name": {"command": "test"},
			"good-name": {"command": "test"}
		}
	}`), 0o644)

	config, err := LoadMCPConfig(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := config.Servers["bad__name"]; ok {
		t.Error("expected bad__name to be rejected")
	}
	if _, ok := config.Servers["good-name"]; !ok {
		t.Error("expected good-name to be accepted")
	}
}

func TestLoadMCPConfig_NoFiles(t *testing.T) {
	dir := t.TempDir()
	config, err := LoadMCPConfig(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(config.Servers) != 0 {
		t.Errorf("expected 0 servers, got %d", len(config.Servers))
	}
}

func TestExpandEnvVars(t *testing.T) {
	t.Setenv("TEST_KEY", "myvalue")

	tests := []struct {
		input    string
		expected string
	}{
		{"${TEST_KEY}", "myvalue"},
		{"prefix-${TEST_KEY}-suffix", "prefix-myvalue-suffix"},
		{"${NONEXISTENT:-default}", "default"},
		{"${TEST_KEY:-fallback}", "myvalue"},
		{"no vars here", "no vars here"},
		{"${NONEXISTENT}", ""},
	}

	for _, tt := range tests {
		result := expandEnvVars(tt.input)
		if result != tt.expected {
			t.Errorf("expandEnvVars(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestParseMCPToolName(t *testing.T) {
	tests := []struct {
		name       string
		wantServer string
		wantTool   string
		wantOK     bool
	}{
		{"mcp__sentry__search_issues", "sentry", "search_issues", true},
		{"mcp__github__create_pr", "github", "create_pr", true},
		{"read_file", "", "", false},
		{"mcp__", "", "", false},
		{"mcp__server", "", "", false},
	}

	for _, tt := range tests {
		server, tool, ok := ParseMCPToolName(tt.name)
		if ok != tt.wantOK || server != tt.wantServer || tool != tt.wantTool {
			t.Errorf("ParseMCPToolName(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tt.name, server, tool, ok, tt.wantServer, tt.wantTool, tt.wantOK)
		}
	}
}
