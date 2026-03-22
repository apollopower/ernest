package agent

import (
	"ernest/internal/config"
	"testing"
)

func TestPermissionChecker_Allowed(t *testing.T) {
	cfg := &config.ClaudeConfig{
		AllowedTools: []string{"read_file", "glob"},
	}
	pc := NewPermissionChecker(cfg)

	if pc.Check("read_file") != PermissionAllowed {
		t.Error("expected read_file to be allowed")
	}
	if pc.Check("glob") != PermissionAllowed {
		t.Error("expected glob to be allowed")
	}
}

func TestPermissionChecker_Denied(t *testing.T) {
	cfg := &config.ClaudeConfig{
		DeniedTools: []string{"bash"},
	}
	pc := NewPermissionChecker(cfg)

	if pc.Check("bash") != PermissionDenied {
		t.Error("expected bash to be denied")
	}
}

func TestPermissionChecker_Ask(t *testing.T) {
	cfg := &config.ClaudeConfig{
		AllowedTools: []string{"read_file"},
		DeniedTools:  []string{"bash"},
	}
	pc := NewPermissionChecker(cfg)

	if pc.Check("write_file") != PermissionAsk {
		t.Error("expected write_file to require asking")
	}
}

func TestPermissionChecker_DeniedTakesPrecedence(t *testing.T) {
	cfg := &config.ClaudeConfig{
		AllowedTools: []string{"bash"},
		DeniedTools:  []string{"bash"},
	}
	pc := NewPermissionChecker(cfg)

	if pc.Check("bash") != PermissionDenied {
		t.Error("expected denied to take precedence over allowed")
	}
}

func TestPermissionChecker_EmptyConfig(t *testing.T) {
	pc := NewPermissionChecker(&config.ClaudeConfig{})

	if pc.Check("anything") != PermissionAsk {
		t.Error("expected Ask for empty config")
	}
}

func TestPermissionChecker_NilConfig(t *testing.T) {
	pc := NewPermissionChecker(nil)

	if pc.Check("anything") != PermissionAsk {
		t.Error("expected Ask for nil config")
	}
}
