package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

const (
	defaultOpenAIBaseURL     = "https://api.openai.com/v1"
	openAIMaxScannerBuffer   = 1 << 20 // 1MB
)

// OpenAICompat implements the Provider interface for any OpenAI Chat Completions-compatible API.
// Covers: OpenAI, SiliconFlow, Together, Ollama, Groq, and any API that speaks
// the /chat/completions protocol.
type OpenAICompat struct {
	name    string
	apiKey  string
	model   string
	baseURL string
	client  *http.Client
}

func NewOpenAICompat(name, apiKey, model, baseURL string) *OpenAICompat {
	if baseURL == "" {
		baseURL = defaultOpenAIBaseURL
	}
	// Ensure no trailing slash
	baseURL = strings.TrimRight(baseURL, "/")
	return &OpenAICompat{
		name:    name,
		apiKey:  apiKey,
		model:   model,
		baseURL: baseURL,
		client:  &http.Client{},
	}
}

func (o *OpenAICompat) Name() string  { return o.name }
func (o *OpenAICompat) Healthy() bool { return true }

func (o *OpenAICompat) Stream(ctx context.Context, systemPrompt string, messages []Message, tools []ToolDef) (<-chan StreamEvent, error) {

	body, err := o.buildRequestBody(systemPrompt, messages, tools)
	if err != nil {
		return nil, fmt.Errorf("building request body: %w", err)
	}

	url := o.baseURL + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if o.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+o.apiKey)
	}

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, NewAPIError(resp, bodyBytes)
	}

	ch := make(chan StreamEvent, 64)
	go o.parseSSE(ctx, resp.Body, ch)
	return ch, nil
}

// buildRequestBody constructs the OpenAI Chat Completions request.
func (o *OpenAICompat) buildRequestBody(systemPrompt string, messages []Message, tools []ToolDef) ([]byte, error) {
	var oaiMessages []openAIMessage

	// System prompt as first message
	if systemPrompt != "" {
		oaiMessages = append(oaiMessages, openAIMessage{
			Role:    "system",
			Content: systemPrompt,
		})
	}

	// Convert messages
	for _, m := range messages {
		oaiMsg := toOpenAIMessage(m)
		oaiMessages = append(oaiMessages, oaiMsg...)
	}

	reqBody := openAIRequest{
		Model:         o.model,
		Messages:      oaiMessages,
		Stream:        true,
		StreamOptions: &streamOptions{IncludeUsage: true},
	}

	// Convert tool definitions
	if len(tools) > 0 {
		for _, t := range tools {
			reqBody.Tools = append(reqBody.Tools, openAITool{
				Type: "function",
				Function: openAIFunction{
					Name:        t.Name,
					Description: t.Description,
					Parameters:  t.InputSchema,
				},
			})
		}
	}

	return json.Marshal(reqBody)
}

// parseSSE reads the OpenAI SSE stream and sends StreamEvents.
func (o *OpenAICompat) parseSSE(ctx context.Context, body io.ReadCloser, ch chan<- StreamEvent) {
	defer close(ch)
	defer body.Close()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, openAIMaxScannerBuffer), openAIMaxScannerBuffer)

	// Tool call accumulation: OpenAI streams tool calls with index-based multiplexing
	pendingTools := make(map[int]*pendingToolCall)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}

		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			// Flush any pending tool calls
			o.flushPendingTools(pendingTools, ch)
			ch <- StreamEvent{Type: "done"}
			return
		}

		var chunk openAIStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		if len(chunk.Choices) == 0 {
			continue
		}

		choice := chunk.Choices[0]
		delta := choice.Delta

		// Text content
		if delta.Content != "" {
			ch <- StreamEvent{Type: "text_delta", Text: delta.Content}
		}

		// Tool calls
		for _, tc := range delta.ToolCalls {
			pending, exists := pendingTools[tc.Index]
			if !exists {
				pending = &pendingToolCall{}
				pendingTools[tc.Index] = pending
			}
			if tc.ID != "" {
				pending.id = tc.ID
			}
			if tc.Function.Name != "" {
				pending.name = tc.Function.Name
			}
			if tc.Function.Arguments != "" {
				pending.arguments += tc.Function.Arguments
			}
		}

		// Check for finish reason
		if choice.FinishReason == "tool_calls" {
			o.flushPendingTools(pendingTools, ch)
		}

		// Usage (some providers include it on the final chunk)
		if chunk.Usage != nil {
			ch <- StreamEvent{
				Type: "message_delta",
				Usage: &Usage{
					InputTokens:  chunk.Usage.PromptTokens,
					OutputTokens: chunk.Usage.CompletionTokens,
				},
			}
		}
	}

	if err := scanner.Err(); err != nil {
		select {
		case <-ctx.Done():
		default:
			ch <- StreamEvent{Type: "error", Error: fmt.Errorf("reading stream: %w", err)}
		}
	}
}

// flushPendingTools emits accumulated tool calls as StreamEvents.
func (o *OpenAICompat) flushPendingTools(pending map[int]*pendingToolCall, ch chan<- StreamEvent) {
	if len(pending) == 0 {
		return
	}

	// Sort by index for deterministic tool call ordering
	indices := make([]int, 0, len(pending))
	for idx := range pending {
		indices = append(indices, idx)
	}
	sort.Ints(indices)

	for _, idx := range indices {
		tc := pending[idx]
		if tc.name != "" {
			ch <- StreamEvent{
				Type:      "tool_use_start",
				ToolUseID: tc.id,
				ToolName:  tc.name,
			}
			if tc.arguments != "" {
				ch <- StreamEvent{
					Type:      "tool_input_delta",
					ToolInput: tc.arguments,
				}
			}
			ch <- StreamEvent{Type: "content_block_stop"}
		}
		delete(pending, idx)
	}

	ch <- StreamEvent{Type: "message_delta", StopReason: "tool_use"}
}

type pendingToolCall struct {
	id        string
	name      string
	arguments string
}

// OpenAI API types

type openAIRequest struct {
	Model         string          `json:"model"`
	Messages      []openAIMessage `json:"messages"`
	Stream        bool            `json:"stream"`
	StreamOptions *streamOptions  `json:"stream_options,omitempty"`
	Tools         []openAITool    `json:"tools,omitempty"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type openAIMessage struct {
	Role       string              `json:"role"`
	Content    string              `json:"content,omitempty"`
	ToolCalls  []openAIToolCallMsg `json:"tool_calls,omitempty"`
	ToolCallID string              `json:"tool_call_id,omitempty"`
}

type openAIToolCallMsg struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Function openAIFuncCall `json:"function"`
}

type openAIFuncCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openAITool struct {
	Type     string         `json:"type"`
	Function openAIFunction `json:"function"`
}

type openAIFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type openAIStreamChunk struct {
	ID      string `json:"id"`
	Choices []struct {
		Delta struct {
			Role      string `json:"role,omitempty"`
			Content   string `json:"content,omitempty"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id,omitempty"`
				Type     string `json:"type,omitempty"`
				Function struct {
					Name      string `json:"name,omitempty"`
					Arguments string `json:"arguments,omitempty"`
				} `json:"function,omitempty"`
			} `json:"tool_calls,omitempty"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason,omitempty"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage,omitempty"`
}

// toOpenAIMessage converts an Ernest Message to one or more OpenAI messages.
// A single Ernest message with tool_use blocks becomes an assistant message with tool_calls.
// Tool results become separate "tool" role messages.
func toOpenAIMessage(msg Message) []openAIMessage {
	role := string(msg.Role)

	// Check for tool results (user role with tool_result blocks)
	var toolResults []openAIMessage
	var textContent string
	var toolCalls []openAIToolCallMsg

	for _, block := range msg.Content {
		switch block.Type {
		case "text":
			textContent += block.Text

		case "tool_use":
			inputJSON, _ := json.Marshal(block.ToolInput)
			toolCalls = append(toolCalls, openAIToolCallMsg{
				ID:   block.ToolUseID,
				Type: "function",
				Function: openAIFuncCall{
					Name:      block.ToolName,
					Arguments: string(inputJSON),
				},
			})

		case "tool_result":
			toolResults = append(toolResults, openAIMessage{
				Role:       "tool",
				Content:    block.Content,
				ToolCallID: block.ToolUseID,
			})
		}
	}

	var result []openAIMessage

	// Assistant message with text and/or tool calls
	if role == "assistant" {
		aMsg := openAIMessage{Role: "assistant"}
		if textContent != "" {
			aMsg.Content = textContent
		}
		if len(toolCalls) > 0 {
			aMsg.ToolCalls = toolCalls
		}
		result = append(result, aMsg)
	} else if len(toolResults) > 0 {
		// Tool result messages (user role in Ernest → tool role in OpenAI)
		result = append(result, toolResults...)
		// If there was also text alongside tool results, emit a separate user message
		if textContent != "" {
			result = append(result, openAIMessage{Role: "user", Content: textContent})
		}
	} else {
		// Regular user message
		result = append(result, openAIMessage{
			Role:    role,
			Content: textContent,
		})
	}

	return result
}
