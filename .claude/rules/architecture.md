# Architecture

## Package Structure

```
cmd/ernest/main.go    — Entry point, flag parsing, provider factory, TUI/headless routing
internal/
  agent/              — Agent loop, permissions, compaction, system prompt, token estimation
  config/             — Ernest config, Claude config, credentials, provider resolution
  headless/           — Non-interactive runner, JSON/text output
  provider/           — Provider interface, Anthropic, OpenAI-compatible, router
  session/            — Session persistence, save/load/list
  tools/              — Tool interface, registry, 6 built-in tools
  tui/                — BubbleTea app, chat, input, status bar, picker, tool confirm
```

## Key Interfaces

- `provider.Provider` — implemented by Anthropic and OpenAICompat. Methods: `Name()`, `Stream()`, `Healthy()`.
- `tools.Tool` — implemented by each tool. Methods: `Name()`, `Execute()`, `RequiresConfirmation()`, `InputSchema()`.
- `provider.Router` — tries providers in priority order with health checking and cooldown.

## Data Flow

1. User submits prompt → TUI sends to `agent.Run()`
2. Agent appends to history, calls `router.Stream()` with system prompt + history + tool defs
3. Provider streams SSE events → agent's `consumeStream()` accumulates text + tool blocks
4. Tool calls → `executeToolWithConfirmation()` → permission check → optional confirmation → execute
5. Tool results appended to history → re-enter streaming loop
6. `"done"` event → TUI finalizes message, updates token count

## Concurrency Model

- Agent runs in a goroutine, communicates with TUI via buffered channel (capacity 64)
- `Agent.mu` protects `history` and `router` — snapshot under lock before I/O
- `confirmCh` (buffered 1) coordinates tool confirmation between agent goroutine and TUI
- BubbleTea is single-threaded — use `tea.Cmd` for async work, never block `Update()`
