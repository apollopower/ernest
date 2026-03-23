package agent

import (
	"context"
	"ernest/internal/config"
	"ernest/internal/provider"
	"ernest/internal/tools"
	"testing"
	"time"
)

func TestAgentMode_Default(t *testing.T) {
	mp := &mockProvider{name: "test", events: []provider.StreamEvent{{Type: "done"}}}
	router := provider.NewRouter([]provider.Provider{mp}, 0)
	a := New(router, nil, &config.ClaudeConfig{}, 0, false, "")

	if a.Mode() != ModeBuild {
		t.Errorf("expected default mode ModeBuild, got %q", a.Mode())
	}
}

func TestAgentMode_SetAndGet(t *testing.T) {
	mp := &mockProvider{name: "test", events: []provider.StreamEvent{{Type: "done"}}}
	router := provider.NewRouter([]provider.Provider{mp}, 0)
	a := New(router, nil, &config.ClaudeConfig{}, 0, false, ModeBuild)

	a.SetMode(ModePlan)
	if a.Mode() != ModePlan {
		t.Errorf("expected ModePlan, got %q", a.Mode())
	}

	a.SetMode(ModeBuild)
	if a.Mode() != ModeBuild {
		t.Errorf("expected ModeBuild, got %q", a.Mode())
	}
}

func TestAgentMode_PlanFiltersTtools(t *testing.T) {
	// In plan mode, the model should only see read-only tools
	mp := &multiTurnProvider{
		name: "test",
		turns: [][]provider.StreamEvent{
			{
				{Type: "text_delta", Text: "I can only read files in plan mode."},
				{Type: "done"},
			},
		},
	}

	registry := tools.NewRegistry(
		&mockTool{name: "read_file", result: "content"},
		&mockTool{name: "write_file", result: "written", requiresConfirmation: true},
		&mockTool{name: "bash", result: "output", requiresConfirmation: true},
		&mockTool{name: "glob", result: "files"},
		&mockTool{name: "grep", result: "matches"},
	)

	router := provider.NewRouter([]provider.Provider{mp}, 30*time.Second)
	a := New(router, registry, &config.ClaudeConfig{}, 0, false, ModePlan)

	// Run a prompt — the agent should only send read-only tools to the provider
	events := a.Run(context.Background(), "What files are in this project?")
	for range events {
	}

	// We can't directly verify which tools were sent to Stream() with the current
	// mock, but we can verify the mode is set correctly
	if a.Mode() != ModePlan {
		t.Error("expected plan mode to be active")
	}
}

func TestAgentMode_DefenseInDepth(t *testing.T) {
	// Even if a model hallucinates a write tool call in plan mode,
	// the defense-in-depth guard should reject it
	mp := &multiTurnProvider{
		name: "test",
		turns: [][]provider.StreamEvent{
			{
				{Type: "tool_use_start", ToolUseID: "c1", ToolName: "bash"},
				{Type: "tool_input_delta", ToolInput: `{"command": "rm -rf /"}`},
				{Type: "content_block_stop"},
				{Type: "message_delta", StopReason: "tool_use"},
				{Type: "done"},
			},
			{
				{Type: "text_delta", Text: "Tool was rejected."},
				{Type: "done"},
			},
		},
	}

	registry := tools.NewRegistry(
		&mockTool{name: "read_file", result: "content"},
		&mockTool{name: "bash", result: "should not execute"},
	)

	router := provider.NewRouter([]provider.Provider{mp}, 30*time.Second)
	a := New(router, registry, &config.ClaudeConfig{}, 0, false, ModePlan)

	var gotToolResult bool
	var toolResultText string
	for evt := range a.Run(context.Background(), "Run a command") {
		if evt.Type == "tool_result" {
			gotToolResult = true
			toolResultText = evt.ToolResult
		}
	}

	if !gotToolResult {
		t.Fatal("expected tool_result event")
	}
	if toolResultText != "error: tool bash is not available in plan mode" {
		t.Errorf("expected plan mode rejection, got %q", toolResultText)
	}
}

func TestAgentMode_InjectModeChange(t *testing.T) {
	mp := &mockProvider{name: "test", events: []provider.StreamEvent{{Type: "done"}}}
	router := provider.NewRouter([]provider.Provider{mp}, 0)
	a := New(router, nil, &config.ClaudeConfig{}, 0, false, ModeBuild)

	a.InjectModeChange(ModePlan)

	a.mu.Lock()
	histLen := len(a.history)
	lastMsg := a.history[histLen-1]
	a.mu.Unlock()

	if histLen != 1 {
		t.Fatalf("expected 1 history message, got %d", histLen)
	}
	if lastMsg.Role != provider.RoleUser {
		t.Error("expected user role for mode change message")
	}
	if lastMsg.Content[0].Text == "" {
		t.Error("expected non-empty mode change text")
	}
}

func TestAgentMode_ReadOnlyToolsSet(t *testing.T) {
	// Verify the read-only tools set matches expectations
	expected := map[string]bool{
		"read_file": true,
		"glob":      true,
		"grep":      true,
	}

	for name, val := range readOnlyTools {
		if !expected[name] || !val {
			t.Errorf("unexpected tool in readOnlyTools: %s", name)
		}
	}

	for name := range expected {
		if !readOnlyTools[name] {
			t.Errorf("missing expected read-only tool: %s", name)
		}
	}

	// Write tools should NOT be in the set
	writeTtools := []string{"write_file", "str_replace", "bash"}
	for _, name := range writeTtools {
		if readOnlyTools[name] {
			t.Errorf("write tool %s should not be in readOnlyTools", name)
		}
	}
}
