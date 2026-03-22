package headless

import (
	"bytes"
	"context"
	"encoding/json"
	"ernest/internal/agent"
	"ernest/internal/config"
	"ernest/internal/provider"
	"ernest/internal/session"
	"ernest/internal/tools"
	"strings"
	"testing"
	"time"
)

// mockProvider returns canned events.
type mockProvider struct {
	name   string
	events []provider.StreamEvent
}

func (m *mockProvider) Name() string  { return m.name }
func (m *mockProvider) Healthy() bool { return true }
func (m *mockProvider) Stream(ctx context.Context, systemPrompt string, messages []provider.Message, toolDefs []provider.ToolDef) (<-chan provider.StreamEvent, error) {
	ch := make(chan provider.StreamEvent, len(m.events))
	for _, evt := range m.events {
		ch <- evt
	}
	close(ch)
	return ch, nil
}

// multiTurnProvider returns different events on each call.
type multiTurnProvider struct {
	name  string
	turns [][]provider.StreamEvent
	call  int
}

func (m *multiTurnProvider) Name() string  { return m.name }
func (m *multiTurnProvider) Healthy() bool { return true }
func (m *multiTurnProvider) Stream(ctx context.Context, systemPrompt string, messages []provider.Message, toolDefs []provider.ToolDef) (<-chan provider.StreamEvent, error) {
	events := m.turns[m.call%len(m.turns)]
	m.call++
	ch := make(chan provider.StreamEvent, len(events))
	for _, evt := range events {
		ch <- evt
	}
	close(ch)
	return ch, nil
}

func newTestSession(t *testing.T) *session.Session {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("APPDATA", dir)
	t.Setenv("HOME", dir)
	return session.New("/test/project")
}

func TestRunPrompt_TextOutput(t *testing.T) {
	mp := &mockProvider{
		name: "test",
		events: []provider.StreamEvent{
			{Type: "text_delta", Text: "Hello "},
			{Type: "text_delta", Text: "world"},
			{Type: "done"},
		},
	}

	router := provider.NewRouter([]provider.Provider{mp}, 30*time.Second)
	a := agent.New(router, nil, &config.ClaudeConfig{}, 0, false)
	sess := newTestSession(t)

	var buf bytes.Buffer
	runner := NewRunner(a, sess, FormatText, &buf)

	err := runner.RunPrompt(context.Background(), "Hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Hello world") {
		t.Errorf("expected 'Hello world' in output, got %q", output)
	}
}

func TestRunPrompt_JSONOutput(t *testing.T) {
	mp := &mockProvider{
		name: "test",
		events: []provider.StreamEvent{
			{Type: "text_delta", Text: "Hello"},
			{Type: "done"},
		},
	}

	router := provider.NewRouter([]provider.Provider{mp}, 30*time.Second)
	a := agent.New(router, nil, &config.ClaudeConfig{}, 0, false)
	sess := newTestSession(t)

	var buf bytes.Buffer
	runner := NewRunner(a, sess, FormatJSON, &buf)

	err := runner.RunPrompt(context.Background(), "Hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Parse JSON lines
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 JSON lines, got %d: %v", len(lines), lines)
	}

	// First line should be session event with version
	var sessionEvt OutputEvent
	if err := json.Unmarshal([]byte(lines[0]), &sessionEvt); err != nil {
		t.Fatalf("failed to parse session event: %v", err)
	}
	if sessionEvt.Type != "session" {
		t.Errorf("expected first event type 'session', got %q", sessionEvt.Type)
	}
	if sessionEvt.Version != 1 {
		t.Errorf("expected version 1, got %d", sessionEvt.Version)
	}
	if sessionEvt.ID == "" {
		t.Error("expected session ID in session event")
	}

	// Last line should be done event
	var doneEvt OutputEvent
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &doneEvt); err != nil {
		t.Fatalf("failed to parse done event: %v", err)
	}
	if doneEvt.Type != "done" {
		t.Errorf("expected last event type 'done', got %q", doneEvt.Type)
	}
}

func TestRunPrompt_JSONToolEvents(t *testing.T) {
	mp := &multiTurnProvider{
		name: "test",
		turns: [][]provider.StreamEvent{
			{
				{Type: "tool_use_start", ToolUseID: "c1", ToolName: "read_file"},
				{Type: "tool_input_delta", ToolInput: `{"file_path": "/tmp/test.txt"}`},
				{Type: "content_block_stop"},
				{Type: "message_delta", StopReason: "tool_use"},
				{Type: "done"},
			},
			{
				{Type: "text_delta", Text: "File contents."},
				{Type: "done"},
			},
		},
	}

	registry := tools.NewRegistry(&mockTool{name: "read_file", result: "hello"})
	router := provider.NewRouter([]provider.Provider{mp}, 30*time.Second)
	a := agent.New(router, registry, &config.ClaudeConfig{}, 0, false)
	sess := newTestSession(t)

	var buf bytes.Buffer
	runner := NewRunner(a, sess, FormatJSON, &buf)

	if err := runner.RunPrompt(context.Background(), "read test.txt"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, `"type":"tool_call"`) {
		t.Error("expected tool_call event in JSON output")
	}
	if !strings.Contains(output, `"type":"tool_result"`) {
		t.Error("expected tool_result event in JSON output")
	}
}

func TestRunConversation_MultiTurn(t *testing.T) {
	mp := &mockProvider{
		name: "test",
		events: []provider.StreamEvent{
			{Type: "text_delta", Text: "Response"},
			{Type: "done"},
		},
	}

	router := provider.NewRouter([]provider.Provider{mp}, 30*time.Second)
	a := agent.New(router, nil, &config.ClaudeConfig{}, 0, false)
	sess := newTestSession(t)

	var buf bytes.Buffer
	runner := NewRunner(a, sess, FormatText, &buf)

	input := strings.NewReader("First prompt\nSecond prompt\n")
	err := runner.RunConversation(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	// Should have two responses
	if strings.Count(output, "Response") != 2 {
		t.Errorf("expected 2 responses, got output: %q", output)
	}
}

func TestRunPrompt_ToolDeniedHeadless(t *testing.T) {
	mp := &multiTurnProvider{
		name: "test",
		turns: [][]provider.StreamEvent{
			{
				{Type: "tool_use_start", ToolUseID: "c1", ToolName: "bash"},
				{Type: "tool_input_delta", ToolInput: `{"command": "echo hi"}`},
				{Type: "content_block_stop"},
				{Type: "message_delta", StopReason: "tool_use"},
				{Type: "done"},
			},
			{
				{Type: "text_delta", Text: "Tool was denied."},
				{Type: "done"},
			},
		},
	}

	registry := tools.NewRegistry(&mockTool{
		name:                 "bash",
		result:               "hi",
		requiresConfirmation: true,
	})
	router := provider.NewRouter([]provider.Provider{mp}, 30*time.Second)
	// No auto-approve — tool should be denied in headless
	a := agent.New(router, registry, &config.ClaudeConfig{}, 0, false)
	sess := newTestSession(t)

	var buf bytes.Buffer
	runner := NewRunner(a, sess, FormatJSON, &buf)

	// Tool denied returns an error
	err := runner.RunPrompt(context.Background(), "run echo hi")
	if err == nil {
		t.Log("note: RunPrompt returned nil error despite tool denial (model may handle gracefully)")
	}

	output := buf.String()
	if !strings.Contains(output, "denied") {
		t.Errorf("expected 'denied' in output, got: %s", output)
	}
}

func TestRunPrompt_AutoApprove(t *testing.T) {
	mp := &multiTurnProvider{
		name: "test",
		turns: [][]provider.StreamEvent{
			{
				{Type: "tool_use_start", ToolUseID: "c1", ToolName: "bash"},
				{Type: "tool_input_delta", ToolInput: `{"command": "echo hi"}`},
				{Type: "content_block_stop"},
				{Type: "message_delta", StopReason: "tool_use"},
				{Type: "done"},
			},
			{
				{Type: "text_delta", Text: "Command executed."},
				{Type: "done"},
			},
		},
	}

	registry := tools.NewRegistry(&mockTool{
		name:                 "bash",
		result:               "hi",
		requiresConfirmation: true,
	})
	router := provider.NewRouter([]provider.Provider{mp}, 30*time.Second)
	// Auto-approve enabled
	a := agent.New(router, registry, &config.ClaudeConfig{}, 0, true)
	sess := newTestSession(t)

	var buf bytes.Buffer
	runner := NewRunner(a, sess, FormatJSON, &buf)

	if err := runner.RunPrompt(context.Background(), "run echo hi"); err != nil {
		t.Fatalf("unexpected error with auto-approve: %v", err)
	}

	output := buf.String()
	if strings.Contains(output, "denied") {
		t.Error("tool should NOT be denied with auto-approve")
	}
	if !strings.Contains(output, `"type":"tool_result"`) {
		t.Error("expected tool_result in output with auto-approve")
	}
}

// mockTool for testing
type mockTool struct {
	name                 string
	result               string
	err                  error
	requiresConfirmation bool
}

func (t *mockTool) Name() string                                                         { return t.name }
func (t *mockTool) Description() string                                                  { return "mock" }
func (t *mockTool) InputSchema() map[string]any                                          { return map[string]any{"type": "object"} }
func (t *mockTool) RequiresConfirmation(_ json.RawMessage) bool                          { return t.requiresConfirmation }
func (t *mockTool) Execute(_ context.Context, _ json.RawMessage) (string, error)         { return t.result, t.err }
