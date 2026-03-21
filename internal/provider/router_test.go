package provider

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// mockProvider is a test double for the Provider interface.
type mockProvider struct {
	name      string
	healthy   bool
	streamErr error
	events    []StreamEvent
}

func (m *mockProvider) Name() string  { return m.name }
func (m *mockProvider) Healthy() bool { return m.healthy }

func (m *mockProvider) Stream(ctx context.Context, systemPrompt string, messages []Message, tools []ToolDef) (<-chan StreamEvent, error) {
	if m.streamErr != nil {
		return nil, m.streamErr
	}

	ch := make(chan StreamEvent, len(m.events))
	for _, evt := range m.events {
		ch <- evt
	}
	close(ch)
	return ch, nil
}

func TestRouter_FirstProviderSucceeds(t *testing.T) {
	p1 := &mockProvider{
		name:    "primary",
		healthy: true,
		events:  []StreamEvent{{Type: "text_delta", Text: "Hello"}, {Type: "done"}},
	}
	p2 := &mockProvider{
		name:    "fallback",
		healthy: true,
		events:  []StreamEvent{{Type: "text_delta", Text: "Fallback"}, {Type: "done"}},
	}

	router := NewRouter([]Provider{p1, p2}, 30*time.Second)
	ch, name, err := router.Stream(context.Background(), "", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "primary" {
		t.Errorf("expected provider 'primary', got %q", name)
	}

	var text string
	for evt := range ch {
		if evt.Type == "text_delta" {
			text += evt.Text
		}
	}
	if text != "Hello" {
		t.Errorf("expected 'Hello', got %q", text)
	}
}

func TestRouter_FallbackOnFailure(t *testing.T) {
	p1 := &mockProvider{
		name:      "primary",
		healthy:   true,
		streamErr: fmt.Errorf("connection refused"),
	}
	p2 := &mockProvider{
		name:    "fallback",
		healthy: true,
		events:  []StreamEvent{{Type: "text_delta", Text: "Fallback"}, {Type: "done"}},
	}

	router := NewRouter([]Provider{p1, p2}, 30*time.Second)
	ch, name, err := router.Stream(context.Background(), "", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "fallback" {
		t.Errorf("expected provider 'fallback', got %q", name)
	}

	var text string
	for evt := range ch {
		if evt.Type == "text_delta" {
			text += evt.Text
		}
	}
	if text != "Fallback" {
		t.Errorf("expected 'Fallback', got %q", text)
	}
}

func TestRouter_AllProvidersExhausted(t *testing.T) {
	p1 := &mockProvider{name: "p1", streamErr: fmt.Errorf("down")}
	p2 := &mockProvider{name: "p2", streamErr: fmt.Errorf("also down")}

	router := NewRouter([]Provider{p1, p2}, 30*time.Second)
	_, _, err := router.Stream(context.Background(), "", nil, nil)
	if err == nil {
		t.Error("expected error when all providers fail")
	}
}

func TestRouter_CooldownSkipsUnhealthy(t *testing.T) {
	p1 := &mockProvider{name: "p1", streamErr: fmt.Errorf("down")}
	p2 := &mockProvider{
		name:   "p2",
		events: []StreamEvent{{Type: "done"}},
	}

	router := NewRouter([]Provider{p1, p2}, 5*time.Second)

	// First call: p1 fails, falls back to p2
	_, name1, err := router.Stream(context.Background(), "", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name1 != "p2" {
		t.Errorf("expected 'p2', got %q", name1)
	}

	// Second call: p1 should be skipped due to cooldown
	p1.streamErr = nil // even if p1 is "fixed", cooldown still applies
	p1.events = []StreamEvent{{Type: "done"}}
	_, name2, err := router.Stream(context.Background(), "", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name2 != "p2" {
		t.Errorf("expected 'p2' (cooldown), got %q", name2)
	}
}

func TestRouter_CooldownExpires(t *testing.T) {
	p1 := &mockProvider{name: "p1", streamErr: fmt.Errorf("down")}
	p2 := &mockProvider{
		name:   "p2",
		events: []StreamEvent{{Type: "done"}},
	}

	router := NewRouter([]Provider{p1, p2}, 10*time.Millisecond)

	// First call: p1 fails
	router.Stream(context.Background(), "", nil, nil)

	// Wait for cooldown to expire
	time.Sleep(20 * time.Millisecond)

	// Now fix p1
	p1.streamErr = nil
	p1.events = []StreamEvent{{Type: "done"}}

	_, name, err := router.Stream(context.Background(), "", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "p1" {
		t.Errorf("expected 'p1' after cooldown expired, got %q", name)
	}
}
