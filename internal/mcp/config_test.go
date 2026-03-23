package mcp

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("failed to write %s: %v", path, err)
	}
}

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
	writeFile(t, filepath.Join(dir, ".mcp.json"), []byte(mcpJSON))

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
	writeFile(t, filepath.Join(home, ".claude.json"), []byte(claudeJSON))

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
	writeFile(t, filepath.Join(home, ".claude.json"), []byte(claudeJSON))

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
	writeFile(t, filepath.Join(home, ".claude.json"), []byte(`{
		"mcpServers": {
			"server": {"command": "user-cmd"}
		}
	}`))

	// Project config (should override)
	writeFile(t, filepath.Join(dir, ".mcp.json"), []byte(`{
		"mcpServers": {
			"server": {"command": "project-cmd"}
		}
	}`))

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
	writeFile(t, filepath.Join(dir, ".mcp.json"), []byte(`{
		"mcpServers": {
			"bad__name": {"command": "test"},
			"good-name": {"command": "test"}
		}
	}`))

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

func TestSaveServerToProjectConfig_New(t *testing.T) {
	dir := t.TempDir()

	cfg := MCPServerConfig{Command: "echo", Args: []string{"hello"}}
	if err := SaveServerToProjectConfig(dir, "test-server", cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the file was created and is parseable
	servers, err := loadMCPFile(filepath.Join(dir, ".mcp.json"))
	if err != nil {
		t.Fatalf("cannot read saved file: %v", err)
	}
	srv, ok := servers["test-server"]
	if !ok {
		t.Fatal("expected test-server in saved config")
	}
	if srv.Command != "echo" {
		t.Errorf("expected command 'echo', got %q", srv.Command)
	}
}

func TestSaveServerToProjectConfig_Update(t *testing.T) {
	dir := t.TempDir()

	// Create initial config
	writeFile(t, filepath.Join(dir, ".mcp.json"), []byte(`{
		"mcpServers": {
			"existing": {"command": "old"}
		}
	}`))

	// Add new server — should preserve existing
	cfg := MCPServerConfig{Command: "new-cmd"}
	if err := SaveServerToProjectConfig(dir, "new-server", cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	servers, err := loadMCPFile(filepath.Join(dir, ".mcp.json"))
	if err != nil {
		t.Fatalf("cannot read saved file: %v", err)
	}
	if _, ok := servers["existing"]; !ok {
		t.Error("expected existing server to be preserved")
	}
	if _, ok := servers["new-server"]; !ok {
		t.Error("expected new-server to be added")
	}
}

func TestRemoveServerFromProjectConfig(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".mcp.json"), []byte(`{
		"mcpServers": {
			"keep": {"command": "a"},
			"remove": {"command": "b"}
		}
	}`))

	if err := RemoveServerFromProjectConfig(dir, "remove"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	servers, err := loadMCPFile(filepath.Join(dir, ".mcp.json"))
	if err != nil {
		t.Fatalf("cannot read saved file: %v", err)
	}
	if _, ok := servers["keep"]; !ok {
		t.Error("expected 'keep' to be preserved")
	}
	if _, ok := servers["remove"]; ok {
		t.Error("expected 'remove' to be deleted")
	}
}

func TestRemoveServerFromProjectConfig_NotFound(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".mcp.json"), []byte(`{
		"mcpServers": {"a": {"command": "x"}}
	}`))

	err := RemoveServerFromProjectConfig(dir, "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent server")
	}
}

func TestBuildTransport_Stdio(t *testing.T) {
	cfg := MCPServerConfig{Command: "echo", Args: []string{"hi"}}
	transport, err := buildTransport(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := transport.(*mcpsdk.CommandTransport); !ok {
		t.Errorf("expected CommandTransport, got %T", transport)
	}
}

func TestBuildTransport_HTTP(t *testing.T) {
	cfg := MCPServerConfig{URL: "http://localhost:8080"}
	transport, err := buildTransport(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := transport.(*mcpsdk.StreamableClientTransport); !ok {
		t.Errorf("expected StreamableClientTransport, got %T", transport)
	}
}

func TestBuildTransport_SSE(t *testing.T) {
	cfg := MCPServerConfig{URL: "http://localhost:8080", Type: "sse"}
	transport, err := buildTransport(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := transport.(*mcpsdk.SSEClientTransport); !ok {
		t.Errorf("expected SSEClientTransport, got %T", transport)
	}
}

func TestBuildTransport_NoConfig(t *testing.T) {
	cfg := MCPServerConfig{}
	_, err := buildTransport(cfg)
	if err == nil {
		t.Error("expected error for empty config")
	}
}
