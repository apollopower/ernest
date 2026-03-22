package tui

import (
	"ernest/internal/provider"
	"testing"
)

func TestMessagesToChat_TextMessages(t *testing.T) {
	msgs := []provider.Message{
		{
			Role:    provider.RoleUser,
			Content: []provider.ContentBlock{{Type: "text", Text: "Hello"}},
		},
		{
			Role:    provider.RoleAssistant,
			Content: []provider.ContentBlock{{Type: "text", Text: "Hi there!"}},
		},
	}

	result := MessagesToChat(msgs)
	if len(result) != 2 {
		t.Fatalf("expected 2 chat messages, got %d", len(result))
	}
	if result[0].Role != "user" || result[0].Content != "Hello" {
		t.Errorf("expected user 'Hello', got %s %q", result[0].Role, result[0].Content)
	}
	if result[1].Role != "assistant" || result[1].Content != "Hi there!" {
		t.Errorf("expected assistant 'Hi there!', got %s %q", result[1].Role, result[1].Content)
	}
}

func TestMessagesToChat_ToolBlocks(t *testing.T) {
	msgs := []provider.Message{
		{
			Role: provider.RoleAssistant,
			Content: []provider.ContentBlock{
				{Type: "text", Text: "Let me read that."},
				{Type: "tool_use", ToolName: "read_file", ToolInput: map[string]any{"file_path": "/tmp/test.txt"}},
			},
		},
		{
			Role: provider.RoleUser,
			Content: []provider.ContentBlock{
				{Type: "tool_result", ToolUseID: "call_1", Content: "file contents here"},
			},
		},
		{
			Role: provider.RoleAssistant,
			Content: []provider.ContentBlock{
				{Type: "text", Text: "The file says: file contents here"},
			},
		},
	}

	result := MessagesToChat(msgs)
	// Expected: text, tool_call, tool_result, text = 4 chat messages
	if len(result) != 4 {
		t.Fatalf("expected 4 chat messages, got %d", len(result))
	}

	if result[0].Role != "assistant" || result[0].Content != "Let me read that." {
		t.Errorf("msg 0: expected assistant text, got %s %q", result[0].Role, result[0].Content)
	}
	if result[1].Role != "tool_call" || result[1].ToolName != "read_file" {
		t.Errorf("msg 1: expected tool_call read_file, got %s %s", result[1].Role, result[1].ToolName)
	}
	if result[2].Role != "tool_result" {
		t.Errorf("msg 2: expected tool_result, got %s", result[2].Role)
	}
	if result[3].Role != "assistant" {
		t.Errorf("msg 3: expected assistant, got %s", result[3].Role)
	}
}

func TestMessagesToChat_Empty(t *testing.T) {
	result := MessagesToChat(nil)
	if len(result) != 0 {
		t.Errorf("expected 0 messages for nil input, got %d", len(result))
	}
}

func TestMessagesToChat_LongToolInput(t *testing.T) {
	longInput := make(map[string]any)
	longInput["content"] = string(make([]byte, 500))

	msgs := []provider.Message{
		{
			Role: provider.RoleAssistant,
			Content: []provider.ContentBlock{
				{Type: "tool_use", ToolName: "write_file", ToolInput: longInput},
			},
		},
	}

	result := MessagesToChat(msgs)
	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}
	// Content should be truncated
	if len(result[0].Content) > 210 {
		t.Errorf("expected truncated content, got length %d", len(result[0].Content))
	}
}

func TestMessagesToChat_LongToolResult(t *testing.T) {
	var longContent string
	for i := 0; i < 100; i++ {
		longContent += "line content\n"
	}

	msgs := []provider.Message{
		{
			Role: provider.RoleUser,
			Content: []provider.ContentBlock{
				{Type: "tool_result", ToolUseID: "c1", Content: longContent},
			},
		},
	}

	result := MessagesToChat(msgs)
	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}
	if !contains(result[0].Content, "truncated") {
		t.Error("expected truncated indicator for long tool result")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
