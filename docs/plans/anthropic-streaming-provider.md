# Anthropic Streaming Provider

## Date: 2026-03-21
## Status: Draft
## GitHub Issue: #1

---

## Problem Statement

Ernest currently echoes user messages back as a placeholder. To be useful as an AI coding assistant, it needs to make real API calls to Claude and stream responses into the TUI in real time. This plan implements the Anthropic Messages API provider with SSE streaming, the provider interface and router from the spec, a minimal agent loop (text-only, no tool use yet), and wires everything into the existing TUI.

---

## Proposed Solution

Implement the provider interface, Anthropic provider, and router as specified in SPEC.md. Build a simplified agent loop that sends user prompts through the router and streams text responses back to the TUI. The agent loop in this plan handles text responses only — tool use is Phase 2. Modify the TUI's app model to accept an agent, dispatch prompts to it, and render streamed text deltas incrementally. Render assistant responses as markdown via Glamour.

---

## Data Model Changes

### Provider Types (`internal/provider/provider.go`)

New file implementing the types from the spec:

```go
type Role string                    // "user", "assistant"
type Message struct                 // Role + []ContentBlock
type ContentBlock struct             // text, tool_use, tool_result
type StreamEvent struct              // text_delta, tool_use_start, tool_input_delta, done, error
type Usage struct                    // InputTokens, OutputTokens
type Provider interface              // Name(), Stream(), Healthy()
type ToolDef struct                  // name, description, input_schema
```

All types match SPEC.md exactly.

### Agent Event (`internal/agent/loop.go`)

```go
type AgentEvent struct {
    Type         string              // "text", "provider_switch", "error", "done"
    Text         string
    ProviderName string
    Error        error
    Usage        *provider.Usage
}
```

Subset of the spec's AgentEvent — tool_call and tool_result fields are omitted until Phase 2.

### TUI Messages

New BubbleTea message types in `internal/tui/app.go`:

```go
type AgentEventMsg struct{ Event agent.AgentEvent }  // wraps agent events for BubbleTea
type StreamStartMsg struct{}                           // signals streaming began
type StreamDoneMsg struct{}                            // signals streaming complete
```

---

## Specific Scenarios to Cover

| # | Scenario | Action | Expected Outcome |
|---|----------|--------|------------------|
| 1 | User submits a prompt with valid ANTHROPIC_API_KEY | Type message, press Enter | Message appears in chat. Assistant response streams in word-by-word. Status bar shows "anthropic" and model name. Token count updates on completion. |
| 2 | User submits a prompt with no API key set | Type message, press Enter | Error message displayed in chat: "error: ANTHROPIC_API_KEY not set" |
| 3 | User submits a prompt and Anthropic returns a 500/529 | API error mid-stream | Error displayed in chat. Provider marked unhealthy in router. |
| 4 | User presses Ctrl+C during streaming | Cancel | Streaming stops, partial response remains visible, input re-enabled |
| 5 | User submits multiple messages in sequence | Conversation | Full conversation history sent to API each turn. Context builds naturally. |
| 6 | Project has `.claude/CLAUDE.md` | Launch ernest in project dir | CLAUDE.md content sent as system prompt to Anthropic API |
| 7 | Assistant response contains markdown | Code blocks, bold, lists | Rendered with syntax highlighting via Glamour |

---

## Implementation Plan

### Step 1: Provider Interface and Types (`internal/provider/provider.go`)

Create the file with all types from the spec:
- `Role`, `Message`, `ContentBlock`, `StreamEvent`, `Usage`, `Provider` interface, `ToolDef`
- These are provider-agnostic — every backend will use them

### Step 2: Anthropic Provider (`internal/provider/anthropic.go`)

Implement the `Provider` interface for the Anthropic Messages API:

**Constructor:**
```go
func NewAnthropic(apiKey, model string) *Anthropic
```

**Stream method:**
- POST to `https://api.anthropic.com/v1/messages`
- Headers: `x-api-key`, `anthropic-version: 2023-06-01`, `content-type: application/json`
- Body: marshal messages to Anthropic's format, set `stream: true`, include `system` field from system prompt, include `max_tokens: 8096`
- Parse SSE response line-by-line using `bufio.Scanner`:
  - Lines starting with `event:` set the current event type
  - Lines starting with `data:` contain JSON payload
  - Handle event types:
    - `message_start` — extract usage from message object
    - `content_block_start` — if type is `text`, begin text accumulation; if `tool_use`, emit `tool_use_start` (future-proofing)
    - `content_block_delta` — if delta type is `text_delta`, emit text; if `input_json_delta`, accumulate partial JSON
    - `content_block_stop` — finalize content block
    - `message_delta` — extract final usage (cumulative output tokens), emit stop_reason
    - `message_stop` — emit done event
    - `ping` — ignore
    - `error` — parse error JSON, emit error event
- Send `StreamEvent` values on the returned channel
- Close channel when stream ends
- Respect context cancellation (close HTTP response body on ctx.Done)

**Message format conversion:**
- Ernest's `Message` → Anthropic API format
- User messages with text content → `{"role": "user", "content": [{"type": "text", "text": "..."}]}`
- Assistant messages → same structure with `"role": "assistant"`
- Tool results → `{"type": "tool_result", "tool_use_id": "...", "content": "..."}` (future-proofing)

**Healthy method:**
- Return true (health is tracked by the router, not the provider itself)

### Step 3: Router (`internal/provider/router.go`)

Implement as specified in the spec:
- `NewRouter(providers []Provider, cooldown time.Duration) *Router`
- `Stream()` tries providers in order, skipping unhealthy ones in cooldown
- `markHealthy()` / `markUnhealthy()` update the health cache
- For this plan there's only one provider, but the router still wraps it for the correct API surface

### Step 4: Agent Loop — Text Only (`internal/agent/loop.go`)

Simplified version of the spec's agent loop (no tool use):

```go
func (a *Agent) Run(ctx context.Context, userPrompt string) <-chan AgentEvent
```

- Append user message to history
- Call `router.Stream()` with system prompt, history, and empty tool list
- Consume stream events:
  - `text_delta` → forward as `AgentEvent{Type: "text", Text: delta}`
  - `done` → forward as `AgentEvent{Type: "done", Usage: usage}`
  - `error` → forward as `AgentEvent{Type: "error", Error: err}`
- Accumulate assistant response text and append complete message to history
- No tool call loop — if model returns tool use, just include the text portion and stop

Also implement `consumeStream()` helper that reads from the stream channel and builds the complete `Message` for history.

### Step 5: Wire Agent into TUI (`internal/tui/app.go`)

Modify `AppModel`:
- Add `agent *agent.Agent` field and `streaming bool` flag
- Change `NewAppModel` to accept `*agent.Agent`
- On `SubmitMsg`:
  - Add user message to chat
  - Start agent goroutine via `agent.Run()`
  - Use a BubbleTea command that reads from the agent's event channel and dispatches `AgentEventMsg` back to the TUI
- On `AgentEventMsg`:
  - `"text"` → append text delta to current streaming message in chat
  - `"provider_switch"` → update status bar
  - `"done"` → finalize message, update token count, re-enable input
  - `"error"` → display error in chat
- Disable input while streaming (prevent sending during active response)
- Ctrl+C during streaming → cancel the context, stop the stream

### Step 6: Streaming Chat Updates (`internal/tui/chat.go`)

Add methods for incremental message building:
- `StartStreamingMessage()` — add an empty assistant message, return its index
- `AppendToMessage(text string)` — append text to the last message and re-render
- `FinalizeMessage()` — mark streaming complete

### Step 7: Markdown Rendering (`internal/tui/chat.go`)

Use Glamour to render assistant messages:
- Create a Glamour renderer with `glamour.WithAutoStyle()` (adapts to terminal background)
- Render assistant message content through Glamour on finalization
- During streaming, render as plain text (avoid re-rendering markdown on every delta)

### Step 8: Update Entry Point (`cmd/ernest/main.go`)

Wire the new components:
- Create Anthropic provider from config (resolve API key from env)
- Create router with the single provider
- Create empty tool registry (placeholder for Phase 2)
- Create agent with router, registry, and claude config
- Pass agent to `NewAppModel`

### Step 9: Write Tests

- `internal/provider/anthropic_test.go`:
  - Test message format conversion (Ernest messages → Anthropic API JSON)
  - Test SSE parsing with mock response data (text streaming, error events)
- `internal/provider/router_test.go`:
  - Test fallback behavior with mock providers (healthy/unhealthy)
  - Test cooldown logic
- `internal/agent/loop_test.go`:
  - Test agent loop with a mock provider that returns canned responses

---

## Phases & Dependency Graph

Single-phase implementation. This plan is small enough to be implemented and shipped in a single PR.

```
Step 1 (Provider types)
  ├── Step 2 (Anthropic provider)
  │     └── Step 3 (Router)
  │           └── Step 4 (Agent loop)
  │                 └── Step 5 (Wire into TUI)
  ├── Step 6 (Streaming chat updates)
  └── Step 7 (Markdown rendering)

Steps 5 + 6 + 7 → Step 8 (Entry point)
Steps 2 + 3 + 4 → Step 9 (Tests)
```

---

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| SSE parsing edge cases (multi-line data fields, empty events, retry directives) | Medium | Medium | Use a robust line-by-line parser that handles `data:` continuation. Test with recorded API responses. |
| BubbleTea update loop blocking on channel reads | Medium | High | Use a `tea.Cmd` that does a single channel read and re-dispatches, rather than blocking the update loop. Each read returns one event and schedules the next read. |
| Glamour rendering performance on large responses | Low | Medium | Only render markdown on finalization, not during streaming. Stream as plain text. |
| Context cancellation not closing HTTP connection cleanly | Medium | Low | Ensure `resp.Body.Close()` is called in a deferred cleanup and on context cancellation. |

---

## Scope Boundaries

This plan does **NOT** include:
- OpenAI or Gemini providers (separate plans)
- Tool use (tool_call/tool_result handling in agent loop) — Phase 2
- Tool confirmation dialog
- Context compaction
- Session persistence (save/resume)
- Conversation search
- Command palette beyond `:q`
- Cost tracking

---

## Implementation Checklist

- [ ] Create `internal/provider/provider.go` — types and interface
- [ ] Create `internal/provider/anthropic.go` — Anthropic Messages API with SSE streaming
- [ ] Create `internal/provider/router.go` — health-checking priority router
- [ ] Create `internal/agent/loop.go` — text-only agent loop
- [ ] Update `internal/tui/chat.go` — streaming message methods + Glamour rendering
- [ ] Update `internal/tui/app.go` — wire agent, handle AgentEventMsg, streaming state
- [ ] Update `internal/tui/status.go` — token count updates from agent events
- [ ] Update `cmd/ernest/main.go` — wire provider → router → agent → TUI
- [ ] Write provider tests (message conversion, SSE parsing)
- [ ] Write router tests (fallback, cooldown)
- [ ] Write agent loop tests (mock provider)
- [ ] Verify: end-to-end streaming conversation with real Anthropic API
