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

// countingProvider returns different errors per call for retry testing.
type countingProvider struct {
	name   string
	calls  int
	errors []error  // error per call (nil = success)
	events []StreamEvent
}

func (c *countingProvider) Name() string  { return c.name }
func (c *countingProvider) Healthy() bool { return true }

func (c *countingProvider) Stream(ctx context.Context, systemPrompt string, messages []Message, tools []ToolDef) (<-chan StreamEvent, error) {
	idx := c.calls
	c.calls++
	if idx < len(c.errors) && c.errors[idx] != nil {
		return nil, c.errors[idx]
	}
	ch := make(chan StreamEvent, len(c.events))
	for _, evt := range c.events {
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

func TestRouter_RetryOn429(t *testing.T) {
	err429 := &APIError{StatusCode: 429, Body: "rate limited"}
	p := &countingProvider{
		name:   "primary",
		errors: []error{err429, err429, nil}, // fail twice, succeed on third
		events: []StreamEvent{{Type: "done"}},
	}

	router := NewRouter([]Provider{p}, 30*time.Second)
	_, name, err := router.Stream(context.Background(), "", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "primary" {
		t.Errorf("expected 'primary', got %q", name)
	}
	if p.calls != 3 {
		t.Errorf("expected 3 calls (2 retries + success), got %d", p.calls)
	}
}

func TestRouter_RetryExhausted_FallsBack(t *testing.T) {
	err429 := &APIError{StatusCode: 429, Body: "rate limited"}
	p1 := &countingProvider{
		name:   "primary",
		errors: []error{err429, err429, err429, err429}, // all 4 attempts fail
	}
	p2 := &mockProvider{
		name:   "fallback",
		events: []StreamEvent{{Type: "done"}},
	}

	router := NewRouter([]Provider{p1, p2}, 30*time.Second)
	_, name, err := router.Stream(context.Background(), "", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "fallback" {
		t.Errorf("expected 'fallback' after retries exhausted, got %q", name)
	}
	if p1.calls != 4 {
		t.Errorf("expected 4 calls (1 + 3 retries), got %d", p1.calls)
	}
}

func TestRouter_NoRetryOn401(t *testing.T) {
	err401 := &APIError{StatusCode: 401, Body: "unauthorized"}
	p1 := &countingProvider{
		name:   "primary",
		errors: []error{err401},
	}
	p2 := &mockProvider{
		name:   "fallback",
		events: []StreamEvent{{Type: "done"}},
	}

	router := NewRouter([]Provider{p1, p2}, 30*time.Second)
	_, name, err := router.Stream(context.Background(), "", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "fallback" {
		t.Errorf("expected immediate fallback on 401, got %q", name)
	}
	if p1.calls != 1 {
		t.Errorf("expected 1 call (no retries for 401), got %d", p1.calls)
	}
}

func TestRouter_NoRetryOnNonAPIError(t *testing.T) {
	p1 := &countingProvider{
		name:   "primary",
		errors: []error{fmt.Errorf("connection refused")},
	}
	p2 := &mockProvider{
		name:   "fallback",
		events: []StreamEvent{{Type: "done"}},
	}

	router := NewRouter([]Provider{p1, p2}, 30*time.Second)
	_, name, err := router.Stream(context.Background(), "", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "fallback" {
		t.Errorf("expected immediate fallback on non-API error, got %q", name)
	}
	if p1.calls != 1 {
		t.Errorf("expected 1 call (no retries), got %d", p1.calls)
	}
}

func TestRouter_RetryCtxCancel(t *testing.T) {
	err429 := &APIError{StatusCode: 429, Body: "rate limited"}
	p := &countingProvider{
		name:   "primary",
		errors: []error{err429, err429, err429, err429},
	}

	ctx, cancel := context.WithCancel(context.Background())
	router := NewRouter([]Provider{p}, 30*time.Second)

	// Cancel after first retry starts
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, _, err := router.Stream(ctx, "", nil, nil)
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestRouter_RetryOn500(t *testing.T) {
	err500 := &APIError{StatusCode: 500, Body: "internal server error"}
	p := &countingProvider{
		name:   "primary",
		errors: []error{err500, nil}, // fail once, succeed on retry
		events: []StreamEvent{{Type: "done"}},
	}

	router := NewRouter([]Provider{p}, 30*time.Second)
	_, name, err := router.Stream(context.Background(), "", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "primary" {
		t.Errorf("expected 'primary', got %q", name)
	}
	if p.calls != 2 {
		t.Errorf("expected 2 calls, got %d", p.calls)
	}
}

func TestAPIError_Format(t *testing.T) {
	err := &APIError{StatusCode: 429, Body: "rate limited"}
	expected := "API error (status 429): rate limited"
	if err.Error() != expected {
		t.Errorf("expected %q, got %q", expected, err.Error())
	}
}

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		err       error
		retryable bool
	}{
		{&APIError{StatusCode: 429}, true},
		{&APIError{StatusCode: 500}, true},
		{&APIError{StatusCode: 502}, true},
		{&APIError{StatusCode: 503}, true},
		{&APIError{StatusCode: 401}, false},
		{&APIError{StatusCode: 403}, false},
		{&APIError{StatusCode: 400}, false},
		{fmt.Errorf("connection refused"), false},
		{nil, false},
	}
	for _, tt := range tests {
		if tt.err == nil {
			continue
		}
		got := IsRetryable(tt.err)
		if got != tt.retryable {
			t.Errorf("IsRetryable(%v) = %v, want %v", tt.err, got, tt.retryable)
		}
	}
}

func TestRetryDelay(t *testing.T) {
	// Exponential backoff
	if d := retryDelay(fmt.Errorf("generic"), 1); d != 1*time.Second {
		t.Errorf("attempt 1: expected 1s, got %v", d)
	}
	if d := retryDelay(fmt.Errorf("generic"), 2); d != 2*time.Second {
		t.Errorf("attempt 2: expected 2s, got %v", d)
	}
	if d := retryDelay(fmt.Errorf("generic"), 3); d != 4*time.Second {
		t.Errorf("attempt 3: expected 4s, got %v", d)
	}

	// Retry-After respected
	err := &APIError{StatusCode: 429, RetryAfter: 5 * time.Second}
	if d := retryDelay(err, 1); d != 5*time.Second {
		t.Errorf("expected 5s from Retry-After, got %v", d)
	}

	// Retry-After capped
	errLong := &APIError{StatusCode: 429, RetryAfter: 120 * time.Second}
	if d := retryDelay(errLong, 1); d != maxRetryAfter {
		t.Errorf("expected %v cap, got %v", maxRetryAfter, d)
	}
}
