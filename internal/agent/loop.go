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
	Type         string // "text", "usage", "tool_call", "tool_result", "provider_switch", "error", "done"
	Text         string
	ToolName     string
	ToolInput    string
	ToolResult   string
	ToolUseID    string
	ProviderName string
	Error        error
	Usage        *provider.Usage
}

// toolCall represents a parsed tool use request from the model response.
type toolCall struct {
	ToolUseID string
	ToolName  string
	ToolInput string // raw JSON string
}

// Agent manages conversation history and dispatches prompts to providers.
type Agent struct {
	mu        sync.Mutex
	router    *provider.Router
	registry  *tools.Registry
	claudeCfg *config.ClaudeConfig
	history   []provider.Message
}

// New creates an agent with the given router, tool registry, and claude config.
func New(router *provider.Router, registry *tools.Registry, claudeCfg *config.ClaudeConfig) *Agent {
	return &Agent{
		router:    router,
		registry:  registry,
		claudeCfg: claudeCfg,
	}
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

			// Execute each tool call
			resultBlocks := make([]provider.ContentBlock, 0, len(calls))
			for _, tc := range calls {
				events <- AgentEvent{
					Type:      "tool_call",
					ToolName:  tc.ToolName,
					ToolInput: tc.ToolInput,
					ToolUseID: tc.ToolUseID,
				}

				result, execErr := a.executeTool(ctx, tc)

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

// executeTool looks up and runs a tool from the registry.
func (a *Agent) executeTool(ctx context.Context, tc toolCall) (string, error) {
	if a.registry == nil {
		return "", fmt.Errorf("unknown tool: %s", tc.ToolName)
	}

	tool, ok := a.registry.Get(tc.ToolName)
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", tc.ToolName)
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
			inputJSON, _ := json.Marshal(block.ToolInput)
			if block.ToolInput == nil {
				inputJSON = []byte("{}")
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
