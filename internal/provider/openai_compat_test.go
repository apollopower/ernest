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

func TestOpenAICompat_BuildRequestBody(t *testing.T) {
	o := NewOpenAICompat("test", "key", "gpt-4.1", "https://api.example.com/v1")

	messages := []Message{
		{Role: RoleUser, Content: []ContentBlock{{Type: "text", Text: "Hello"}}},
	}
	tools := []ToolDef{
		{Name: "read_file", Description: "Read a file", InputSchema: map[string]any{"type": "object"}},
	}

	body, err := o.buildRequestBody("You are helpful.", messages, tools)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var req openAIRequest
	json.Unmarshal(body, &req)

	if req.Model != "gpt-4.1" {
		t.Errorf("expected model gpt-4.1, got %q", req.Model)
	}
	if !req.Stream {
		t.Error("expected stream=true")
	}
	// System prompt + user message = 2
	if len(req.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(req.Messages))
	}
	if req.Messages[0].Role != "system" {
		t.Errorf("expected system message first, got %q", req.Messages[0].Role)
	}
	if len(req.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(req.Tools))
	}
	if req.Tools[0].Function.Name != "read_file" {
		t.Errorf("expected tool name read_file, got %q", req.Tools[0].Function.Name)
	}
}

func TestOpenAICompat_MessageConversion(t *testing.T) {
	// Test tool_use conversion
	msg := Message{
		Role: RoleAssistant,
		Content: []ContentBlock{
			{Type: "text", Text: "Let me read that."},
			{Type: "tool_use", ToolUseID: "call_1", ToolName: "read_file", ToolInput: map[string]any{"file_path": "/tmp/test.txt"}},
		},
	}

	oaiMsgs := toOpenAIMessage(msg)
	if len(oaiMsgs) != 1 {
		t.Fatalf("expected 1 OpenAI message, got %d", len(oaiMsgs))
	}
	if oaiMsgs[0].Role != "assistant" {
		t.Errorf("expected assistant role, got %q", oaiMsgs[0].Role)
	}
	if len(oaiMsgs[0].ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(oaiMsgs[0].ToolCalls))
	}
	if oaiMsgs[0].ToolCalls[0].Function.Name != "read_file" {
		t.Errorf("expected read_file, got %q", oaiMsgs[0].ToolCalls[0].Function.Name)
	}
}

func TestOpenAICompat_ToolResultConversion(t *testing.T) {
	msg := Message{
		Role: RoleUser,
		Content: []ContentBlock{
			{Type: "tool_result", ToolUseID: "call_1", Content: "file contents here"},
		},
	}

	oaiMsgs := toOpenAIMessage(msg)
	if len(oaiMsgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(oaiMsgs))
	}
	if oaiMsgs[0].Role != "tool" {
		t.Errorf("expected tool role, got %q", oaiMsgs[0].Role)
	}
	if oaiMsgs[0].ToolCallID != "call_1" {
		t.Errorf("expected tool_call_id call_1, got %q", oaiMsgs[0].ToolCallID)
	}
}

func TestOpenAICompat_ParseSSE_TextStreaming(t *testing.T) {
	sseData := `data: {"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}

data: {"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}

data: {"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":null}]}

data: {"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: [DONE]
`

	o := NewOpenAICompat("test", "key", "test-model", "")
	ch := make(chan StreamEvent, 64)
	body := io.NopCloser(strings.NewReader(sseData))

	go o.parseSSE(context.Background(), body, ch)

	var textDeltas []string
	var gotDone bool
	for evt := range ch {
		switch evt.Type {
		case "text_delta":
			textDeltas = append(textDeltas, evt.Text)
		case "done":
			gotDone = true
		}
	}

	if !gotDone {
		t.Error("missing done event")
	}
	combined := strings.Join(textDeltas, "")
	if combined != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", combined)
	}
}

func TestOpenAICompat_ParseSSE_ToolCalls(t *testing.T) {
	sseData := `data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read_file","arguments":""}}]},"finish_reason":null}]}

data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"file"}}]},"finish_reason":null}]}

data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"_path\": \"/tmp/test.txt\"}"}}]},"finish_reason":null}]}

data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}

data: [DONE]
`

	o := NewOpenAICompat("test", "key", "test-model", "")
	ch := make(chan StreamEvent, 64)
	body := io.NopCloser(strings.NewReader(sseData))

	go o.parseSSE(context.Background(), body, ch)

	var gotToolStart, gotToolInput, gotBlockStop, gotDone bool
	var toolName, toolInput string

	for evt := range ch {
		switch evt.Type {
		case "tool_use_start":
			gotToolStart = true
			toolName = evt.ToolName
		case "tool_input_delta":
			gotToolInput = true
			toolInput = evt.ToolInput
		case "content_block_stop":
			gotBlockStop = true
		case "done":
			gotDone = true
		}
	}

	if !gotToolStart {
		t.Error("missing tool_use_start")
	}
	if toolName != "read_file" {
		t.Errorf("expected tool name read_file, got %q", toolName)
	}
	if !gotToolInput {
		t.Error("missing tool_input_delta")
	}
	if !strings.Contains(toolInput, "file_path") {
		t.Errorf("expected file_path in tool input, got %q", toolInput)
	}
	if !gotBlockStop {
		t.Error("missing content_block_stop")
	}
	if !gotDone {
		t.Error("missing done event")
	}
}

func TestOpenAICompat_NoAPIKey(t *testing.T) {
	o := NewOpenAICompat("test", "", "model", "")
	_, err := o.Stream(context.Background(), "", nil, nil)
	if err == nil {
		t.Error("expected error for empty API key")
	}
}

func TestOpenAICompat_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error": {"message": "invalid api key"}}`))
	}))
	defer server.Close()

	o := NewOpenAICompat("test", "bad-key", "model", server.URL)
	o.client = server.Client()

	messages := []Message{
		{Role: RoleUser, Content: []ContentBlock{{Type: "text", Text: "Hi"}}},
	}

	_, err := o.Stream(context.Background(), "", messages, nil)
	if err == nil {
		t.Fatal("expected error for 401 status")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("expected 401 in error, got: %v", err)
	}
}
