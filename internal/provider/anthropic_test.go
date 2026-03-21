package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBuildRequestBody_BasicMessage(t *testing.T) {
	a := NewAnthropic("test-key", "claude-opus-4-6")

	messages := []Message{
		{
			Role: RoleUser,
			Content: []ContentBlock{
				{Type: "text", Text: "Hello"},
			},
		},
	}

	body, err := a.buildRequestBody("You are helpful.", messages, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var req anthropicRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if req.Model != "claude-opus-4-6" {
		t.Errorf("expected model 'claude-opus-4-6', got %q", req.Model)
	}
	if req.MaxTokens != 16384 {
		t.Errorf("expected max_tokens 16384, got %d", req.MaxTokens)
	}
	if !req.Stream {
		t.Error("expected stream to be true")
	}
	if req.System != "You are helpful." {
		t.Errorf("expected system prompt, got %q", req.System)
	}
	if len(req.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(req.Messages))
	}
	if req.Messages[0].Role != "user" {
		t.Errorf("expected role 'user', got %q", req.Messages[0].Role)
	}
}

func TestBuildRequestBody_EmptySystemPrompt(t *testing.T) {
	a := NewAnthropic("test-key", "claude-opus-4-6")

	messages := []Message{
		{Role: RoleUser, Content: []ContentBlock{{Type: "text", Text: "Hi"}}},
	}

	body, err := a.buildRequestBody("", messages, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var raw map[string]any
	json.Unmarshal(body, &raw)

	if _, exists := raw["system"]; exists {
		t.Error("expected system field to be omitted when empty")
	}
}

func TestBuildRequestBody_WithTools(t *testing.T) {
	a := NewAnthropic("test-key", "claude-opus-4-6")

	messages := []Message{
		{Role: RoleUser, Content: []ContentBlock{{Type: "text", Text: "Read file"}}},
	}
	tools := []ToolDef{
		{Name: "read_file", Description: "Read a file", InputSchema: map[string]any{"type": "object"}},
	}

	body, err := a.buildRequestBody("", messages, tools)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var req anthropicRequest
	json.Unmarshal(body, &req)

	if len(req.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(req.Tools))
	}
	if req.Tools[0].Name != "read_file" {
		t.Errorf("expected tool name 'read_file', got %q", req.Tools[0].Name)
	}
}

func TestToAnthropicContent(t *testing.T) {
	tests := []struct {
		name  string
		block ContentBlock
		key   string
		value string
	}{
		{"text", ContentBlock{Type: "text", Text: "hello"}, "text", "hello"},
		{"tool_use", ContentBlock{Type: "tool_use", ToolUseID: "id1", ToolName: "bash"}, "name", "bash"},
		{"tool_result", ContentBlock{Type: "tool_result", ToolUseID: "id1", Content: "output"}, "content", "output"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := toAnthropicContent(tt.block)
			if result["type"] != tt.block.Type {
				t.Errorf("expected type %q, got %q", tt.block.Type, result["type"])
			}
		})
	}
}

func TestParseSSE_TextStreaming(t *testing.T) {
	sseData := `event: message_start
data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-opus-4-6","usage":{"input_tokens":25,"output_tokens":1}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":12}}

event: message_stop
data: {"type":"message_stop"}

`

	a := NewAnthropic("test-key", "test-model")
	ch := make(chan StreamEvent, 64)
	body := io.NopCloser(strings.NewReader(sseData))

	go a.parseSSE(context.Background(), body, ch)

	var events []StreamEvent
	for evt := range ch {
		events = append(events, evt)
	}

	// Expect: message_start, text_delta("Hello"), text_delta(" world"), message_delta, done
	var textDeltas []string
	var gotDone bool
	var gotMessageStart bool

	for _, evt := range events {
		switch evt.Type {
		case "message_start":
			gotMessageStart = true
			if evt.Usage == nil || evt.Usage.InputTokens != 25 {
				t.Errorf("expected input_tokens=25, got %+v", evt.Usage)
			}
		case "text_delta":
			textDeltas = append(textDeltas, evt.Text)
		case "message_delta":
			if evt.Usage == nil || evt.Usage.OutputTokens != 12 {
				t.Errorf("expected output_tokens=12, got %+v", evt.Usage)
			}
		case "done":
			gotDone = true
		}
	}

	if !gotMessageStart {
		t.Error("missing message_start event")
	}
	if !gotDone {
		t.Error("missing done event")
	}
	combined := strings.Join(textDeltas, "")
	if combined != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", combined)
	}
}

func TestParseSSE_ErrorEvent(t *testing.T) {
	sseData := `event: error
data: {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}

`

	a := NewAnthropic("test-key", "test-model")
	ch := make(chan StreamEvent, 64)
	body := io.NopCloser(strings.NewReader(sseData))

	go a.parseSSE(context.Background(), body, ch)

	var gotError bool
	for evt := range ch {
		if evt.Type == "error" && evt.Error != nil {
			if strings.Contains(evt.Error.Error(), "Overloaded") {
				gotError = true
			}
		}
	}

	if !gotError {
		t.Error("expected error event with 'Overloaded' message")
	}
}

func TestParseSSE_PingIgnored(t *testing.T) {
	sseData := `event: ping
data: {"type":"ping"}

event: message_stop
data: {"type":"message_stop"}

`

	a := NewAnthropic("test-key", "test-model")
	ch := make(chan StreamEvent, 64)
	body := io.NopCloser(strings.NewReader(sseData))

	go a.parseSSE(context.Background(), body, ch)

	var events []StreamEvent
	for evt := range ch {
		events = append(events, evt)
	}

	// Should only get done (from message_stop), no ping events
	if len(events) != 1 || events[0].Type != "done" {
		t.Errorf("expected only done event, got %d events: %+v", len(events), events)
	}
}

func TestStream_NoAPIKey(t *testing.T) {
	a := NewAnthropic("", "test-model")
	_, err := a.Stream(context.Background(), "", nil, nil)
	if err == nil {
		t.Error("expected error for empty API key")
	}
	if !strings.Contains(err.Error(), "ANTHROPIC_API_KEY not set") {
		t.Errorf("expected API key error, got: %v", err)
	}
}

func TestStream_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": "internal server error"}`))
	}))
	defer server.Close()

	a := NewAnthropic("test-key", "test-model")
	a.apiURL = server.URL
	a.client = server.Client()

	messages := []Message{
		{Role: RoleUser, Content: []ContentBlock{{Type: "text", Text: "Hi"}}},
	}

	_, err := a.Stream(context.Background(), "", messages, nil)
	if err == nil {
		t.Fatal("expected error for non-200 status")
	}
	if !strings.Contains(err.Error(), "status 500") {
		t.Errorf("expected status 500 in error, got: %v", err)
	}
}
