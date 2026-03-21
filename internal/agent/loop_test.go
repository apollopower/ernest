package agent

import (
	"context"
	"ernest/internal/config"
	"ernest/internal/provider"
	"testing"
	"time"
)

// mockProvider implements provider.Provider for testing.
type mockProvider struct {
	name   string
	events []provider.StreamEvent
}

func (m *mockProvider) Name() string  { return m.name }
func (m *mockProvider) Healthy() bool { return true }

func (m *mockProvider) Stream(ctx context.Context, systemPrompt string, messages []provider.Message, tools []provider.ToolDef) (<-chan provider.StreamEvent, error) {
	ch := make(chan provider.StreamEvent, len(m.events))
	for _, evt := range m.events {
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
	a := New(router, &config.ClaudeConfig{})

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
	a := New(router, &config.ClaudeConfig{SystemPrompt: "Be helpful"})

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
	a := New(router, &config.ClaudeConfig{})

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
	// Create a provider that sends events slowly
	mp := &mockProvider{
		name: "test",
		events: []provider.StreamEvent{
			{Type: "text_delta", Text: "Hello"},
			// In a real scenario there would be more events
			{Type: "done"},
		},
	}

	router := provider.NewRouter([]provider.Provider{mp}, 30*time.Second)
	a := New(router, &config.ClaudeConfig{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	events := a.Run(ctx, "Hi")

	// Drain events — should complete without hanging
	for range events {
	}
}
