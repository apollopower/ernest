package provider

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Role represents a conversation participant.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message represents a conversation message (provider-agnostic).
type Message struct {
	Role    Role           `json:"role"`
	Content []ContentBlock `json:"content"`
}

// ContentBlock is a single piece of content within a message.
type ContentBlock struct {
	Type      string `json:"type"`                 // "text", "tool_use", "tool_result"
	Text      string `json:"text,omitempty"`
	ToolUseID string `json:"tool_use_id,omitempty"`
	ToolName  string `json:"tool_name,omitempty"`
	ToolInput any    `json:"tool_input,omitempty"`
	Content   string `json:"content,omitempty"` // for tool_result
	IsError   bool   `json:"is_error,omitempty"`
}

// StreamEvent is emitted during streaming responses.
type StreamEvent struct {
	Type       string // "text_delta", "tool_use_start", "tool_input_delta", "content_block_stop", "message_start", "message_delta", "done", "error"
	Text       string
	ToolUseID  string
	ToolName   string
	ToolInput  string // accumulated JSON
	StopReason string // "end_turn", "tool_use", etc. — set on "message_delta" events, not "done"
	Error      error
	Usage      *Usage
}

// Usage tracks token consumption for a request.
type Usage struct {
	InputTokens  int
	OutputTokens int
}

// Provider is the interface every LLM backend implements.
type Provider interface {
	Name() string
	// Stream sends messages and streams back events.
	// systemPrompt includes the assembled CLAUDE.md + rules content.
	// tools is the list of tool definitions to make available.
	Stream(ctx context.Context, systemPrompt string, messages []Message,
		tools []ToolDef) (<-chan StreamEvent, error)
	// Healthy returns true if the provider is believed to be operational.
	Healthy() bool
}

// APIError represents an HTTP error from a provider API.
// The router uses StatusCode to decide whether to retry or fall back.
type APIError struct {
	StatusCode int
	Body       string
	RetryAfter time.Duration // from Retry-After header, 0 if absent
}

func (e *APIError) Error() string {
	return fmt.Sprintf("API error (status %d): %s", e.StatusCode, e.Body)
}

// NewAPIError creates an APIError, parsing the Retry-After header if present.
func NewAPIError(resp *http.Response, body []byte) *APIError {
	e := &APIError{
		StatusCode: resp.StatusCode,
		Body:       string(body),
	}
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if secs, err := strconv.Atoi(strings.TrimSpace(ra)); err == nil {
			e.RetryAfter = time.Duration(secs) * time.Second
		}
	}
	return e
}

// IsRetryable returns true for errors that may succeed on retry (429, 5xx).
func IsRetryable(err error) bool {
	apiErr, ok := err.(*APIError)
	if !ok {
		return false
	}
	return apiErr.StatusCode == 429 || apiErr.StatusCode >= 500
}

// ToolDef describes a tool for the provider (maps to function calling schema).
type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}
