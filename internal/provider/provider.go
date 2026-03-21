package provider

import "context"

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
	Type      string // "text_delta", "tool_use_start", "tool_input_delta", "done", "error"
	Text      string
	ToolUseID string
	ToolName  string
	ToolInput string // accumulated JSON
	Error     error
	Usage     *Usage
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

// ToolDef describes a tool for the provider (maps to function calling schema).
type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}
