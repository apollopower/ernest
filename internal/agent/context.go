package agent

import (
	"encoding/json"
	"ernest/internal/provider"
)

const (
	// tokensPerChar is the rough heuristic: ~4 characters per token.
	tokensPerChar = 4
	// messageOverhead is the estimated token cost for message structure (role, etc.)
	messageOverhead = 4
)

// EstimateTokens returns a rough token count for a list of messages.
// Uses a chars/4 heuristic — fast and good enough for threshold decisions.
// Over-estimates are safe (triggers compaction early).
func EstimateTokens(messages []provider.Message) int {
	total := 0
	for _, msg := range messages {
		total += messageOverhead
		for _, block := range msg.Content {
			total += estimateBlockTokens(block)
		}
	}
	return total
}

// EstimateSystemPromptTokens returns a rough token count for the system prompt.
func EstimateSystemPromptTokens(systemPrompt string) int {
	if systemPrompt == "" {
		return 0
	}
	return len(systemPrompt)/tokensPerChar + messageOverhead
}

// estimateBlockTokens returns a rough token count for a single content block.
func estimateBlockTokens(block provider.ContentBlock) int {
	switch block.Type {
	case "text":
		return len(block.Text) / tokensPerChar

	case "tool_use":
		tokens := len(block.ToolName) / tokensPerChar
		if block.ToolInput != nil {
			inputJSON, _ := json.Marshal(block.ToolInput)
			tokens += len(inputJSON) / tokensPerChar
		}
		return tokens + 10 // overhead for tool_use structure

	case "tool_result":
		return len(block.Content)/tokensPerChar + 5 // overhead for tool_result structure

	default:
		return len(block.Text) / tokensPerChar
	}
}
