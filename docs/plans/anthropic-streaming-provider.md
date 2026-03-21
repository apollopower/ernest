# Anthropic Streaming Provider

## Date: 2026-03-21
## Status: Complete
## GitHub Issue: #1

---

## Problem Statement

Ernest currently echoes user messages back as a placeholder. To be useful as an AI coding assistant, it needs to make real API calls to Claude and stream responses into the TUI in real time. This plan implements the Anthropic Messages API provider with SSE streaming, the provider interface and router from the spec, a minimal agent loop (text-only, no tool use yet), and wires everything into the existing TUI.

---

## Proposed Solution

Implement the provider interface, Anthropic provider, and router as specified in the project spec. Build a simplified agent loop that sends user prompts through the router and streams text responses back to the TUI. The agent loop in this plan handles text responses only — tool use is Phase 2. Modify the TUI's app model to accept an agent, dispatch prompts to it, and render streamed text deltas incrementally. Render assistant responses as markdown via Glamour.

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

All types match the project spec exactly. (The spec is maintained locally as `SPEC.md` at the repo root but is gitignored — it is a private reference document, not checked into version control.)

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
| 8 | No project `.claude/` directory, no repo-root CLAUDE.md, and no user-global `~/.claude/` config | Launch ernest in empty dir with no user-global claude config | System prompt is empty string. Anthropic API call omits the `system` field (or sends empty). Model responds normally without custom instructions. |

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
- Headers: `x-api-key`, `anthropic-version` (define as `const AnthropicVersion = "2023-06-01"`), `content-type: application/json`
- Body: marshal messages to Anthropic's format, set `stream: true`, include `system` field from system prompt, include `max_tokens: 8192`
- Parse SSE response line-by-line using `bufio.Scanner` (call `Scanner.Buffer()` to increase from the default 64KB limit to 1MB, since future tool_use input JSON could exceed 64KB):

  - Lines starting with `event:` set the current event type
  - Lines starting with `data:` buffer the payload. Per the SSE spec, an event can have multiple `data:` lines — buffer all `data:` lines and join them (with newlines) on the blank-line event terminator, then JSON-decode the joined payload once per event.
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
- Return true. Health is tracked by the router, not the provider itself. The `Healthy()` method exists to satisfy the `Provider` interface contract — providers could override it in future (e.g., to check if an API key is set) but for now the router is the sole authority on health state.

### Step 3: Router (`internal/provider/router.go`)

Implement based on the spec, with one deviation: the constructor accepts a `cooldown` parameter instead of hardcoding 30s, making it testable:
- `NewRouter(providers []Provider, cooldown time.Duration) *Router`
- The spec hardcodes `cooldown: 30 * time.Second` — we parameterize it so tests can use short durations. Production code passes the value from `config.CooldownSeconds`.
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
- No tool call loop — if model returns tool use, include any text content blocks and stop

**`consumeStream()` helper:**

Reads from the provider's stream channel and builds the complete `Message` for history:
- `text_delta` events → accumulate into a text `ContentBlock`, forward as `AgentEvent{Type: "text"}`
- `tool_use_start` / `tool_input_delta` events → ignore in Phase 1 (the stream may contain these if the model decides to call a tool despite no tools being offered, which is unlikely but possible). Log and skip.
- `done` event → finalize the message, forward usage
- `error` event → forward as `AgentEvent{Type: "error"}`. On error, still append whatever partial text was accumulated to history (so the user can see what was generated before the failure), but mark the turn as errored.
- Returns the assembled `provider.Message` containing all accumulated content blocks.

### Step 5: Wire Agent into TUI (`internal/tui/app.go`)

**Important: BubbleTea value-receiver pattern.** `AppModel.Update` uses a value receiver (BubbleTea's standard pattern). Every mutation path must set fields on the local `m` and return it — mutations inside closures or goroutines are silently lost. The `tea.Cmd` functions below return messages; they do not mutate `m` directly.

Modify `AppModel`:
- Add `agent *agent.Agent`, `streaming bool`, and `cancelStream context.CancelFunc` fields
- Change `NewAppModel` signature: keep `cfg config.Config` and `claudeCfg *config.ClaudeConfig` parameters (needed for status bar initialization), and add `agent *agent.Agent`. The agent is constructed externally in `main.go` and passed in alongside the existing config values.

**Context lifecycle:**
- On `SubmitMsg`: create `ctx, cancel := context.WithCancel(context.Background())`. Store `cancel` in `m.cancelStream`. Pass `ctx` to `agent.Run()`.
- On `AgentEvent{Type: "done"}` or `AgentEvent{Type: "error"}`: call `m.cancelStream()` to clean up, set `m.cancelStream = nil`.
- On `Ctrl+C` while streaming: call `m.cancelStream()` to abort the HTTP connection and stop the agent loop. Set `m.streaming = false`, re-enable input.
- Between turns: the old context is cancelled and a fresh one is created on the next submit.

**BubbleTea channel-reading pattern:**

Use a command that performs a single blocking read from the agent channel and returns one message. The TUI dispatches the next read after processing each event:

```go
func waitForAgentEvent(ch <-chan agent.AgentEvent) tea.Cmd {
    return func() tea.Msg {
        event, ok := <-ch
        if !ok {
            return StreamDoneMsg{}
        }
        return AgentEventMsg{Event: event}
    }
}
```

On `SubmitMsg`: return `waitForAgentEvent(ch)` as the initial command.
On each `AgentEventMsg`: process the event, then return `waitForAgentEvent(ch)` to schedule the next read.
On `StreamDoneMsg`: the channel is closed, streaming is complete.

**Event handling:**
- `"text"` → append text delta to current streaming message in chat
- `"provider_switch"` → dispatch `StatusUpdateMsg` to status bar (forward through `m.statusBar.Update()`)
- `"done"` → finalize message, dispatch `StatusUpdateMsg` with token counts, set `m.streaming = false`, re-enable input
- `"error"` → display error in chat, set `m.streaming = false`, re-enable input

**Input gating:**
- While `m.streaming == true`, `SubmitMsg` is ignored (input is visually disabled but keystrokes are still captured for Ctrl+C handling)

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

**Known UX limitation:** During streaming the user sees raw markdown syntax (e.g., `**bold**`, triple-backtick code blocks). On finalization the view "jumps" to rendered output. This is acceptable for Phase 1. A future improvement could re-render through Glamour periodically (e.g., on a 500ms tick) to smooth the transition.

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
| BubbleTea value-receiver mutation loss | Medium | High | `AppModel.Update` uses value receivers. All mutations must happen on the local `m` that is returned — never inside closures or goroutines. Use `tea.Cmd` functions that return messages rather than mutating state directly. Document this pattern clearly in code comments. |
| Streaming markdown "jump" on finalization | High | Low | Known UX limitation. Raw markdown visible during streaming, rendered on completion. Acceptable for Phase 1; periodic re-rendering can be added later. |

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

- [x] Create `internal/provider/provider.go` — types and interface
- [x] Create `internal/provider/anthropic.go` — Anthropic Messages API with SSE streaming
- [x] Create `internal/provider/router.go` — health-checking priority router
- [x] Create `internal/agent/loop.go` — text-only agent loop
- [x] Update `internal/tui/chat.go` — streaming message methods + Glamour rendering
- [x] Update `internal/tui/app.go` — wire agent, handle AgentEventMsg, streaming state
- [x] Update `internal/tui/status.go` — token count updates from agent events
- [x] Update `cmd/ernest/main.go` — wire provider → router → agent → TUI
- [x] Write provider tests (message conversion, SSE parsing)
- [x] Write router tests (fallback, cooldown)
- [x] Write agent loop tests (mock provider)
- [x] Test: empty system prompt does not break Anthropic API call
- [x] Verify: end-to-end streaming conversation with real Anthropic API
