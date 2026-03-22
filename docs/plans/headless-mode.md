# Headless Mode: Non-Interactive Prompt Execution

## Date: 2026-03-22
## Status: Draft
## GitHub Issue: #22

---

## Problem Statement

Ernest can only be used through the BubbleTea TUI, which requires a terminal and human interaction. This prevents:
- Automated testing (Claude Code can't run and verify Ernest within a dev session)
- CI integration tests with real API calls
- Scripting and piping (`ernest -p "question" | process`)
- Batch automation (`ernest -p "fix the failing test" --auto-approve`)

A headless mode that reuses the existing agent/provider/tool stack but replaces the TUI with stdin/stdout would unlock all of these.

---

## Proposed Solution

Add CLI flags that launch Ernest in headless mode — no TUI, no alt screen. Two usage patterns:

**One-shot**: `ernest -p "prompt"` sends a single prompt, streams the response, exits. Use `ernest -p -` to read the entire stdin as a single prompt (useful for piping large inputs).

**Conversational**: `ernest --headless` reads prompts from stdin line by line, streams responses to stdout, continues until EOF.

Both support `--output json` for structured event output (tool calls, token usage, session ID) and `--auto-approve` to bypass tool confirmation dialogs.

---

## Data Model Changes

### CLI Flags (`cmd/ernest/main.go`)

```
ernest [flags]

Flags:
  -p, --prompt string     Run a single prompt in headless mode and exit
  --headless              Run in headless conversational mode (stdin/stdout)
  --output string         Output format: "text" (default) or "json"
  --auto-approve          Skip all tool confirmation dialogs
  --resume string         Resume a session by ID
```

No flags: launches the TUI (current behavior).

### Headless Runner (`internal/headless/runner.go`)

```go
type Runner struct {
    agent   *agent.Agent
    session *session.Session
    format  OutputFormat  // "text" or "json"
}

type OutputFormat string
const (
    FormatText OutputFormat = "text"
    FormatJSON OutputFormat = "json"
)
```

### JSON Output Events

Each line is a self-contained JSON object:

```go
type OutputEvent struct {
    Version int     `json:"version,omitempty"` // format version (1), included in "session" event
    Type    string  `json:"type"`              // "session", "text", "tool_call", "tool_result", "error", "done"
    Content string  `json:"content,omitempty"` // text content
    Tool    string  `json:"tool,omitempty"`    // tool name
    Input   any     `json:"input,omitempty"`   // tool input (for tool_call)
    Output  string  `json:"output,omitempty"`  // tool output (for tool_result)
    ID      string  `json:"id,omitempty"`      // session ID (for session event)
    Project string  `json:"project,omitempty"` // project dir (for session event)
    Tokens  *Tokens `json:"tokens,omitempty"`  // token usage (for done event)
    Error   string  `json:"error,omitempty"`   // error message (for error event)
}

type Tokens struct {
    Input  int `json:"input"`
    Output int `json:"output"`
}
```

**Note on auto-approve and JSON events:** When `--auto-approve` is active, `tool_confirm` events are NOT emitted — the permission checker returns `PermissionAllowed` directly, so the agent loop skips the confirmation step entirely. JSON output still shows `tool_call` and `tool_result` events, which capture what tool ran and what it returned. The confirmation step is a TUI/human concern and is not relevant to machine consumers.

---

## Specific Scenarios to Cover

| # | Scenario | Command | Expected Outcome |
|---|----------|---------|------------------|
| 1 | One-shot prompt | `ernest -p "what is Go?"` | Streams text response to stdout, exits 0 |
| 2 | One-shot with tools | `ernest -p "read main.go" --auto-approve` | Tool executes, model responds, exits 0 |
| 3 | One-shot JSON output | `ernest -p "hello" --output json` | JSON lines on stdout: session, text events, done with tokens |
| 4 | Conversational mode | `ernest --headless` | Reads lines from stdin, responds to each, continues until EOF |
| 5 | Pipe from stdin | `echo "what is Go?" \| ernest --headless` | Responds, exits on EOF |
| 6 | Tool confirmation without --auto-approve | `ernest -p "write a file"` | Tool denied (no TTY for confirmation), error in output |
| 7 | Tool confirmation with --auto-approve | `ernest -p "write a file" --auto-approve` | Tool executes without prompt |
| 8 | Resume session (one-shot) | `ernest --resume a1b2c3d4 -p "continue"` | Loads session, sends prompt, responds |
| 9 | API error | `ernest -p "hello"` (no API key) | Error on stderr, exits 1 |
| 10 | JSON output with tools | `ernest -p "list files" --output json --auto-approve` | Session, tool_call, tool_result, text, done events |
| 11 | Multi-turn JSON conversation | `ernest --headless --output json` | Each turn: text/tool events, done. New prompt starts next turn. |
| 12 | Stdin as single prompt | `cat question.txt \| ernest -p -` | Reads all stdin as one prompt, responds, exits |

---

## Implementation Plan

### Step 1: Flag Parsing (`cmd/ernest/main.go`)

Add flag parsing using Go's `flag` package:
- `-p` / `--prompt`: string, triggers one-shot headless mode. Value `-` reads all of stdin as the prompt.
- `--headless`: bool, triggers conversational headless mode
- `--output`: string, "text" or "json" (default "text")
- `--auto-approve`: bool, bypasses all tool confirmation
- `--resume`: string, session ID to resume

Logic:
- If `-p` is set: run one-shot headless mode
- If `--headless` is set (without `-p`): run conversational headless mode
- `-p` and `--headless` are mutually exclusive — error if both set
- `--auto-approve` requires `-p` or `--headless` — error if used with TUI mode
- Otherwise: run TUI (current behavior)

**Flag validation:**
- `--auto-approve` without `-p` or `--headless` → exit with error
- `-p` and `--headless` together → exit with error
- `--output json` without `-p` or `--headless` → exit with error

**Exit codes:**
- 0: clean completion
- 1: error (no API key, provider failure, all tools denied, max loops exceeded)

**Signal handling:**
- Set up `signal.NotifyContext` for SIGINT/SIGTERM
- On signal: cancel the agent context, save session, flush output, exit 1

### Step 2: Auto-Approve Mode (`internal/agent/permissions.go`)

Add an `autoApprove` flag to `PermissionChecker`:

```go
func NewPermissionChecker(claudeCfg *config.ClaudeConfig, autoApprove bool) *PermissionChecker
```

When `autoApprove` is true, `Check()` returns `PermissionAllowed` for everything (except explicitly denied tools). This is simpler than bypassing the confirmation channel — it means the agent loop never emits `tool_confirm` events.

Update `agent.New()` to accept the flag and pass it through.

### Step 3: Headless Runner (`internal/headless/runner.go`)

The core headless execution engine:

```go
func NewRunner(agent *agent.Agent, session *session.Session, format OutputFormat, out io.Writer) *Runner

// RunPrompt executes a single prompt and writes output.
func (r *Runner) RunPrompt(ctx context.Context, prompt string) error

// RunConversation reads prompts from stdin and responds until EOF.
func (r *Runner) RunConversation(ctx context.Context, in io.Reader) error
```

**RunPrompt**:
- Call `agent.Run(ctx, prompt)`
- Consume events from the channel
- Write output based on format (text or JSON)
- Return error on agent error

**RunConversation**:
- Emit session event (JSON mode)
- Read lines from `in` with `bufio.Scanner` (increase buffer to 1MB for large prompts)
- For each non-empty line, call `RunPrompt`
- After each turn, check `agent.NeedsCompaction()` and call `agent.Compact(ctx)` if needed (same logic as TUI's "done" handler)
- Continue until EOF or error

**Text output**:
- `"text"` events → write directly to stdout
- `"tool_call"` / `"tool_result"` → silent (not shown)
- `"error"` → write to stderr
- `"done"` → newline, ready for next prompt

**JSON output**:
- Each `AgentEvent` mapped to an `OutputEvent` JSON line
- Session event emitted at start of both `RunPrompt` and `RunConversation` so all JSON-mode runs include session ID and version
- Done event includes token counts

### Step 4: Tool Confirmation in Headless Mode

When `--auto-approve` is NOT set, tools requiring confirmation should be denied automatically in headless mode (no TTY to prompt). The agent will receive a "tool use denied" error and can inform the user.

Implementation: the headless runner detects `tool_confirm` events and automatically calls `agent.ResolveTool(id, false)` with a message like "tool denied (headless mode, use --auto-approve)".

### Step 5: Wire Entry Point

In `cmd/ernest/main.go`:
- Parse flags before the current TUI setup
- Validate flag combinations (see Step 1 logic)
- Set up `signal.NotifyContext` for SIGINT/SIGTERM in headless mode
- If headless: create agent with `autoApprove` flag, create runner, execute
- If TUI: current behavior (unchanged)
- Session resume: add `session.LoadByID(id string)` helper (trivial: `Load(filepath.Join(SessionDir(), id+".json"))`) — works in both modes
- Save session on exit in both headless and TUI modes

### Step 6: Tests

- `internal/headless/runner_test.go`:
  - One-shot text output with mock provider
  - One-shot JSON output — verify event structure
  - Tool call events in JSON mode
  - Error handling (provider failure)
  - Multi-turn conversation with mock stdin
  - Auto-approve flag (tool executes without confirmation)
  - Tool denied in headless mode without --auto-approve

---

## Phases & Dependency Graph

Single-phase implementation. This is small enough for a single PR.

```
Step 1 (Flag parsing)
  ├── Step 2 (Auto-approve)
  │     └── Step 3 (Headless runner)
  │           └── Step 4 (Tool confirmation)
  └── Step 5 (Wire entry point)
       └── Step 6 (Tests)
```

---

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| `--auto-approve` used carelessly | Medium | High | Only available in headless mode. TUI always prompts. Clear naming signals the risk. |
| Multi-turn conversation prompt boundary ambiguity | Medium | Low | Each line is a turn. Empty lines are skipped. Clear and simple. |
| JSON output format changes break consumers | Low | Medium | Version the format with a `"version": 1` field in the session event. |
| Stdin buffering issues with piped input | Low | Low | Use `bufio.Scanner` with line splitting. Works with pipes and heredocs. |
| Tool confirmation deadlock in headless without --auto-approve | Medium | High | Auto-deny with clear error message. Never block waiting for user input that can't come. |

---

## Scope Boundaries

This plan does **NOT** include:
- Interactive prompts in headless mode (e.g., "are you sure?")
- WebSocket or HTTP server mode
- Parallel prompt execution
- Custom output templates
- Streaming JSON (each event is a complete JSON line, not a streaming partial)
- Multi-line prompt support in conversational mode (each line is one turn)
- `--quiet` / `--no-stream` flag for buffered-only output
- Combined `-p` + `--headless` for "start with prompt, then continue conversationally"

---

## Implementation Checklist

- [ ] Add flag parsing to `cmd/ernest/main.go` (-p, --headless, --output, --auto-approve, --resume)
- [ ] Add flag validation (mutually exclusive flags, --auto-approve requires headless)
- [ ] Add `autoApprove` to PermissionChecker and Agent constructor
- [ ] Add `session.LoadByID()` helper
- [ ] Create `internal/headless/runner.go` — Runner, RunPrompt, RunConversation
- [ ] Create `internal/headless/output.go` — OutputEvent (with Version field), text and JSON formatters
- [ ] Handle tool_confirm events in headless mode (auto-deny or auto-approve)
- [ ] Add compaction check after each turn in conversational mode
- [ ] Add signal handling (SIGINT/SIGTERM → context cancel → session save)
- [ ] Wire headless path in main.go (flag check → headless runner instead of TUI)
- [ ] Session support: create new or resume existing, save on exit
- [ ] Write runner tests (one-shot, JSON, multi-turn, auto-approve, tool denied, compaction)
- [ ] Verify: `ernest -p "hello"` streams text response
- [ ] Verify: `ernest -p "read main.go" --auto-approve --output json` shows full event stream
