package agent

import (
	"context"
	"encoding/json"
	"ernest/internal/config"
	"ernest/internal/provider"
	"ernest/internal/tools"
	"testing"
	"time"
)

// mockProvider implements provider.Provider for testing.
type mockProvider struct {
	name      string
	events    []provider.StreamEvent
	callCount int // tracks how many times Stream was called
}

func (m *mockProvider) Name() string  { return m.name }
func (m *mockProvider) Healthy() bool { return true }

func (m *mockProvider) Stream(ctx context.Context, systemPrompt string, messages []provider.Message, toolDefs []provider.ToolDef) (<-chan provider.StreamEvent, error) {
	m.callCount++
	ch := make(chan provider.StreamEvent, len(m.events))
	for _, evt := range m.events {
		ch <- evt
	}
	close(ch)
	return ch, nil
}

// multiTurnProvider returns different events on successive Stream calls.
type multiTurnProvider struct {
	name   string
	turns  [][]provider.StreamEvent
	call   int
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

func TestAgent_TextOnlyResponse(t *testing.T) {
	mp := &mockProvider{
		name: "test",
		events: []provider.StreamEvent{
			{Type: "text_delta", Text: "Hello "},
			{Type: "text_delta", Text: "world"},
			{Type: "done"},
		},
	}

	router := provider.NewRouter([]provider.Provider{mp}, 30*time.Second)
	a := New(router, nil, &config.ClaudeConfig{})

	events := a.Run(context.Background(), "Hi")

	var text string
	var gotDone bool
	var gotProvider bool

	for evt := range events {
		switch evt.Type {
		case "text":
			text += evt.Text
		case "done":
			gotDone = true
		case "provider_switch":
			gotProvider = true
			if evt.ProviderName != "test" {
				t.Errorf("expected provider 'test', got %q", evt.ProviderName)
			}
		}
	}

	if !gotProvider {
		t.Error("missing provider_switch event")
	}
	if !gotDone {
		t.Error("missing done event")
	}
	if text != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", text)
	}
}

func TestAgent_ConversationHistory(t *testing.T) {
	mp := &mockProvider{
		name: "test",
		events: []provider.StreamEvent{
			{Type: "text_delta", Text: "Response"},
			{Type: "done"},
		},
	}

	router := provider.NewRouter([]provider.Provider{mp}, 30*time.Second)
	a := New(router, nil, &config.ClaudeConfig{SystemPrompt: "Be helpful"})

	// First turn
	for range a.Run(context.Background(), "First message") {
	}

	// Second turn
	for range a.Run(context.Background(), "Second message") {
	}

	// History should have 4 messages: user, assistant, user, assistant
	if len(a.history) != 4 {
		t.Fatalf("expected 4 history messages, got %d", len(a.history))
	}
	if string(a.history[0].Role) != "user" {
		t.Error("expected first message to be user")
	}
	if string(a.history[1].Role) != "assistant" {
		t.Error("expected second message to be assistant")
	}
	if a.history[0].Content[0].Text != "First message" {
		t.Errorf("expected 'First message', got %q", a.history[0].Content[0].Text)
	}
}

func TestAgent_ErrorEvent(t *testing.T) {
	mp := &mockProvider{
		name: "test",
		events: []provider.StreamEvent{
			{Type: "text_delta", Text: "Partial"},
			{Type: "error", Error: context.DeadlineExceeded},
		},
	}

	router := provider.NewRouter([]provider.Provider{mp}, 30*time.Second)
	a := New(router, nil, &config.ClaudeConfig{})

	var gotError bool
	var text string

	for evt := range a.Run(context.Background(), "Hi") {
		switch evt.Type {
		case "text":
			text += evt.Text
		case "error":
			gotError = true
		}
	}

	if !gotError {
		t.Error("expected error event")
	}
	if text != "Partial" {
		t.Errorf("expected partial text 'Partial', got %q", text)
	}
}

func TestAgent_ContextCancellation(t *testing.T) {
	mp := &mockProvider{
		name: "test",
		events: []provider.StreamEvent{
			{Type: "text_delta", Text: "Hello"},
			{Type: "done"},
		},
	}

	router := provider.NewRouter([]provider.Provider{mp}, 30*time.Second)
	a := New(router, nil, &config.ClaudeConfig{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	events := a.Run(ctx, "Hi")

	// Drain events — should complete without hanging
	for range events {
	}
}

// mockTool implements tools.Tool for testing.
type mockTool struct {
	name                 string
	result               string
	err                  error
	requiresConfirmation bool
}

func (t *mockTool) Name() string                                                  { return t.name }
func (t *mockTool) Description() string                                           { return "mock" }
func (t *mockTool) InputSchema() map[string]any                                   { return map[string]any{"type": "object"} }
func (t *mockTool) RequiresConfirmation(_ json.RawMessage) bool                   { return t.requiresConfirmation }
func (t *mockTool) Execute(_ context.Context, _ json.RawMessage) (string, error)  { return t.result, t.err }

func TestAgent_ToolCallAndExecution(t *testing.T) {
	// Turn 1: model returns text + tool_use, Turn 2: model returns text only
	mp := &multiTurnProvider{
		name: "test",
		turns: [][]provider.StreamEvent{
			// Turn 1: text + tool use
			{
				{Type: "text_delta", Text: "Let me read that file."},
				{Type: "tool_use_start", ToolUseID: "call_1", ToolName: "read_file"},
				{Type: "tool_input_delta", ToolInput: `{"file_path": "/tmp/test.txt"}`},
				{Type: "content_block_stop"},
				{Type: "message_delta", StopReason: "tool_use"},
				{Type: "done"},
			},
			// Turn 2: text only response after seeing tool result
			{
				{Type: "text_delta", Text: "The file contains hello."},
				{Type: "done"},
			},
		},
	}

	registry := tools.NewRegistry(&mockTool{name: "read_file", result: "hello world"})
	router := provider.NewRouter([]provider.Provider{mp}, 30*time.Second)
	a := New(router, registry, &config.ClaudeConfig{})

	var events []AgentEvent
	for evt := range a.Run(context.Background(), "Read /tmp/test.txt") {
		events = append(events, evt)
	}

	// Verify we got: provider_switch, text, tool_call, tool_result, text, done
	var gotToolCall, gotToolResult, gotDone bool
	var texts []string

	for _, evt := range events {
		switch evt.Type {
		case "text":
			texts = append(texts, evt.Text)
		case "tool_call":
			gotToolCall = true
			if evt.ToolName != "read_file" {
				t.Errorf("expected tool name 'read_file', got %q", evt.ToolName)
			}
		case "tool_result":
			gotToolResult = true
			if evt.ToolResult != "hello world" {
				t.Errorf("expected tool result 'hello world', got %q", evt.ToolResult)
			}
		case "done":
			gotDone = true
		}
	}

	if !gotToolCall {
		t.Error("missing tool_call event")
	}
	if !gotToolResult {
		t.Error("missing tool_result event")
	}
	if !gotDone {
		t.Error("missing done event")
	}
	if mp.call != 2 {
		t.Errorf("expected 2 Stream calls (tool loop), got %d", mp.call)
	}

	// Verify history: user, assistant (with tool_use), user (with tool_result), assistant (text)
	if len(a.history) != 4 {
		t.Fatalf("expected 4 history messages, got %d", len(a.history))
	}
	if string(a.history[0].Role) != "user" {
		t.Errorf("history[0]: expected user, got %s", a.history[0].Role)
	}
	if string(a.history[1].Role) != "assistant" {
		t.Errorf("history[1]: expected assistant, got %s", a.history[1].Role)
	}
	// Assistant message should have both text and tool_use blocks
	hasToolUse := false
	for _, block := range a.history[1].Content {
		if block.Type == "tool_use" {
			hasToolUse = true
		}
	}
	if !hasToolUse {
		t.Error("history[1]: expected tool_use content block in assistant message")
	}
	// Tool result message
	if string(a.history[2].Role) != "user" {
		t.Errorf("history[2]: expected user (tool result), got %s", a.history[2].Role)
	}
	if len(a.history[2].Content) == 0 || a.history[2].Content[0].Type != "tool_result" {
		t.Error("history[2]: expected tool_result content block")
	}
}

func TestAgent_UnknownTool(t *testing.T) {
	mp := &multiTurnProvider{
		name: "test",
		turns: [][]provider.StreamEvent{
			{
				{Type: "tool_use_start", ToolUseID: "call_1", ToolName: "unknown_tool"},
				{Type: "tool_input_delta", ToolInput: `{}`},
				{Type: "content_block_stop"},
				{Type: "message_delta", StopReason: "tool_use"},
				{Type: "done"},
			},
			{
				{Type: "text_delta", Text: "Sorry, I don't have that tool."},
				{Type: "done"},
			},
		},
	}

	registry := tools.NewRegistry(&mockTool{name: "read_file", result: "ok"})
	router := provider.NewRouter([]provider.Provider{mp}, 30*time.Second)
	a := New(router, registry, &config.ClaudeConfig{})

	var gotToolResult bool
	for evt := range a.Run(context.Background(), "Use unknown tool") {
		if evt.Type == "tool_result" {
			gotToolResult = true
			if evt.ToolResult != "error: unknown tool: unknown_tool" {
				t.Errorf("expected unknown tool error, got %q", evt.ToolResult)
			}
		}
	}

	if !gotToolResult {
		t.Error("missing tool_result event for unknown tool")
	}
}

func TestAgent_ToolExecutionError(t *testing.T) {
	mp := &multiTurnProvider{
		name: "test",
		turns: [][]provider.StreamEvent{
			{
				{Type: "tool_use_start", ToolUseID: "call_1", ToolName: "failing_tool"},
				{Type: "tool_input_delta", ToolInput: `{}`},
				{Type: "content_block_stop"},
				{Type: "message_delta", StopReason: "tool_use"},
				{Type: "done"},
			},
			{
				{Type: "text_delta", Text: "That failed."},
				{Type: "done"},
			},
		},
	}

	registry := tools.NewRegistry(&mockTool{
		name: "failing_tool",
		err:  context.DeadlineExceeded,
	})
	router := provider.NewRouter([]provider.Provider{mp}, 30*time.Second)
	a := New(router, registry, &config.ClaudeConfig{})

	var gotToolResult bool
	for evt := range a.Run(context.Background(), "Run failing tool") {
		if evt.Type == "tool_result" {
			gotToolResult = true
			if evt.ToolResult != "error: context deadline exceeded" {
				t.Errorf("expected deadline error, got %q", evt.ToolResult)
			}
		}
	}

	if !gotToolResult {
		t.Error("missing tool_result event for failing tool")
	}
}

func TestAgent_MultipleToolCalls(t *testing.T) {
	mp := &multiTurnProvider{
		name: "test",
		turns: [][]provider.StreamEvent{
			// Two tool calls in one response
			{
				{Type: "tool_use_start", ToolUseID: "call_1", ToolName: "read_file"},
				{Type: "tool_input_delta", ToolInput: `{"file_path": "a.txt"}`},
				{Type: "content_block_stop"},
				{Type: "tool_use_start", ToolUseID: "call_2", ToolName: "read_file"},
				{Type: "tool_input_delta", ToolInput: `{"file_path": "b.txt"}`},
				{Type: "content_block_stop"},
				{Type: "message_delta", StopReason: "tool_use"},
				{Type: "done"},
			},
			{
				{Type: "text_delta", Text: "Done reading both files."},
				{Type: "done"},
			},
		},
	}

	registry := tools.NewRegistry(&mockTool{name: "read_file", result: "content"})
	router := provider.NewRouter([]provider.Provider{mp}, 30*time.Second)
	a := New(router, registry, &config.ClaudeConfig{})

	toolCallCount := 0
	for evt := range a.Run(context.Background(), "Read both files") {
		if evt.Type == "tool_call" {
			toolCallCount++
		}
	}

	if toolCallCount != 2 {
		t.Errorf("expected 2 tool calls, got %d", toolCallCount)
	}
}

func TestAgent_ToolConfirmation_Approved(t *testing.T) {
	mp := &multiTurnProvider{
		name: "test",
		turns: [][]provider.StreamEvent{
			{
				{Type: "tool_use_start", ToolUseID: "call_1", ToolName: "bash"},
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
	a := New(router, registry, &config.ClaudeConfig{})

	events := a.Run(context.Background(), "Run echo hi")

	var gotConfirm, gotResult, gotDone bool
	for evt := range events {
		switch evt.Type {
		case "tool_confirm":
			gotConfirm = true
			// Simulate user pressing "y"
			go a.ResolveTool(evt.ToolUseID, true)
		case "tool_result":
			gotResult = true
			if evt.ToolResult != "hi" {
				t.Errorf("expected result 'hi', got %q", evt.ToolResult)
			}
		case "done":
			gotDone = true
		}
	}

	if !gotConfirm {
		t.Error("missing tool_confirm event")
	}
	if !gotResult {
		t.Error("missing tool_result event")
	}
	if !gotDone {
		t.Error("missing done event")
	}
}

func TestAgent_ToolConfirmation_Denied(t *testing.T) {
	mp := &multiTurnProvider{
		name: "test",
		turns: [][]provider.StreamEvent{
			{
				{Type: "tool_use_start", ToolUseID: "call_1", ToolName: "bash"},
				{Type: "tool_input_delta", ToolInput: `{"command": "rm -rf /"}`},
				{Type: "content_block_stop"},
				{Type: "message_delta", StopReason: "tool_use"},
				{Type: "done"},
			},
			{
				{Type: "text_delta", Text: "Understood, I won't do that."},
				{Type: "done"},
			},
		},
	}

	registry := tools.NewRegistry(&mockTool{
		name:                 "bash",
		result:               "should not execute",
		requiresConfirmation: true,
	})
	router := provider.NewRouter([]provider.Provider{mp}, 30*time.Second)
	a := New(router, registry, &config.ClaudeConfig{})

	events := a.Run(context.Background(), "Delete everything")

	var gotConfirm, gotResult bool
	for evt := range events {
		switch evt.Type {
		case "tool_confirm":
			gotConfirm = true
			go a.ResolveTool(evt.ToolUseID, false)
		case "tool_result":
			gotResult = true
			if evt.ToolResult != "error: tool use denied by user" {
				t.Errorf("expected denial error, got %q", evt.ToolResult)
			}
		}
	}

	if !gotConfirm {
		t.Error("missing tool_confirm event")
	}
	if !gotResult {
		t.Error("missing tool_result event (with denial error)")
	}
}

func TestAgent_ToolPermission_AutoAllowed(t *testing.T) {
	mp := &multiTurnProvider{
		name: "test",
		turns: [][]provider.StreamEvent{
			{
				{Type: "tool_use_start", ToolUseID: "call_1", ToolName: "bash"},
				{Type: "tool_input_delta", ToolInput: `{"command": "echo ok"}`},
				{Type: "content_block_stop"},
				{Type: "message_delta", StopReason: "tool_use"},
				{Type: "done"},
			},
			{
				{Type: "text_delta", Text: "Done."},
				{Type: "done"},
			},
		},
	}

	registry := tools.NewRegistry(&mockTool{
		name:                 "bash",
		result:               "ok",
		requiresConfirmation: true,
	})
	router := provider.NewRouter([]provider.Provider{mp}, 30*time.Second)
	// bash is in allowedTools — should skip confirmation
	a := New(router, registry, &config.ClaudeConfig{AllowedTools: []string{"bash"}})

	var gotConfirm bool
	var gotResult bool
	for evt := range a.Run(context.Background(), "Run echo ok") {
		if evt.Type == "tool_confirm" {
			gotConfirm = true
		}
		if evt.Type == "tool_result" {
			gotResult = true
			if evt.ToolResult != "ok" {
				t.Errorf("expected 'ok', got %q", evt.ToolResult)
			}
		}
	}

	if gotConfirm {
		t.Error("should NOT get tool_confirm when tool is in allowedTools")
	}
	if !gotResult {
		t.Error("missing tool_result event")
	}
}

func TestAgent_ToolPermission_Denied(t *testing.T) {
	mp := &multiTurnProvider{
		name: "test",
		turns: [][]provider.StreamEvent{
			{
				{Type: "tool_use_start", ToolUseID: "call_1", ToolName: "bash"},
				{Type: "tool_input_delta", ToolInput: `{}`},
				{Type: "content_block_stop"},
				{Type: "message_delta", StopReason: "tool_use"},
				{Type: "done"},
			},
			{
				{Type: "text_delta", Text: "That tool is denied."},
				{Type: "done"},
			},
		},
	}

	registry := tools.NewRegistry(&mockTool{
		name:                 "bash",
		result:               "should not execute",
		requiresConfirmation: true,
	})
	router := provider.NewRouter([]provider.Provider{mp}, 30*time.Second)
	// bash is in deniedTools — should auto-deny without confirmation
	a := New(router, registry, &config.ClaudeConfig{DeniedTools: []string{"bash"}})

	var gotConfirm bool
	var gotResult bool
	for evt := range a.Run(context.Background(), "Run bash") {
		if evt.Type == "tool_confirm" {
			gotConfirm = true
		}
		if evt.Type == "tool_result" {
			gotResult = true
			if evt.ToolResult != "error: tool bash is denied by settings" {
				t.Errorf("expected denied error, got %q", evt.ToolResult)
			}
		}
	}

	if gotConfirm {
		t.Error("should NOT get tool_confirm when tool is denied")
	}
	if !gotResult {
		t.Error("missing tool_result event (with denied error)")
	}
}

func TestAgent_AllowToolAlways(t *testing.T) {
	// Two turns: first call requires confirmation (user presses "always"),
	// second call should skip confirmation because the tool is now allowed.
	mp := &multiTurnProvider{
		name: "test",
		turns: [][]provider.StreamEvent{
			// Turn 1: tool call
			{
				{Type: "tool_use_start", ToolUseID: "call_1", ToolName: "bash"},
				{Type: "tool_input_delta", ToolInput: `{"command": "echo first"}`},
				{Type: "content_block_stop"},
				{Type: "message_delta", StopReason: "tool_use"},
				{Type: "done"},
			},
			// Turn 1 response
			{
				{Type: "text_delta", Text: "First done."},
				{Type: "done"},
			},
		},
	}

	dir := t.TempDir()
	registry := tools.NewRegistry(&mockTool{
		name:                 "bash",
		result:               "output",
		requiresConfirmation: true,
	})
	router := provider.NewRouter([]provider.Provider{mp}, 30*time.Second)
	a := New(router, registry, &config.ClaudeConfig{ProjectDir: dir})

	// First run: should get confirmation, use AllowToolAlways
	var gotConfirm bool
	for evt := range a.Run(context.Background(), "Run echo first") {
		if evt.Type == "tool_confirm" {
			gotConfirm = true
			go func() {
				a.AllowToolAlways(evt.ToolUseID, evt.ToolName)
			}()
		}
	}
	if !gotConfirm {
		t.Fatal("expected tool_confirm on first call")
	}

	// Verify in-memory permission updated
	if a.permissions.Check("bash") != PermissionAllowed {
		t.Error("expected bash to be allowed in-memory after AllowToolAlways")
	}

	// Reset provider for second run
	mp.call = 0
	mp.turns = [][]provider.StreamEvent{
		{
			{Type: "tool_use_start", ToolUseID: "call_2", ToolName: "bash"},
			{Type: "tool_input_delta", ToolInput: `{"command": "echo second"}`},
			{Type: "content_block_stop"},
			{Type: "message_delta", StopReason: "tool_use"},
			{Type: "done"},
		},
		{
			{Type: "text_delta", Text: "Second done."},
			{Type: "done"},
		},
	}

	// Second run: should NOT get confirmation
	gotConfirm = false
	for evt := range a.Run(context.Background(), "Run echo second") {
		if evt.Type == "tool_confirm" {
			gotConfirm = true
		}
	}
	if gotConfirm {
		t.Error("should NOT get tool_confirm after AllowToolAlways")
	}
}
