package agent

import (
	"context"
	"ernest/internal/config"
	"ernest/internal/provider"
	"fmt"
	"strings"
	"testing"
)

func TestEstimateTokens_TextMessages(t *testing.T) {
	messages := []provider.Message{
		{
			Role: provider.RoleUser,
			Content: []provider.ContentBlock{
				{Type: "text", Text: "Hello, how are you?"}, // 19 chars / 4 = ~4 tokens
			},
		},
		{
			Role: provider.RoleAssistant,
			Content: []provider.ContentBlock{
				{Type: "text", Text: "I'm doing well, thanks for asking!"}, // 34 chars / 4 = ~8 tokens
			},
		},
	}

	tokens := EstimateTokens(messages)
	// 2 messages * 4 overhead + ~4 + ~8 = ~20
	if tokens < 10 || tokens > 30 {
		t.Errorf("expected ~20 tokens for short conversation, got %d", tokens)
	}
}

func TestEstimateTokens_ToolBlocks(t *testing.T) {
	messages := []provider.Message{
		{
			Role: provider.RoleAssistant,
			Content: []provider.ContentBlock{
				{Type: "text", Text: "Let me read that file."},
				{Type: "tool_use", ToolName: "read_file", ToolInput: map[string]any{"file_path": "/tmp/test.txt"}},
			},
		},
		{
			Role: provider.RoleUser,
			Content: []provider.ContentBlock{
				{Type: "tool_result", ToolUseID: "call_1", Content: "file contents here"},
			},
		},
	}

	tokens := EstimateTokens(messages)
	if tokens <= 0 {
		t.Error("expected positive token count")
	}
}

func TestEstimateTokens_Empty(t *testing.T) {
	tokens := EstimateTokens(nil)
	if tokens != 0 {
		t.Errorf("expected 0 for nil messages, got %d", tokens)
	}
}

func TestEstimateSystemPromptTokens(t *testing.T) {
	tokens := EstimateSystemPromptTokens("You are a helpful assistant.")
	if tokens <= 0 {
		t.Error("expected positive token count")
	}

	tokens = EstimateSystemPromptTokens("")
	if tokens != 0 {
		t.Errorf("expected 0 for empty prompt, got %d", tokens)
	}
}

func TestEstimateTokens_LargeConversation(t *testing.T) {
	// Build a conversation that should be roughly 1000 tokens
	var messages []provider.Message
	for i := 0; i < 20; i++ {
		// ~200 chars per message pair = ~50 tokens per pair = ~1000 tokens total
		messages = append(messages,
			provider.Message{
				Role:    provider.RoleUser,
				Content: []provider.ContentBlock{{Type: "text", Text: "This is a moderately long user message that contains about one hundred characters of text for testing."}},
			},
			provider.Message{
				Role:    provider.RoleAssistant,
				Content: []provider.ContentBlock{{Type: "text", Text: "This is a moderately long assistant response that also contains about one hundred characters of text."}},
			},
		)
	}

	tokens := EstimateTokens(messages)
	// Should be roughly 40 messages * (4 overhead + ~25 text tokens) = ~1160
	if tokens < 500 || tokens > 2000 {
		t.Errorf("expected ~1000 tokens for 20-exchange conversation, got %d", tokens)
	}
}

func TestNeedsCompaction(t *testing.T) {
	mp := &mockProvider{
		name:   "test",
		events: []provider.StreamEvent{{Type: "done"}},
	}
	router := provider.NewRouter([]provider.Provider{mp}, 0)

	// With low maxContextTokens and some history, should need compaction
	a := New(router, nil, &config.ClaudeConfig{}, 100, false, ModeBuild) // very low limit

	// Add messages that exceed 90% of 100 tokens (the compaction threshold)
	a.mu.Lock()
	for i := 0; i < 10; i++ {
		a.history = append(a.history, provider.Message{
			Role:    provider.RoleUser,
			Content: []provider.ContentBlock{{Type: "text", Text: "This is a long message to push tokens over the limit for testing purposes."}},
		})
	}
	a.mu.Unlock()

	if !a.NeedsCompaction() {
		t.Error("expected NeedsCompaction to be true")
	}
}

func TestNeedsCompaction_Disabled(t *testing.T) {
	mp := &mockProvider{
		name:   "test",
		events: []provider.StreamEvent{{Type: "done"}},
	}
	router := provider.NewRouter([]provider.Provider{mp}, 0)

	// maxContextTokens = 0 disables compaction
	a := New(router, nil, &config.ClaudeConfig{}, 0, false, ModeBuild)
	if a.NeedsCompaction() {
		t.Error("expected NeedsCompaction to be false when disabled")
	}
}

func TestCompact_Success(t *testing.T) {
	// Provider returns a summary when asked to compact
	mp := &mockProvider{
		name: "test",
		events: []provider.StreamEvent{
			{Type: "text_delta", Text: "Summary: user asked about Go. Files read: main.go."},
			{Type: "done"},
		},
	}
	router := provider.NewRouter([]provider.Provider{mp}, 0)
	a := New(router, nil, &config.ClaudeConfig{}, 180000, false, ModeBuild)

	// Build a conversation with 8 messages (4 exchanges) — use long text
	// so the summary is shorter than the original.
	a.mu.Lock()
	for i := 0; i < 4; i++ {
		longText := fmt.Sprintf("This is exchange %d. ", i) + strings.Repeat("Detailed discussion about implementation. ", 20)
		a.history = append(a.history,
			provider.Message{Role: provider.RoleUser, Content: []provider.ContentBlock{{Type: "text", Text: longText}}},
			provider.Message{Role: provider.RoleAssistant, Content: []provider.ContentBlock{{Type: "text", Text: longText}}},
		)
	}
	a.mu.Unlock()

	before, after, err := a.Compact(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if before <= 0 {
		t.Error("expected positive before token count")
	}
	// After compaction, token count should be lower (summary replaces verbose history)
	if after >= before {
		t.Errorf("expected after (%d) < before (%d) tokens", after, before)
	}

	// History should be: context summary (user), ack (assistant), + last 4 preserved = 6 messages
	a.mu.Lock()
	histLen := len(a.history)
	a.mu.Unlock()

	if histLen != 6 {
		t.Errorf("expected 6 messages after compaction, got %d", histLen)
	}

	// First message should be the context summary
	a.mu.Lock()
	firstMsg := a.history[0]
	a.mu.Unlock()

	if firstMsg.Role != provider.RoleUser {
		t.Error("expected first message to be user (context summary)")
	}
	if !strings.Contains(firstMsg.Content[0].Text, "[Context from previous conversation]") {
		t.Error("expected context framing in first message")
	}
}

func TestCompact_TooShort(t *testing.T) {
	mp := &mockProvider{name: "test", events: []provider.StreamEvent{{Type: "done"}}}
	router := provider.NewRouter([]provider.Provider{mp}, 0)
	a := New(router, nil, &config.ClaudeConfig{}, 180000, false, ModeBuild)

	// Only 1 message — too short
	a.mu.Lock()
	a.history = append(a.history, provider.Message{
		Role: provider.RoleUser, Content: []provider.ContentBlock{{Type: "text", Text: "Hi"}},
	})
	a.mu.Unlock()

	before, after, err := a.Compact(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should be a no-op (before == after)
	if before != after {
		t.Errorf("expected no-op (before == after), got %d != %d", before, after)
	}
}

func TestCompact_SingleLongResponse(t *testing.T) {
	// 2 messages: user + long assistant response. Should summarize everything.
	mp := &mockProvider{
		name: "test",
		events: []provider.StreamEvent{
			{Type: "text_delta", Text: "Compact summary of the long response."},
			{Type: "done"},
		},
	}
	router := provider.NewRouter([]provider.Provider{mp}, 0)
	a := New(router, nil, &config.ClaudeConfig{}, 180000, false, ModeBuild)

	a.mu.Lock()
	a.history = append(a.history,
		provider.Message{Role: provider.RoleUser, Content: []provider.ContentBlock{{Type: "text", Text: "Tell me everything about Go."}}},
		provider.Message{Role: provider.RoleAssistant, Content: []provider.ContentBlock{{Type: "text", Text: strings.Repeat("Go is great. ", 500)}}},
	)
	a.mu.Unlock()

	_, _, err := a.Compact(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have summary + ack = 2 messages (no preserved, everything summarized)
	a.mu.Lock()
	histLen := len(a.history)
	a.mu.Unlock()

	if histLen != 2 {
		t.Errorf("expected 2 messages after full compaction, got %d", histLen)
	}
}
