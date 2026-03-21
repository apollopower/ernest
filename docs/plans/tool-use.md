# Tool Use: Interface, Core Tools, Agent Loop, and Confirmation UX

## Date: 2026-03-21
## Status: Draft
## GitHub Issue: #4

---

## Problem Statement

Ernest can stream text responses from Claude but cannot take actions — it can't read files, write code, or run commands. To be useful as a coding assistant, it needs a tool system that lets the model call tools, see results, and iterate. This plan implements the full tool use pipeline: the tool interface and registry, six core tools, an upgraded agent loop with the tool call → execute → resume cycle, a TUI confirmation dialog, and permission checking from `.claude/settings.json`.

---

## Proposed Solution

Implement in three phases, each producing a mergeable PR:

**Phase 1** builds the tool interface, registry, and six core tools (`read_file`, `write_file`, `str_replace`, `bash`, `glob`, `grep`). These are pure Go implementations with no TUI coupling — each tool takes JSON input and returns a string result.

**Phase 2** upgrades the agent loop to handle tool calls. The `consumeStream` helper is extended to accumulate `tool_use` content blocks. After streaming completes, the loop extracts tool calls, executes them, appends results to history, and re-enters the streaming loop until the model stops requesting tools. New `AgentEvent` types (`tool_call`, `tool_result`) are emitted for the TUI to display.

**Phase 3** adds the TUI confirmation dialog and permission system. Tools that modify state (write_file, bash, str_replace) require user approval via a y/n dialog before execution. The permission checker reads `allowedTools` and `deniedTools` from `.claude/settings.json` to auto-approve or auto-deny tools without prompting.

---

## Data Model Changes

### Tool Interface (`internal/tools/tools.go`)

```go
type Tool interface {
    Name() string
    Description() string
    InputSchema() map[string]any
    Execute(ctx context.Context, input json.RawMessage) (string, error)
    RequiresConfirmation(input json.RawMessage) bool
}

type Registry struct {
    tools map[string]Tool
}
```

Matches the spec exactly.

### AgentEvent Extensions (`internal/agent/loop.go`)

Add fields to AgentEvent for Phase 2:

```go
type AgentEvent struct {
    Type         string // "text", "usage", "tool_call", "tool_result",
                        // "tool_confirm", "provider_switch", "error", "done"
    Text         string
    ToolName     string
    ToolInput    string
    ToolResult   string
    ToolUseID    string
    ProviderName string
    Error        error
    Usage        *provider.Usage
}
```

### Tool Confirmation (`internal/tui/tool_confirm.go`) — Phase 3

```go
type ToolConfirmModel struct {
    toolName  string
    toolInput string
    toolUseID string
    width     int
}

type ToolApproveMsg struct{ ToolUseID string }
type ToolDenyMsg struct{ ToolUseID string }
```

### Permissions (`internal/agent/permissions.go`) — Phase 3

```go
type PermissionChecker struct {
    allowedTools []string
    deniedTools  []string
}

func (p *PermissionChecker) Check(toolName string) Permission // Allowed, Denied, Ask
```

---

## Specific Scenarios to Cover

| # | Scenario | Action | Expected Outcome |
|---|----------|--------|------------------|
| 1 | User asks "read the contents of main.go" | Submit prompt | Model calls `read_file`, tool executes, result fed back, model summarizes file contents |
| 2 | User asks "create a file called hello.go" | Submit prompt | Model calls `write_file`, confirmation dialog appears (y/n), user approves, file created, model confirms |
| 3 | User asks "run go test" | Submit prompt | Model calls `bash` with `go test`, confirmation dialog, user approves, output returned to model |
| 4 | User denies a tool confirmation | Press `n` | Tool returns "tool use denied by user" error, model acknowledges and continues |
| 5 | User asks a multi-step coding task | Submit prompt | Model chains multiple tool calls (read → edit → run tests), each tool executes in sequence |
| 6 | Tool in `allowedTools` list | Tool called | Executes immediately, no confirmation prompt |
| 7 | Tool in `deniedTools` list | Tool called | Returns error to model without prompting user |
| 8 | Model calls unknown tool | Tool not in registry | Error result returned, model continues |
| 9 | `bash` command times out | Long-running command | Context cancellation stops the command, partial output returned |
| 10 | `read_file` on nonexistent path | Bad path | Error result returned to model, model handles gracefully |
| 11 | `str_replace` with non-unique old_string | Ambiguous edit | Error result returned: "old_string is not unique in file" |
| 12 | `glob` pattern matches many files | Wide pattern | Results truncated to reasonable limit, model informed of truncation |
| 13 | `grep` with regex pattern | Search | Matching lines returned with file paths and line numbers |

---

## Implementation Plan

### Phase 1: Tool Interface, Registry, and Core Tools

#### Step 1.1: Tool Interface and Registry (`internal/tools/tools.go`)

Implement as specified in the project spec:
- `Tool` interface with `Name()`, `Description()`, `InputSchema()`, `Execute()`, `RequiresConfirmation()`
- `Registry` struct with `NewRegistry()`, `Get()`, `ToolDefs()`
- `ToolDefs()` converts registry to `[]provider.ToolDef` for the provider API
- **`ToolDefs()` must return tools in a stable order** (sort by name) to preserve Anthropic prompt cache hits and make debugging deterministic. Store a `[]string` of ordered names alongside the map, or sort on output.

#### Step 1.2: `read_file` Tool (`internal/tools/file_read.go`)

**Input schema:**
```json
{
    "type": "object",
    "properties": {
        "file_path": {"type": "string", "description": "Absolute path to the file to read"},
        "offset": {"type": "integer", "description": "Line number to start reading from (1-based)"},
        "limit": {"type": "integer", "description": "Maximum number of lines to read"}
    },
    "required": ["file_path"]
}
```

**Behavior:**
- Read file contents, return with line numbers (like `cat -n`)
- Support `offset` and `limit` for partial reads
- Default: read up to 2000 lines
- Return clear error for nonexistent files, directories, permission errors
- `RequiresConfirmation`: always false (read-only)

#### Step 1.3: `write_file` Tool (`internal/tools/file_write.go`)

**Input schema:**
```json
{
    "type": "object",
    "properties": {
        "file_path": {"type": "string", "description": "Absolute path to write to"},
        "content": {"type": "string", "description": "Content to write"}
    },
    "required": ["file_path", "content"]
}
```

**Behavior:**
- Write content to file, creating parent directories as needed
- Overwrite if file exists
- Return confirmation message with file path and byte count
- `RequiresConfirmation`: always true (modifies filesystem)

#### Step 1.4: `str_replace` Tool (`internal/tools/str_replace.go`)

**Input schema:**
```json
{
    "type": "object",
    "properties": {
        "file_path": {"type": "string", "description": "Absolute path to the file"},
        "old_string": {"type": "string", "description": "Exact string to find and replace"},
        "new_string": {"type": "string", "description": "Replacement string"},
        "replace_all": {"type": "boolean", "description": "Replace all occurrences (default: false)"}
    },
    "required": ["file_path", "old_string", "new_string"]
}
```

**Behavior:**
- Read file, find `old_string`, replace with `new_string`
- If `replace_all` is false (default) and `old_string` appears more than once, return error: "old_string is not unique in file, found N occurrences"
- If `old_string` not found, return error: "old_string not found in file"
- `new_string: ""` is valid and means "delete the matched text"
- Write file back only if a replacement was made
- `RequiresConfirmation`: always true (modifies filesystem)

#### Step 1.5: `bash` Tool (`internal/tools/bash.go`)

**Input schema:**
```json
{
    "type": "object",
    "properties": {
        "command": {"type": "string", "description": "The shell command to execute"},
        "timeout": {"type": "integer", "description": "Timeout in milliseconds (default: 120000)"}
    },
    "required": ["command"]
}
```

**Behavior:**
- Execute command via `exec.CommandContext` with `sh -c`
- Default timeout: 120 seconds (2 minutes), configurable via `timeout` parameter
- Capture combined stdout + stderr
- Return output with exit code
- If timeout exceeded, kill process and return partial output with timeout error
- Truncate output to 100KB to avoid blowing up context
- `RequiresConfirmation`: always true (executes arbitrary commands)

#### Step 1.6: `glob` Tool (`internal/tools/glob.go`)

**Input schema:**
```json
{
    "type": "object",
    "properties": {
        "pattern": {"type": "string", "description": "Glob pattern (e.g. '**/*.go', 'src/**/*.ts')"},
        "path": {"type": "string", "description": "Directory to search in (default: cwd)"}
    },
    "required": ["pattern"]
}
```

**Behavior:**
- Use `filepath.Glob` for simple patterns, or `doublestar` library for `**` support
- Return matching file paths, one per line
- Truncate to 1000 results with a note about truncation
- `RequiresConfirmation`: always false (read-only)

#### Step 1.7: `grep` Tool (`internal/tools/grep.go`)

**Input schema:**
```json
{
    "type": "object",
    "properties": {
        "pattern": {"type": "string", "description": "Regex pattern to search for"},
        "path": {"type": "string", "description": "File or directory to search in (default: cwd)"},
        "include": {"type": "string", "description": "Glob pattern to filter files (e.g. '*.go')"}
    },
    "required": ["pattern"]
}
```

**Behavior:**
- Walk directory tree, match file contents against compiled regex
- Return matches as `file:line:content` format
- Respect `include` filter for file types
- Skip binary files, `.git/`, `node_modules/`, other common ignore patterns
- Truncate to 500 matches with a note
- `RequiresConfirmation`: always false (read-only)

**Known limitation for glob and grep:** Neither tool respects `.gitignore` patterns in Phase 1. Results may include files from `vendor/`, build output, etc. Full `.gitignore` parsing is deferred — for now, the hardcoded skip list (`.git/`, `node_modules/`) provides baseline filtering.

#### Step 1.8: Tests for Phase 1

- `internal/tools/tools_test.go` — registry creation, Get, ToolDefs
- `internal/tools/file_read_test.go` — read existing file, offset/limit, nonexistent file, directory error
- `internal/tools/file_write_test.go` — write new file, overwrite, create parent dirs
- `internal/tools/str_replace_test.go` — single replacement, replace_all, non-unique error, not-found error
- `internal/tools/bash_test.go` — simple command, exit code, timeout
- `internal/tools/glob_test.go` — pattern matching, truncation
- `internal/tools/grep_test.go` — regex matching, include filter, binary skip
- All tests use `t.TempDir()` for filesystem operations

### Phase 2: Agent Loop Upgrade

#### Step 2.1: Extend `consumeStream` to Accumulate Tool Use

**Prerequisite: update `internal/provider/anthropic.go`:**
- In `handleSSEEvent`, emit `StreamEvent{Type: "content_block_stop"}` for `content_block_stop` events (currently swallowed with a comment). Without this, `consumeStream` has no signal to finalize tool input JSON.
- Extract `stop_reason` from `message_delta` events and include it in the `StreamEvent` (add `StopReason string` field to `StreamEvent`). This enables belt-and-suspenders validation: if `stop_reason == "tool_use"`, tool calls must be present.
- Update `StreamEvent.Type` documentation in `provider.go` to include the new event types.

**Update `consumeStream` in `internal/agent/loop.go`:**
- Track a "current tool block" state: on `tool_use_start`, record tool ID, name, and begin a JSON accumulation buffer. Anthropic streams blocks sequentially (text finishes before tool starts), so a single "current block" tracker is sufficient — no need for a `map[int]` by index.
- On `tool_input_delta`, append partial JSON string to the current tool block's buffer.
- On `content_block_stop`, finalize the current tool block: **unmarshal the accumulated JSON string into `json.RawMessage` or `map[string]any`** before storing in `ContentBlock.ToolInput`. This is critical — if stored as a raw string, `toAnthropicContent` will serialize it as a JSON string rather than a JSON object, breaking the next API call.
- Build the assistant response `Message` with both text and tool_use `ContentBlock`s.

**Retain the existing `sync.Mutex` on `Agent.history`.** All history reads and writes must remain guarded as they are today. Copy the history slice before passing to `router.Stream()`.

#### Step 2.2: Implement Tool Call Loop

**Define `extractToolCalls` helper:**

```go
type toolCall struct {
    ToolUseID string
    ToolName  string
    ToolInput string // raw JSON string for display and execution
}

// extractToolCalls returns all tool_use content blocks from a message.
func extractToolCalls(msg provider.Message) []toolCall
```

Iterates `msg.Content`, finds blocks where `Type == "tool_use"`, marshals `ToolInput` back to JSON string for execution.

**Update `Run` in `internal/agent/loop.go`:**
- Add a max loop iteration guard: `const maxToolLoops = 50`. If exceeded, emit an error event and return. This prevents runaway loops from a misbehaving model.
- After streaming completes, extract tool calls from the response message via `extractToolCalls()`
- If `stop_reason == "tool_use"` but no tool calls found, emit an error (API contract violation)
- If no tool calls, emit `"done"` and return (current behavior)
- If tool calls exist:
  - For each tool call, emit `AgentEvent{Type: "tool_call"}` to TUI
  - Look up tool in registry, execute it
  - Emit `AgentEvent{Type: "tool_result"}` to TUI
  - Build `tool_result` `ContentBlock`s
  - Append tool results as a user message to history
  - Re-enter the streaming loop (call `router.Stream` again)
- The loop continues until the model responds with no tool calls

**Phase 2 safety: only register read-only tools.** To avoid a security window where all tools auto-execute without confirmation, Phase 2 registers only `read_file`, `glob`, and `grep`. The write tools (`write_file`, `str_replace`, `bash`) are deferred to Phase 3 when the confirmation dialog gates them. This eliminates the need for throwaway safety code.

#### Step 2.3: Update Agent Constructor

Change `agent.New()` to accept `*tools.Registry`:
```go
func New(router *provider.Router, registry *tools.Registry, claudeCfg *config.ClaudeConfig) *Agent
```

Pass `registry.ToolDefs()` to `router.Stream()` instead of `nil`.

#### Step 2.4: Update TUI to Display Tool Events

In `internal/tui/app.go`, handle new `AgentEvent` types:
- `"tool_call"` → display tool name and input in chat (styled distinctly)
- `"tool_result"` → display tool output in chat (truncated if long)

In `internal/tui/chat.go`, add rendering for tool call/result messages:
- Tool calls: show tool name with a distinctive style (e.g., muted, with a prefix like `[tool: read_file]`)
- Tool results: show output in a code-block style, truncated to ~50 lines with "... (truncated)" indicator

#### Step 2.5: Update Entry Point

In `cmd/ernest/main.go`:
- Import `internal/tools`
- Create all tool instances
- Create registry with `tools.NewRegistry(...)`
- Pass registry to `agent.New()`

#### Step 2.6: Tests for Phase 2

- `internal/agent/loop_test.go` — extend with:
  - Test tool call detection and execution with mock provider returning tool_use events
  - Test multi-turn tool loop (tool call → result → model responds with more tool calls → done)
  - Test unknown tool handling
  - Test tool execution error handling

### Phase 3: Confirmation Dialog and Permissions

#### Step 3.1: Permission Checker (`internal/agent/permissions.go`)

```go
type Permission int
const (
    PermissionAsk Permission = iota
    PermissionAllowed
    PermissionDenied
)

type PermissionChecker struct {
    allowedTools []string
    deniedTools  []string
}

func NewPermissionChecker(claudeCfg *config.ClaudeConfig) *PermissionChecker
func (p *PermissionChecker) Check(toolName string) Permission
```

- Tool names are matched exactly against the `allowedTools` and `deniedTools` lists
- If the tool name is in `allowedTools` → `PermissionAllowed`
- If the tool name is in `deniedTools` → `PermissionDenied`
- Otherwise → `PermissionAsk`

#### Step 3.2: Tool Confirmation Dialog (`internal/tui/tool_confirm.go`)

A BubbleTea model that renders a confirmation prompt:

```
─── Tool Use ──────────────────────────────────────
  bash
  > go test ./...

  Allow? (y)es / (n)o
───────────────────────────────────────────────────
```

- Shows tool name and a summary of the input (command for bash, file path for file ops)
- `y` sends `ToolApproveMsg{ToolUseID}`
- `n` sends `ToolDenyMsg{ToolUseID}`
- Captures all keyboard input while visible (modal)

#### Step 3.3: Wire Confirmation into Agent Loop

The confirmation flow requires coordination between the agent loop (goroutine) and the TUI (main thread):

- Agent loop emits `AgentEvent{Type: "tool_confirm"}` with tool details instead of executing immediately
- TUI shows the confirmation dialog
- User presses y/n
- TUI sends the approval/denial back to the agent via a channel
- Agent loop receives the decision and either executes or returns an error result

Implementation:
- Add a `confirmCh chan ToolDecision` to the `Agent` struct — **must be buffered with size 1** to prevent the TUI from deadlocking. BubbleTea runs on a single goroutine; if the agent hasn't reached its `<-confirmCh` read yet, an unbuffered send from the TUI's `Update` would block the entire event loop.
- Agent sends confirmation request on `events` channel, then blocks reading `confirmCh` with a `select` that also checks `ctx.Done()`:
  ```go
  select {
  case decision := <-a.confirmCh:
      // execute or deny based on decision
  case <-ctx.Done():
      // user cancelled (Ctrl+C, :q), return error result
  }
  ```
  This prevents goroutine leaks if the user quits while a dialog is visible.
- TUI receives `"tool_confirm"` event, shows dialog, sends `ToolDecision` back on `confirmCh` when user responds
- `ToolDecision` is a struct: `{ToolUseID string, Approved bool}`

For tools where `RequiresConfirmation()` is false, or where the permission checker returns `PermissionAllowed`, skip the dialog and execute immediately.

For `PermissionDenied`, return error result immediately without asking.

**Phase 3 also registers the write tools** (`write_file`, `str_replace`, `bash`) that were deferred from Phase 2. These tools are now safe to register because the confirmation dialog gates execution.

#### Step 3.4: Update AppModel for Confirmation State

In `internal/tui/app.go`:
- Add `confirmDialog *ToolConfirmModel` field
- Add `confirmCh chan<- agent.ToolDecision` field
- When `AgentEvent{Type: "tool_confirm"}` arrives:
  - Create and display the confirmation dialog
  - Block further agent event processing until user responds
- On `ToolApproveMsg` / `ToolDenyMsg`:
  - Send decision on `confirmCh`
  - Dismiss dialog
  - Resume processing agent events

#### Step 3.5: Tests for Phase 3

- `internal/agent/permissions_test.go` — allowed, denied, ask, empty lists
- Tool confirm model rendering (basic view test)
- Integration test: tool call with confirmation → approve → result
- Integration test: tool call with confirmation → deny → error result

---

## Phases & Dependency Graph

```
Phase 1 (Tool interface + 6 tools) ──→ PR #1
    │
    ▼
Phase 2 (Agent loop upgrade) ──→ PR #2
    │
    ▼
Phase 3 (Confirmation + permissions) ──→ PR #3
```

Each phase produces a working, testable state:
- After Phase 1: tools exist and are tested in isolation, but aren't called by the agent
- After Phase 2: tool use loop works with read-only tools (read_file, glob, grep). Write tools deferred to Phase 3.
- After Phase 3: all tools registered, confirmation dialog gates write tools, permissions respected

---

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| `bash` tool security: model runs destructive commands | High | High | `RequiresConfirmation` always true for bash. Phase 2 only registers read-only tools (read_file, glob, grep). Write tools (write_file, str_replace, bash) are deferred to Phase 3 when the confirmation dialog gates them. |
| `**` glob pattern requires third-party library | Medium | Low | Use `github.com/bmatcuk/doublestar/v4` — small, well-maintained, no transitive deps. Or implement a simple recursive walker if we want zero deps. |
| Tool call JSON accumulation from partial deltas | Medium | Medium | Accumulate `input_json_delta` partials into a buffer string. Parse JSON only on `content_block_stop`, not during deltas. Test with multi-chunk tool inputs. |
| Agent-TUI confirmation channel coordination | Medium | High | `confirmCh` is buffered(1) to prevent TUI deadlock. Agent reads with `select` on both `confirmCh` and `ctx.Done()` to prevent goroutine leaks on user quit. |
| Runaway tool loop from misbehaving model | Low | Medium | Max loop iteration guard (`maxToolLoops = 50`). Emit error and stop if exceeded. |
| Large tool outputs blowing up context window | Medium | Medium | Truncate all tool outputs: bash to 100KB, grep to 500 matches, glob to 1000 files, file_read to 2000 lines. Include truncation notices. |

---

## Scope Boundaries

This plan does **NOT** include:
- Context compaction (separate plan)
- MCP server support (separate plan)
- Windows compatibility for the `bash` tool — uses `sh -c` which requires a POSIX shell. A dedicated Windows portability plan will bridge this gap (e.g., `cmd /C` or PowerShell via build tags).
- Image input / base64 encoding
- Cost tracking
- Session persistence
- Additional slash commands beyond `:q`

---

## Implementation Checklist

### Phase 1: Tool Interface and Core Tools
- [x] Implement `internal/tools/tools.go` — Tool interface and Registry
- [x] Implement `internal/tools/file_read.go` — read_file tool
- [x] Implement `internal/tools/file_write.go` — write_file tool
- [x] Implement `internal/tools/str_replace.go` — str_replace tool
- [x] Implement `internal/tools/bash.go` — bash tool
- [x] Implement `internal/tools/glob.go` — glob tool
- [x] Implement `internal/tools/grep.go` — grep tool
- [x] Write tests for all tools and registry
- [x] Verify: `go build`, `go vet`, `go test` all pass

### Phase 2: Agent Loop Upgrade
- [x] Update `anthropic.go`: emit `content_block_stop` event, extract `stop_reason` from `message_delta`
- [x] Update `StreamEvent` type documentation in `provider.go`
- [x] Extend `consumeStream` to accumulate tool_use content blocks (unmarshal JSON into `map[string]any`)
- [x] Implement `extractToolCalls()` helper and `toolCall` struct
- [x] Implement tool call → execute → resume loop in `Run()` with max loop guard
- [x] Add `ToolName`, `ToolInput`, `ToolResult`, `ToolUseID` fields to `AgentEvent`
- [x] Update `agent.New()` to accept `*tools.Registry`
- [x] Register only read-only tools (read_file, glob, grep) in `cmd/ernest/main.go`
- [x] Update TUI to display tool call/result events in chat
- [x] Write agent loop tests for tool use scenarios
- [x] Verify: end-to-end tool use with real Anthropic API

### Phase 3: Confirmation Dialog and Permissions
- [ ] Implement `internal/agent/permissions.go` — PermissionChecker
- [ ] Implement `internal/tui/tool_confirm.go` — confirmation dialog model
- [ ] Add `confirmCh` (buffered size 1) coordination between agent loop and TUI
- [ ] Wire confirmation into agent loop (check permissions, prompt if needed, `select` with `ctx.Done()`)
- [ ] Register write tools (write_file, str_replace, bash) now that confirmation gates them
- [ ] Update AppModel for confirmation state management
- [ ] Add styles for confirmation dialog
- [ ] Write permission checker tests
- [ ] Write confirmation flow integration tests
- [ ] Verify: end-to-end with confirmation dialog and permission checking
