package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	anthropicAPIURL  = "https://api.anthropic.com/v1/messages"
	anthropicVersion = "2023-06-01"
	maxScannerBuffer = 1 << 20 // 1MB
)

// Anthropic implements the Provider interface for the Anthropic Messages API.
type Anthropic struct {
	apiKey string
	model  string
	client *http.Client
	apiURL string // overridable for testing
}

func NewAnthropic(apiKey, model string) *Anthropic {
	return &Anthropic{
		apiKey: apiKey,
		model:  model,
		client: &http.Client{},
		apiURL: anthropicAPIURL,
	}
}

func (a *Anthropic) Name() string    { return "anthropic" }
func (a *Anthropic) Healthy() bool   { return true }

func (a *Anthropic) Stream(ctx context.Context, systemPrompt string, messages []Message, tools []ToolDef) (<-chan StreamEvent, error) {
	if a.apiKey == "" {
		return nil, fmt.Errorf("ANTHROPIC_API_KEY not set")
	}

	body, err := a.buildRequestBody(systemPrompt, messages, tools)
	if err != nil {
		return nil, fmt.Errorf("building request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", a.apiURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", a.apiKey)
	req.Header.Set("anthropic-version", anthropicVersion)

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(bodyBytes))
	}

	ch := make(chan StreamEvent, 64)
	go a.parseSSE(ctx, resp.Body, ch)
	return ch, nil
}

// buildRequestBody marshals the request for the Anthropic Messages API.
func (a *Anthropic) buildRequestBody(systemPrompt string, messages []Message, tools []ToolDef) ([]byte, error) {
	apiMessages := make([]anthropicMessage, 0, len(messages))
	for _, m := range messages {
		apiMsg := anthropicMessage{Role: string(m.Role)}
		for _, block := range m.Content {
			apiMsg.Content = append(apiMsg.Content, toAnthropicContent(block))
		}
		apiMessages = append(apiMessages, apiMsg)
	}

	reqBody := anthropicRequest{
		Model:     a.model,
		MaxTokens: 8192,
		Stream:    true,
		Messages:  apiMessages,
	}

	if systemPrompt != "" {
		reqBody.System = systemPrompt
	}

	if len(tools) > 0 {
		reqBody.Tools = tools
	}

	return json.Marshal(reqBody)
}

// parseSSE reads the SSE stream and sends StreamEvents on the channel.
func (a *Anthropic) parseSSE(ctx context.Context, body io.ReadCloser, ch chan<- StreamEvent) {
	defer close(ch)
	defer body.Close()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, maxScannerBuffer), maxScannerBuffer)

	var currentEvent string
	var dataLines []string

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}

		line := scanner.Text()

		// Blank line = event terminator per SSE spec
		if line == "" {
			if currentEvent != "" && len(dataLines) > 0 {
				data := strings.Join(dataLines, "\n")
				a.handleSSEEvent(currentEvent, data, ch)
			}
			currentEvent = ""
			dataLines = nil
			continue
		}

		if strings.HasPrefix(line, "event: ") {
			currentEvent = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			dataLines = append(dataLines, strings.TrimPrefix(line, "data: "))
		}
		// Ignore comments (lines starting with :) and other fields
	}

	// Flush any pending event if the stream ended without a trailing blank line
	if currentEvent != "" && len(dataLines) > 0 {
		select {
		case <-ctx.Done():
			return
		default:
			data := strings.Join(dataLines, "\n")
			a.handleSSEEvent(currentEvent, data, ch)
		}
	}

	if err := scanner.Err(); err != nil {
		select {
		case <-ctx.Done():
			// Context cancelled, don't send error
		default:
			ch <- StreamEvent{Type: "error", Error: fmt.Errorf("reading stream: %w", err)}
		}
	}
}

func (a *Anthropic) handleSSEEvent(eventType, data string, ch chan<- StreamEvent) {
	switch eventType {
	case "message_start":
		var msg struct {
			Message struct {
				Usage struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(data), &msg) == nil {
			ch <- StreamEvent{
				Type: "message_start",
				Usage: &Usage{
					InputTokens:  msg.Message.Usage.InputTokens,
					OutputTokens: msg.Message.Usage.OutputTokens,
				},
			}
		}

	case "content_block_start":
		var block struct {
			Index        int `json:"index"`
			ContentBlock struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"content_block"`
		}
		if json.Unmarshal([]byte(data), &block) == nil {
			if block.ContentBlock.Type == "tool_use" {
				ch <- StreamEvent{
					Type:      "tool_use_start",
					ToolUseID: block.ContentBlock.ID,
					ToolName:  block.ContentBlock.Name,
				}
			}
			// text blocks: nothing to emit on start, deltas carry the content
		}

	case "content_block_delta":
		var delta struct {
			Index int `json:"index"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
			} `json:"delta"`
		}
		if json.Unmarshal([]byte(data), &delta) == nil {
			switch delta.Delta.Type {
			case "text_delta":
				ch <- StreamEvent{Type: "text_delta", Text: delta.Delta.Text}
			case "input_json_delta":
				ch <- StreamEvent{Type: "tool_input_delta", ToolInput: delta.Delta.PartialJSON}
			}
		}

	case "content_block_stop":
		// Nothing to do for now — content blocks are finalized via deltas

	case "message_delta":
		var delta struct {
			Usage struct {
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal([]byte(data), &delta) == nil {
			ch <- StreamEvent{
				Type: "message_delta",
				Usage: &Usage{
					OutputTokens: delta.Usage.OutputTokens,
				},
			}
		}

	case "message_stop":
		ch <- StreamEvent{Type: "done"}

	case "ping":
		// Ignore

	case "error":
		var errResp struct {
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(data), &errResp) == nil {
			ch <- StreamEvent{
				Type:  "error",
				Error: fmt.Errorf("%s: %s", errResp.Error.Type, errResp.Error.Message),
			}
		}
	}
}

// Anthropic API request/response types (internal)

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	Stream    bool               `json:"stream"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
	Tools     []ToolDef          `json:"tools,omitempty"`
}

type anthropicMessage struct {
	Role    string           `json:"role"`
	Content []map[string]any `json:"content"`
}

func toAnthropicContent(block ContentBlock) map[string]any {
	switch block.Type {
	case "text":
		return map[string]any{
			"type": "text",
			"text": block.Text,
		}
	case "tool_use":
		return map[string]any{
			"type":  "tool_use",
			"id":    block.ToolUseID,
			"name":  block.ToolName,
			"input": block.ToolInput,
		}
	case "tool_result":
		m := map[string]any{
			"type":        "tool_result",
			"tool_use_id": block.ToolUseID,
			"content":     block.Content,
		}
		if block.IsError {
			m["is_error"] = true
		}
		return m
	default:
		return map[string]any{
			"type": block.Type,
			"text": block.Text,
		}
	}
}
