package agent

import (
	"encoding/json"
	"ernest/internal/config"
	"testing"
)

func TestPermissionChecker_Allowed(t *testing.T) {
	cfg := &config.ClaudeConfig{
		AllowedTools: []string{"read_file", "glob"},
	}
	pc := NewPermissionChecker(cfg, false)

	if pc.Check("read_file", nil) != PermissionAllowed {
		t.Error("expected read_file to be allowed")
	}
	if pc.Check("glob", nil) != PermissionAllowed {
		t.Error("expected glob to be allowed")
	}
}

func TestPermissionChecker_Denied(t *testing.T) {
	cfg := &config.ClaudeConfig{
		DeniedTools: []string{"bash"},
	}
	pc := NewPermissionChecker(cfg, false)

	if pc.Check("bash", nil) != PermissionDenied {
		t.Error("expected bash to be denied")
	}
}

func TestPermissionChecker_Ask(t *testing.T) {
	cfg := &config.ClaudeConfig{
		AllowedTools: []string{"read_file"},
		DeniedTools:  []string{"bash"},
	}
	pc := NewPermissionChecker(cfg, false)

	if pc.Check("write_file", nil) != PermissionAsk {
		t.Error("expected write_file to require asking")
	}
}

func TestPermissionChecker_DeniedTakesPrecedence(t *testing.T) {
	cfg := &config.ClaudeConfig{
		AllowedTools: []string{"bash"},
		DeniedTools:  []string{"bash"},
	}
	pc := NewPermissionChecker(cfg, false)

	if pc.Check("bash", nil) != PermissionDenied {
		t.Error("expected denied to take precedence over allowed")
	}
}

func TestPermissionChecker_EmptyConfig(t *testing.T) {
	pc := NewPermissionChecker(&config.ClaudeConfig{}, false)

	if pc.Check("anything", nil) != PermissionAsk {
		t.Error("expected Ask for empty config")
	}
}

func TestPermissionChecker_NilConfig(t *testing.T) {
	pc := NewPermissionChecker(nil, false)

	if pc.Check("anything", nil) != PermissionAsk {
		t.Error("expected Ask for nil config")
	}
}

// Granular permission tests

func TestPermissionChecker_BashExactCommand(t *testing.T) {
	cfg := &config.ClaudeConfig{
		AllowedTools: []string{"bash(git pull)"},
	}
	pc := NewPermissionChecker(cfg, false)

	gitPull := json.RawMessage(`{"command": "git pull"}`)
	gitPush := json.RawMessage(`{"command": "git push"}`)
	rmRf := json.RawMessage(`{"command": "rm -rf /"}`)

	if pc.Check("bash", gitPull) != PermissionAllowed {
		t.Error("expected 'git pull' to be allowed")
	}
	if pc.Check("bash", gitPush) != PermissionAsk {
		t.Error("expected 'git push' to require asking")
	}
	if pc.Check("bash", rmRf) != PermissionAsk {
		t.Error("expected 'rm -rf /' to require asking")
	}
}

func TestPermissionChecker_BashGlobPattern(t *testing.T) {
	cfg := &config.ClaudeConfig{
		AllowedTools: []string{"bash(go *)"},
	}
	pc := NewPermissionChecker(cfg, false)

	goTest := json.RawMessage(`{"command": "go test"}`)
	goBuild := json.RawMessage(`{"command": "go build"}`)
	npmTest := json.RawMessage(`{"command": "npm test"}`)

	if pc.Check("bash", goTest) != PermissionAllowed {
		t.Error("expected 'go test' to match 'go *'")
	}
	if pc.Check("bash", goBuild) != PermissionAllowed {
		t.Error("expected 'go build' to match 'go *'")
	}
	if pc.Check("bash", npmTest) != PermissionAsk {
		t.Error("expected 'npm test' to NOT match 'go *'")
	}
}

func TestPermissionChecker_BashGlobWithPaths(t *testing.T) {
	cfg := &config.ClaudeConfig{
		AllowedTools: []string{"bash(go *)"},
	}
	pc := NewPermissionChecker(cfg, false)

	// Commands containing / should still match
	goTestDots := json.RawMessage(`{"command": "go test ./..."}`)
	goBuildPath := json.RawMessage(`{"command": "go build ./cmd/ernest"}`)

	if pc.Check("bash", goTestDots) != PermissionAllowed {
		t.Error("expected 'go test ./...' to match 'go *'")
	}
	if pc.Check("bash", goBuildPath) != PermissionAllowed {
		t.Error("expected 'go build ./cmd/ernest' to match 'go *'")
	}
}

func TestPermissionChecker_BashFullToolAllowStillWorks(t *testing.T) {
	// Backward compat: plain "bash" still allows all bash commands
	cfg := &config.ClaudeConfig{
		AllowedTools: []string{"bash"},
	}
	pc := NewPermissionChecker(cfg, false)

	anyCmd := json.RawMessage(`{"command": "rm -rf /"}`)
	if pc.Check("bash", anyCmd) != PermissionAllowed {
		t.Error("expected plain 'bash' to allow all commands")
	}
}

func TestPermissionChecker_GranularDenied(t *testing.T) {
	cfg := &config.ClaudeConfig{
		AllowedTools: []string{"bash(git *)"},
		DeniedTools:  []string{"bash(git push --force)"},
	}
	pc := NewPermissionChecker(cfg, false)

	gitPull := json.RawMessage(`{"command": "git pull"}`)
	gitForce := json.RawMessage(`{"command": "git push --force"}`)

	if pc.Check("bash", gitPull) != PermissionAllowed {
		t.Error("expected 'git pull' to be allowed")
	}
	if pc.Check("bash", gitForce) != PermissionDenied {
		t.Error("expected 'git push --force' to be denied")
	}
}

func TestPermissionKey_Bash(t *testing.T) {
	input := json.RawMessage(`{"command": "git pull"}`)
	key := PermissionKey("bash", input)
	if key != "bash(git pull)" {
		t.Errorf("expected 'bash(git pull)', got %q", key)
	}
}

func TestPermissionKey_BashEmptyCommand(t *testing.T) {
	// Should return empty — don't fall back to plain "bash"
	key := PermissionKey("bash", json.RawMessage(`{}`))
	if key != "" {
		t.Errorf("expected empty key for missing command, got %q", key)
	}

	key = PermissionKey("bash", json.RawMessage(`{"command": ""}`))
	if key != "" {
		t.Errorf("expected empty key for empty command, got %q", key)
	}

	key = PermissionKey("bash", nil)
	if key != "" {
		t.Errorf("expected empty key for nil input, got %q", key)
	}
}

func TestPermissionKey_NonBash(t *testing.T) {
	input := json.RawMessage(`{"file_path": "/tmp/test.txt"}`)
	key := PermissionKey("write_file", input)
	if key != "write_file" {
		t.Errorf("expected 'write_file', got %q", key)
	}
}

func TestMatchGlob(t *testing.T) {
	tests := []struct {
		pattern string
		value   string
		want    bool
	}{
		// Exact match
		{"git pull", "git pull", true},
		{"git pull", "git push", false},
		// Trailing wildcard
		{"go *", "go test", true},
		{"go *", "go test ./...", true},
		{"go *", "go build ./cmd/ernest", true},
		{"go *", "npm test", false},
		// Leading wildcard
		{"* --verbose", "git pull --verbose", true},
		{"* --verbose", "git pull", false},
		// Middle wildcard
		{"git * --force", "git push --force", true},
		{"git * --force", "git push origin main --force", true},
		{"git * --force", "git pull", false},
		// Multiple wildcards
		{"*test*", "go test ./...", true},
		{"*test*", "npm test --verbose", true},
		{"*test*", "go build", false},
		// No wildcard = exact
		{"echo hello", "echo hello", true},
		{"echo hello", "echo hello world", false},
	}

	for _, tt := range tests {
		got := matchGlob(tt.pattern, tt.value)
		if got != tt.want {
			t.Errorf("matchGlob(%q, %q) = %v, want %v", tt.pattern, tt.value, got, tt.want)
		}
	}
}

func TestPermissionChecker_AllowRuntime(t *testing.T) {
	pc := NewPermissionChecker(&config.ClaudeConfig{}, false)

	gitPull := json.RawMessage(`{"command": "git pull"}`)

	// Before Allow: should ask
	if pc.Check("bash", gitPull) != PermissionAsk {
		t.Error("expected Ask before Allow")
	}

	// Allow the specific command
	pc.Allow("bash(git pull)")

	// After Allow: should be allowed
	if pc.Check("bash", gitPull) != PermissionAllowed {
		t.Error("expected Allowed after Allow")
	}

	// Other commands still ask
	otherCmd := json.RawMessage(`{"command": "npm install"}`)
	if pc.Check("bash", otherCmd) != PermissionAsk {
		t.Error("expected Ask for non-matching command")
	}
}

func TestPermissionChecker_AutoApprove(t *testing.T) {
	pc := NewPermissionChecker(&config.ClaudeConfig{}, true)

	// Everything should be allowed
	if pc.Check("bash", json.RawMessage(`{"command": "rm -rf /"}`)) != PermissionAllowed {
		t.Error("expected auto-approve to allow bash")
	}
	if pc.Check("write_file", nil) != PermissionAllowed {
		t.Error("expected auto-approve to allow write_file")
	}
	if pc.Check("unknown_tool", nil) != PermissionAllowed {
		t.Error("expected auto-approve to allow unknown tools")
	}
}

func TestPermissionChecker_AutoApproveRespectsExplicitDeny(t *testing.T) {
	pc := NewPermissionChecker(&config.ClaudeConfig{
		DeniedTools: []string{"bash"},
	}, true)

	// bash denied even with auto-approve
	if pc.Check("bash", nil) != PermissionDenied {
		t.Error("expected denied to override auto-approve")
	}

	// other tools still allowed
	if pc.Check("write_file", nil) != PermissionAllowed {
		t.Error("expected auto-approve for non-denied tools")
	}
}

func TestPermissionChecker_AutoApproveRespectsGranularDeny(t *testing.T) {
	pc := NewPermissionChecker(&config.ClaudeConfig{
		DeniedTools: []string{"bash(rm *)"},
	}, true)

	// rm commands denied even with auto-approve
	if pc.Check("bash", json.RawMessage(`{"command": "rm -rf /"}`)) != PermissionDenied {
		t.Error("expected granular deny to override auto-approve")
	}

	// other bash commands allowed
	if pc.Check("bash", json.RawMessage(`{"command": "echo hello"}`)) != PermissionAllowed {
		t.Error("expected auto-approve for non-denied bash commands")
	}
}
