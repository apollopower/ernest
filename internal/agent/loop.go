package agent

import (
	"context"
	"encoding/json"
	"ernest/internal/config"
	"ernest/internal/provider"
	"ernest/internal/tools"
	"fmt"
	"log"
	"sync"
)

const maxToolLoops = 50

// AgentEvent is what the TUI receives from the agent loop.
type AgentEvent struct {
	Type         string // "text", "usage", "tool_call", "tool_result", "tool_confirm", "provider_switch", "error", "done"
	Text         string
	ToolName     string
	ToolInput    string
	ToolResult   string
	ToolUseID    string
	ProviderName string
	Error        error
	Usage        *provider.Usage
}

// ToolDecision is the user's response to a tool confirmation prompt.
type ToolDecision struct {
	ToolUseID string
	Approved  bool
}

// toolCall represents a parsed tool use request from the model response.
type toolCall struct {
	ToolUseID string
	ToolName  string
	ToolInput string // raw JSON string
}

// Agent manages conversation history and dispatches prompts to providers.
type Agent struct {
	mu          sync.Mutex
	router      *provider.Router
	registry    *tools.Registry
	permissions *PermissionChecker
	claudeCfg   *config.ClaudeConfig
	history     []provider.Message
	confirmCh   chan ToolDecision // buffered(1), internal — use ResolveTool to send
}

// New creates an agent with the given router, tool registry, and claude config.
func New(router *provider.Router, registry *tools.Registry, claudeCfg *config.ClaudeConfig) *Agent {
	return &Agent{
		router:      router,
		registry:    registry,
		permissions: NewPermissionChecker(claudeCfg),
		claudeCfg:   claudeCfg,
		confirmCh:   make(chan ToolDecision, 1),
	}
}

// ResolveTool sends a tool confirmation decision to the agent loop.
// Called by the TUI when the user approves or denies a tool use.
// Non-blocking: if the agent is no longer waiting (e.g., cancelled), the
// decision is silently dropped.
func (a *Agent) ResolveTool(toolUseID string, approved bool) {
	select {
	case a.confirmCh <- ToolDecision{ToolUseID: toolUseID, Approved: approved}:
	default:
		log.Printf("[agent] dropped tool decision for %s (no receiver)", toolUseID)
	}
}

// AllowToolAlways approves the current tool call, adds the tool to the
// in-memory allowed list, and persists the choice to .claude/settings.local.json.
// Persistence happens before unblocking the agent to ensure the file is written
// before the tool starts executing.
func (a *Agent) AllowToolAlways(toolUseID, toolName string) error {
	a.permissions.Allow(toolName)

	// Persist to disk. Even if this fails, always unblock the agent loop
	// so the TUI doesn't hang.
	var persistErr error
	if a.claudeCfg != nil && a.claudeCfg.ProjectDir != "" {
		if err := config.SaveAllowedTool(a.claudeCfg.ProjectDir, toolName); err != nil {
			persistErr = err
		}
	}

	select {
	case a.confirmCh <- ToolDecision{ToolUseID: toolUseID, Approved: true}:
	default:
		log.Printf("[agent] dropped always-allow decision for %s (no receiver)", toolUseID)
	}
	return persistErr
}

// Run executes the full agent loop for a user prompt.
// It streams events back via the returned channel.
// The loop continues until the model stops requesting tool calls,
// up to maxToolLoops iterations.
func (a *Agent) Run(ctx context.Context, userPrompt string) <-chan AgentEvent {
	events := make(chan AgentEvent, 64)

	go func() {
		defer close(events)

		a.mu.Lock()
		a.history = append(a.history, provider.Message{
			Role:    provider.RoleUser,
			Content: []provider.ContentBlock{{Type: "text", Text: userPrompt}},
		})
		a.mu.Unlock()

		var toolDefs []provider.ToolDef
		if a.registry != nil {
			toolDefs = a.registry.ToolDefs()
		}

		for iteration := 0; iteration < maxToolLoops; iteration++ {
			a.mu.Lock()
			history := make([]provider.Message, len(a.history))
			copy(history, a.history)
			a.mu.Unlock()

			streamCh, providerName, err := a.router.Stream(
				ctx, a.claudeCfg.SystemPrompt, history, toolDefs,
			)
			if err != nil {
				events <- AgentEvent{Type: "error", Error: err}
				return
			}

			if iteration == 0 {
				events <- AgentEvent{Type: "provider_switch", ProviderName: providerName}
			}

			response, stopReason := a.consumeStream(ctx, streamCh, events)

			a.mu.Lock()
			a.history = append(a.history, response)
			a.mu.Unlock()

			// Stop immediately if context was cancelled (e.g., Ctrl+C)
			if ctx.Err() != nil {
				events <- AgentEvent{Type: "done"}
				return
			}

			// Extract tool calls from the response
			calls := extractToolCalls(response)

			if len(calls) == 0 {
				// Validate: if stop_reason was "tool_use" but no calls found, log a warning
				if stopReason == "tool_use" {
					log.Printf("[agent] warning: stop_reason=tool_use but no tool calls found")
				}
				events <- AgentEvent{Type: "done"}
				return
			}

			// Execute each tool call (with permission checking and confirmation)
			resultBlocks := make([]provider.ContentBlock, 0, len(calls))
			for _, tc := range calls {
				events <- AgentEvent{
					Type:      "tool_call",
					ToolName:  tc.ToolName,
					ToolInput: tc.ToolInput,
					ToolUseID: tc.ToolUseID,
				}

				result, execErr := a.executeToolWithConfirmation(ctx, tc, events)

				var resultContent string
				isError := false
				if execErr != nil {
					resultContent = "error: " + execErr.Error()
					isError = true
				} else {
					resultContent = result
				}

				resultBlocks = append(resultBlocks, provider.ContentBlock{
					Type:      "tool_result",
					ToolUseID: tc.ToolUseID,
					Content:   resultContent,
					IsError:   isError,
				})

				events <- AgentEvent{
					Type:       "tool_result",
					ToolName:   tc.ToolName,
					ToolResult: resultContent,
					ToolUseID:  tc.ToolUseID,
				}

				// Stop if context was cancelled during tool execution/confirmation
				if ctx.Err() != nil {
					break
				}
			}

			// Append tool results as a user message and loop
			a.mu.Lock()
			a.history = append(a.history, provider.Message{
				Role:    provider.RoleUser,
				Content: resultBlocks,
			})
			a.mu.Unlock()
		}

		// Exceeded max iterations
		events <- AgentEvent{
			Type:  "error",
			Error: fmt.Errorf("exceeded maximum tool loop iterations (%d)", maxToolLoops),
		}
	}()

	return events
}

// executeToolWithConfirmation checks permissions, optionally requests user
// confirmation, and executes the tool.
func (a *Agent) executeToolWithConfirmation(ctx context.Context, tc toolCall, events chan<- AgentEvent) (string, error) {
	if a.registry == nil {
		return "", fmt.Errorf("unknown tool: %s", tc.ToolName)
	}

	tool, ok := a.registry.Get(tc.ToolName)
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", tc.ToolName)
	}

	// Check permissions
	perm := a.permissions.Check(tc.ToolName)
	if perm == PermissionDenied {
		return "", fmt.Errorf("tool %s is denied by settings", tc.ToolName)
	}

	// If the tool requires confirmation and isn't auto-allowed, ask the user
	if perm != PermissionAllowed && tool.RequiresConfirmation(json.RawMessage(tc.ToolInput)) {
		events <- AgentEvent{
			Type:      "tool_confirm",
			ToolName:  tc.ToolName,
			ToolInput: tc.ToolInput,
			ToolUseID: tc.ToolUseID,
		}

		// Block until user responds with matching ToolUseID or context is cancelled.
		// Discard stale decisions from previous tool uses.
		for {
			select {
			case decision := <-a.confirmCh:
				if decision.ToolUseID != tc.ToolUseID {
					log.Printf("[agent] discarding stale tool decision: got %s, want %s", decision.ToolUseID, tc.ToolUseID)
					continue
				}
				if !decision.Approved {
					return "", fmt.Errorf("tool use denied by user")
				}
				goto confirmed
			case <-ctx.Done():
				return "", fmt.Errorf("cancelled")
			}
		}
	confirmed:
	}

	return tool.Execute(ctx, json.RawMessage(tc.ToolInput))
}

// consumeStream reads from the provider's stream channel, forwards text events
// to the TUI, accumulates tool_use blocks, and builds the complete assistant
// Message for history. Returns the message and the stop_reason.
func (a *Agent) consumeStream(ctx context.Context, streamCh <-chan provider.StreamEvent, events chan<- AgentEvent) (provider.Message, string) {
	var contentBlocks []provider.ContentBlock
	var textContent string
	var lastUsage *provider.Usage
	var stopReason string

	// Tool use accumulation state
	var currentToolID string
	var currentToolName string
	var currentToolInput string

	for {
		select {
		case <-ctx.Done():
			return buildMessage(textContent, contentBlocks), stopReason
		case evt, ok := <-streamCh:
			if !ok {
				return buildMessage(textContent, contentBlocks), stopReason
			}

			switch evt.Type {
			case "text_delta":
				textContent += evt.Text
				events <- AgentEvent{Type: "text", Text: evt.Text}

			case "tool_use_start":
				currentToolID = evt.ToolUseID
				currentToolName = evt.ToolName
				currentToolInput = ""

			case "tool_input_delta":
				currentToolInput += evt.ToolInput

			case "content_block_stop":
				// Finalize current tool block if one is being accumulated.
				// Always reset accumulation state afterward, even for non-tool
				// blocks, to ensure clean state for the next block.
				if currentToolID != "" {
					var parsedInput any
					if currentToolInput != "" {
						if err := json.Unmarshal([]byte(currentToolInput), &parsedInput); err != nil {
							log.Printf("[agent] warning: failed to parse tool input JSON: %v", err)
							parsedInput = currentToolInput // fall back to raw string
						}
					}
					contentBlocks = append(contentBlocks, provider.ContentBlock{
						Type:      "tool_use",
						ToolUseID: currentToolID,
						ToolName:  currentToolName,
						ToolInput: parsedInput,
					})
				}
				currentToolID = ""
				currentToolName = ""
				currentToolInput = ""

			case "message_start":
				if evt.Usage != nil {
					lastUsage = evt.Usage
				}

			case "message_delta":
				if evt.Usage != nil {
					lastUsage = evt.Usage
				}
				if evt.StopReason != "" {
					stopReason = evt.StopReason
				}

			case "done":
				// Note: stopReason is set by message_delta which the Anthropic API
				// guarantees arrives before message_stop. If a future provider doesn't
				// follow this ordering, stopReason may be empty here.
				if lastUsage != nil {
					events <- AgentEvent{Type: "usage", Usage: lastUsage}
				}
				return buildMessage(textContent, contentBlocks), stopReason

			case "error":
				events <- AgentEvent{Type: "error", Error: evt.Error}
				return buildMessage(textContent, contentBlocks), stopReason
			}
		}
	}
}

// extractToolCalls finds all tool_use content blocks in a message.
func extractToolCalls(msg provider.Message) []toolCall {
	var calls []toolCall
	for _, block := range msg.Content {
		if block.Type == "tool_use" {
			var inputJSON []byte
			if block.ToolInput == nil {
				inputJSON = []byte("{}")
			} else {
				inputJSON, _ = json.Marshal(block.ToolInput)
			}
			calls = append(calls, toolCall{
				ToolUseID: block.ToolUseID,
				ToolName:  block.ToolName,
				ToolInput: string(inputJSON),
			})
		}
	}
	return calls
}

// buildMessage creates an assistant Message from accumulated text and tool blocks.
func buildMessage(text string, toolBlocks []provider.ContentBlock) provider.Message {
	msg := provider.Message{
		Role: provider.RoleAssistant,
	}
	if text != "" {
		msg.Content = append(msg.Content, provider.ContentBlock{Type: "text", Text: text})
	}
	msg.Content = append(msg.Content, toolBlocks...)
	return msg
}
