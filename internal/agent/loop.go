package agent

import (
	"context"
	"ernest/internal/config"
	"ernest/internal/provider"
	"log"
)

// AgentEvent is what the TUI receives from the agent loop.
type AgentEvent struct {
	Type         string // "text", "provider_switch", "error", "done"
	Text         string
	ProviderName string
	Error        error
	Usage        *provider.Usage
}

// Agent manages conversation history and dispatches prompts to providers.
type Agent struct {
	router    *provider.Router
	claudeCfg *config.ClaudeConfig
	history   []provider.Message
}

// New creates an agent with the given router and claude config.
func New(router *provider.Router, claudeCfg *config.ClaudeConfig) *Agent {
	return &Agent{
		router:    router,
		claudeCfg: claudeCfg,
	}
}

// Run executes the agent loop for a user prompt.
// It streams events back via the returned channel.
// This is a text-only loop — tool calls are ignored until Phase 2.
func (a *Agent) Run(ctx context.Context, userPrompt string) <-chan AgentEvent {
	events := make(chan AgentEvent, 64)

	go func() {
		defer close(events)

		a.history = append(a.history, provider.Message{
			Role:    provider.RoleUser,
			Content: []provider.ContentBlock{{Type: "text", Text: userPrompt}},
		})

		streamCh, providerName, err := a.router.Stream(
			ctx, a.claudeCfg.SystemPrompt, a.history, nil,
		)
		if err != nil {
			events <- AgentEvent{Type: "error", Error: err}
			return
		}

		events <- AgentEvent{Type: "provider_switch", ProviderName: providerName}

		response := a.consumeStream(ctx, streamCh, events)
		a.history = append(a.history, response)

		events <- AgentEvent{Type: "done"}
	}()

	return events
}

// consumeStream reads from the provider's stream channel, forwards events to
// the TUI, and builds the complete assistant Message for history.
func (a *Agent) consumeStream(ctx context.Context, streamCh <-chan provider.StreamEvent, events chan<- AgentEvent) provider.Message {
	var textContent string
	var lastUsage *provider.Usage

	for {
		select {
		case <-ctx.Done():
			return buildAssistantMessage(textContent)
		case evt, ok := <-streamCh:
			if !ok {
				return buildAssistantMessage(textContent)
			}

			switch evt.Type {
			case "text_delta":
				textContent += evt.Text
				events <- AgentEvent{Type: "text", Text: evt.Text}

			case "message_start":
				if evt.Usage != nil {
					lastUsage = evt.Usage
				}

			case "message_delta":
				if evt.Usage != nil {
					lastUsage = evt.Usage
				}

			case "tool_use_start", "tool_input_delta":
				// Phase 1: log and skip tool use events
				log.Printf("[agent] ignoring %s event (tool use not yet supported)", evt.Type)

			case "done":
				if lastUsage != nil {
					events <- AgentEvent{Type: "usage", Usage: lastUsage}
				}
				return buildAssistantMessage(textContent)

			case "error":
				events <- AgentEvent{Type: "error", Error: evt.Error}
				return buildAssistantMessage(textContent)
			}
		}
	}
}

func buildAssistantMessage(text string) provider.Message {
	msg := provider.Message{
		Role: provider.RoleAssistant,
	}
	if text != "" {
		msg.Content = []provider.ContentBlock{{Type: "text", Text: text}}
	}
	return msg
}
