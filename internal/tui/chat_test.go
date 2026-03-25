package tui

import (
	"ernest/internal/provider"
	"strings"
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
	if !strings.Contains(result[0].Content, "truncated") {
		t.Error("expected truncated indicator for long tool result")
	}
}

func TestFormatToolName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"read_file", "read_file"},
		{"bash", "bash"},
		{"mcp__sentry__search_issues", "sentry: search_issues"},
		{"mcp__github__create_pr", "github: create_pr"},
		{"mcp__db__query_table", "db: query_table"},
		// Tool names with __ preserved after server name
		{"mcp__server__my__tool", "server: my__tool"},
		// Edge cases
		{"mcp__", "mcp__"},
		{"mcp__server", "mcp__server"},
	}

	for _, tt := range tests {
		got := formatToolName(tt.input)
		if got != tt.want {
			t.Errorf("formatToolName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFindStableBlockBoundary(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    int
	}{
		{"empty", "", 0},
		{"no boundary", "hello world", 0},
		{"single paragraph", "hello\nworld", 0},
		{"two paragraphs", "hello\n\nworld", len("hello\n")},
		{"three paragraphs", "one\n\ntwo\n\nthree", len("one\n\ntwo\n")},
		{"code fence spans boundary", "```\ncode\n\nmore\n```\n\nafter", len("```\ncode\n\nmore\n```\n")},
		{"unclosed fence hides boundary", "```\ncode\n\nmore", 0},
	}
	for _, tt := range tests {
		got := findStableBlockBoundary(tt.content)
		if got != tt.want {
			t.Errorf("findStableBlockBoundary(%q) [%s] = %d, want %d", tt.content, tt.name, got, tt.want)
		}
	}
}

func TestSanitizePartialMarkdown(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		// No fences — unchanged
		{"hello world", "hello world"},
		// Closed fence — unchanged
		{"```go\nfmt.Println()\n```", "```go\nfmt.Println()\n```"},
		// Unclosed fence — gets closed
		{"```go\nfmt.Println()", "```go\nfmt.Println()\n```"},
		// Multiple fences on separate lines — unchanged when all closed
		{"```\na\n```\n```\nb\n```", "```\na\n```\n```\nb\n```"},
		// Second fence unclosed
		{"```\na\n```\n```\nb", "```\na\n```\n```\nb\n```"},
		// No content — unchanged
		{"", ""},
		// Plain backticks (not triple) — unchanged
		{"`code`", "`code`"},
	}
	for _, tt := range tests {
		got := sanitizePartialMarkdown(tt.input)
		if got != tt.want {
			t.Errorf("sanitizePartialMarkdown(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
