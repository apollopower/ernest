# Context Compaction and Session Persistence

## Date: 2026-03-21
## Status: Draft
## GitHub Issue: #9

---

## Problem Statement

Ernest loses all conversation history when closed, and long coding sessions risk exceeding the context window limit (180K tokens default). These two problems are linked: persistence requires a serializable conversation format, and compaction requires tracking token usage — both operate on the same conversation history data.

Without compaction, a multi-tool coding session can easily consume 50-100K tokens in 10-15 exchanges. Without persistence, every restart means re-explaining context to the model.

---

## Proposed Solution

Implement in three phases:

**Phase 1** builds the session storage format and slash command infrastructure. Sessions are serialized as JSON files in `{UserConfigDir}/ernest/sessions/` (via `os.UserConfigDir()`). Slash commands (`/save`, `/clear`, `/status`) are routed from both the input box and the `:` command palette. Auto-save on exit.

**Phase 2** adds token counting and context compaction. Token counts are estimated using a simple heuristic (chars/4 for text, structured counting for tool blocks). When the conversation approaches the configured `max_context_tokens` threshold, compaction is triggered — the model is asked to summarize the conversation, and the summary replaces the history. `/compact` allows manual compaction.

**Phase 3** adds session resume. `/resume` lists available sessions or loads a specific one. The TUI shows a session picker when multiple sessions exist.

---

## Data Model Changes

### Session Format (`internal/session/session.go`)

```go
type Session struct {
    Version    int                `json:"version"`     // schema version, currently 1
    ID         string             `json:"id"`          // short unique ID (8 chars, crypto/rand)
    CreatedAt  time.Time          `json:"created_at"`
    UpdatedAt  time.Time          `json:"updated_at"`
    ProjectDir string             `json:"project_dir"`
    Summary    string             `json:"summary"`     // first user message or compaction summary
    Messages   []provider.Message `json:"messages"`
    TokenCount int                `json:"token_count"`
}
```

Sessions are stored as individual JSON files: `~/.config/ernest/sessions/{id}.json`

For listing sessions without loading full message history, `ListSessions` uses a lightweight struct that omits the `Messages` field:

```go
type sessionMeta struct {
    Version    int       `json:"version"`
    ID         string    `json:"id"`
    CreatedAt  time.Time `json:"created_at"`
    UpdatedAt  time.Time `json:"updated_at"`
    ProjectDir string    `json:"project_dir"`
    Summary    string    `json:"summary"`
    TokenCount int       `json:"token_count"`
}
```

Go's `encoding/json` will still scan the entire JSON document (including the `messages` array), but it silently ignores fields not present in the target struct. Decoding a full session file into `sessionMeta` avoids allocating and unmarshaling the `Messages` slice and its elements, keeping listing fast and low-allocation while still reading the full file.

### Token Estimator (`internal/agent/context.go`)

```go
func EstimateTokens(messages []provider.Message) int
func EstimateBlockTokens(block provider.ContentBlock) int
```

Simple heuristic: ~4 characters per token for text, structured counting for tool use/result blocks.

### Slash Command Infrastructure (`internal/tui/command.go`)

```go
type SlashCommand struct {
    Name    string
    Args    string
    Handler func(args string) tea.Cmd
}
```

Commands are detected when input starts with `/`. Parsed and dispatched before reaching the agent.

---

## Specific Scenarios to Cover

| # | Scenario | Action | Expected Outcome |
|---|----------|--------|------------------|
| 1 | User types `/status` | Submit | Chat displays: provider, model, token count, session ID, max context tokens |
| 2 | User types `/save` | Submit | Session saved to disk, confirmation shown in chat |
| 3 | User types `/clear` | Submit | Conversation history cleared, fresh session started |
| 4 | User types `/compact` | Submit | Model summarizes conversation, summary replaces history, token count drops |
| 5 | User exits Ernest (Ctrl+C or `:q`) | Exit | Current session auto-saved to disk |
| 6 | Context approaches token limit | During conversation | Auto-compact triggered, user sees notification, conversation continues |
| 7 | User types `/resume` with no args | Submit | Lists recent sessions with ID, date, summary |
| 8 | User types `/resume abc123` | Submit | Loads session abc123, conversation history restored, chat populated |
| 9 | User starts Ernest in same project dir | Launch | Most recent session for this project dir is shown as resumable |
| 10 | User types `/save` multiple times | Submit | Session updated in place (same ID), not duplicated |
| 11 | Session file is corrupt | `/resume` | Error shown, session skipped in listing |
| 12 | Token count exceeds 80% of max | During conversation | Warning shown in status bar |

---

## Implementation Plan

### Phase 1: Session Storage and Slash Commands

#### Step 1.1: Session Package (`internal/session/session.go`)

```go
type Session struct { ... }  // as defined above

func New(projectDir string) *Session              // create new with generated ID
func Load(path string) (*Session, error)           // load from JSON file
func (s *Session) Save() error                     // write to ~/.config/ernest/sessions/{id}.json
func (s *Session) SetMessages(msgs []provider.Message)
func ListSessions() ([]Session, error)             // list all sessions, sorted by UpdatedAt desc
func SessionDir() string                           // ~/.config/ernest/sessions/
```

- Session ID: 8-character hex string via `crypto/rand`
- `Save()` writes to `SessionDir()/{id}.json`, creating the directory if needed
- `ListSessions()` reads all JSON files into `sessionMeta` (skips Messages), sorted by UpdatedAt desc
- `Summary` is set to the first user message text, updated on compaction

#### Step 1.2: Slash Command Infrastructure

**Unified command dispatch:** Both `/` commands (from input box) and `:` commands (from vim nav mode) route to the same `executeCmd(name, args string)` handler:

- In `SubmitMsg` handler: if text starts with `/`, strip the prefix, split into name + args, call `executeCmd`
- In `handleCmdMode`: the existing `executeCmd` is extended with the same command names
- This avoids duplicating command logic across two code paths

**`/status` is a system message** — displayed in the chat viewport but NOT saved to session history. Add a `"system"` role to `ChatMessage` with distinct styling (muted, no label).

Commands for Phase 1:
- `/status` — display provider, model, token count, session ID (system message, not saved)
- `/save` — save current session
- `/clear` — clear history, start new session
- `:q` / `/quit` — save and exit

#### Step 1.3: Wire Session into Agent

- Add `session *session.Session` field to `Agent`
- After each turn (user message + assistant response), update session messages and save
- On agent creation, create a new session or resume the most recent one for the project dir
- Expose `Session()` method for TUI to read session metadata

#### Step 1.4: Auto-save on Exit

- In `main.go`, defer a session save after the BubbleTea program exits
- Also save on `:q` / `/quit` before quitting
- Save on Ctrl+C if possible (may not be reliable for signal handling)

#### Step 1.5: Tests for Phase 1

- Session create, save, load round-trip
- ListSessions with multiple files
- Slash command parsing
- Auto-save on agent turn completion

### Phase 2: Token Counting and Compaction

#### Step 2.1: Token Estimator (`internal/agent/context.go`)

Simple heuristic estimator:
- Text content: `len(text) / 4` (rough but fast)
- Tool use blocks: name + input JSON length / 4
- Tool result blocks: content length / 4
- System prompt: counted once
- Overhead per message: ~4 tokens for role/structure

No external tokenizer dependency — the heuristic is good enough for threshold decisions. Actual token counts from the API (`Usage.InputTokens`) are used when available for status display.

#### Step 2.2: Compaction Strategy

**`Agent.Compact(ctx) error`** — a separate method, called by the TUI between turns, NOT inside `Agent.Run()`. This avoids intra-loop complexity and mutex contention.

When estimated tokens exceed `compactionThreshold` (80% of `max_context_tokens`):

1. TUI shows a system message: "Compacting conversation..."
2. Agent builds a compaction request using the following system prompt (no tool definitions included — this is a summarization task):

```
You are summarizing a coding conversation for context continuity. Produce a
concise summary that preserves:
- The user's current goal and task
- Key decisions made and their rationale
- Files that were read, created, or modified (with paths)
- Any errors encountered and how they were resolved
- The current state of work (what's done, what's next)

Format as a structured summary, not a conversation. Be terse.
```

3. The model's summary response replaces the conversation history:
   - A user message with explicit framing:
     ```
     [Context from previous conversation]
     {summary}
     [End of context. The conversation continues below.]
     ```
   - The last 2-3 exchanges preserved verbatim (to maintain recent context and tool results)
4. Update the session's `Summary` field with the compaction output
5. Update token count estimate

The compaction call uses the same router/provider as normal conversation. Tool definitions are NOT included in the compaction request.

#### Step 2.3: Auto-compact Trigger

**Compaction is triggered by the TUI, not inside `Agent.Run()`.** After receiving a `"done"` event:
- TUI calls `agent.EstimateTokens()` to check current history size
- If above 80% of `maxContextTokens`, TUI calls `agent.Compact(ctx)`
- TUI shows a system message notification during compaction
- `maxContextTokens` is passed to the agent constructor (add to `Agent` struct) so the threshold is accessible

This keeps the agent loop simple and avoids concurrent access to history during compaction.

#### Step 2.4: `/compact` Command

Manual trigger for compaction:
- Same logic as auto-compact but user-initiated
- Show before/after token counts in chat

#### Step 2.5: Token Display in Status Bar

Update `StatusModel` to show estimated token count with color coding:
- Green: < 50% of max
- Yellow: 50-80% of max
- Red: > 80% of max

The status bar already shows token count from API usage. Enhance it with the running estimate between API calls.

#### Step 2.6: Tests for Phase 2

- Token estimator accuracy against known inputs
- Compaction trigger at threshold
- Compaction preserves recent exchanges
- `/compact` command flow

### Phase 3: Session Resume

#### Step 3.1: `/resume` Command

- `/resume` with no args: list recent sessions (last 10) with ID, date, summary, project dir
- `/resume {id}`: load the specified session
- Display in chat as a formatted list

#### Step 3.2: Session Loading into Agent

- `Agent.LoadSession(session)` replaces current history with session messages
- Update token estimate from loaded messages
- TUI re-renders chat with loaded messages

#### Step 3.3: Message-to-ChatMessage Conversion

Implement `func MessagesToChat(msgs []provider.Message) []ChatMessage` in `internal/tui/chat.go` to convert agent history into TUI display messages. This is needed for resume to populate the chat view.

Conversion rules:
- `RoleUser` + text block → `ChatMessage{Role: "user", Content: text}`
- `RoleAssistant` + text block → `ChatMessage{Role: "assistant", Content: text}`
- `RoleAssistant` + tool_use block → `ChatMessage{Role: "tool_call", ToolName: name, Content: input summary}`
- `RoleUser` + tool_result block → `ChatMessage{Role: "tool_result", ToolName: name, Content: result}`
- Multi-block messages produce multiple ChatMessages

#### Step 3.4: Auto-resume Prompt

On startup, if a recent session exists for the current project dir (updated within last 24 hours):
- Show a prompt: "Resume previous session? (y/n)"
- `y`: load the session
- `n` or any other key: start fresh

#### Step 3.5: Tests for Phase 3

- `/resume` listing format
- Session load into agent
- Auto-resume detection logic

---

## Phases & Dependency Graph

```
Phase 1 (Session storage + slash commands) ──→ PR #1
    │
    ▼
Phase 2 (Token counting + compaction) ──→ PR #2
    │
    ▼
Phase 3 (Session resume) ──→ PR #3
```

Each phase produces a working, testable state:
- After Phase 1: sessions save/load, `/status`, `/save`, `/clear` work, auto-save on exit
- After Phase 2: context compaction works (auto + manual), token usage visible in status bar
- After Phase 3: full session lifecycle — save, resume, auto-resume prompt

---

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Token estimation inaccuracy | High | Low | The heuristic only needs to be accurate enough to trigger compaction at roughly the right time. Over-estimating is safe (triggers early). Actual API usage displayed when available. |
| Compaction loses important context | Medium | High | Preserve last 2-3 exchanges verbatim. The compaction prompt explicitly asks the model to retain file paths, decisions, and current task state. Users can `/save` before `/compact` as a safety net. |
| Large session files slow down listing | Low | Low | `ListSessions` deserializes into `sessionMeta` struct (no Messages field), so the JSON decoder skips the messages array. Full loading only on resume. |
| `ToolInput any` JSON round-trip | Medium | Medium | `ToolInput` typed as `any` may shift types through JSON (e.g., `map[string]any` with float64 numbers). Add explicit round-trip serialization tests covering all ContentBlock types. Consider migrating `ToolInput` to `json.RawMessage` in a future refactor for deterministic serialization. |
| Auto-compact during streaming | Medium | Medium | Only trigger compaction between turns (after `"done"` event), never mid-stream. Check threshold before starting the next `router.Stream` call. |
| Session format changes break old sessions | Low | Medium | Include a `"version": 1` field in the JSON. If format changes, add migration logic or skip incompatible sessions in listing. |
| `provider.Message` JSON serialization | Medium | Medium | `ContentBlock.ToolInput` is typed `any` which may not round-trip cleanly through JSON. Test serialization of all content block types. |

---

## Scope Boundaries

This plan does **NOT** include:
- Multi-branch conversation trees
- Session sharing or export
- Cloud sync
- Exact tokenizer (tiktoken/similar) — heuristic is sufficient
- Session search by content
- Session tagging or organization beyond chronological listing
- `/providers` command (separate plan with fallback provider support)
- Migrating `ToolInput` from `any` to `json.RawMessage` (separate refactor)

---

## Implementation Checklist

### Phase 1: Session Storage and Slash Commands
- [ ] Create `internal/session/session.go` — Session struct (with Version field), New, Save, Load, ListSessions (via sessionMeta)
- [ ] Unified slash command dispatch: `/` in input and `:` in vim mode route to same `executeCmd`
- [ ] Add `"system"` role to ChatMessage for command output (not saved to session)
- [ ] Implement `/status` command (system message)
- [ ] Implement `/save` command
- [ ] Implement `/clear` command
- [ ] Wire session into agent — update after each turn
- [ ] Auto-save on exit
- [ ] Write session round-trip tests (including all ContentBlock types: text, tool_use, tool_result)
- [ ] Write ToolInput serialization round-trip test (map, string fallback, nil)
- [ ] Verify: `go build`, `go vet`, `go test` all pass

### Phase 2: Token Counting and Compaction
- [ ] Implement `internal/agent/context.go` — token estimator
- [ ] Pass `maxContextTokens` to Agent constructor
- [ ] Implement `Agent.Compact(ctx)` as a separate method (called by TUI, not inside Run)
- [ ] Implement compaction prompt template and summary injection format
- [ ] Add auto-compact trigger in TUI after "done" event (80% threshold check)
- [ ] Implement `/compact` command
- [ ] Enhance status bar with color-coded token display
- [ ] Write token estimator and compaction tests
- [ ] Verify: end-to-end compaction with real API

### Phase 3: Session Resume
- [ ] Implement `/resume` command (list + load)
- [ ] Implement `Agent.LoadSession()` for restoring history
- [ ] Implement `MessagesToChat()` conversion for populating TUI from loaded session
- [ ] Add auto-resume prompt on startup
- [ ] Write session resume tests (including nonexistent ID, short conversations)
- [ ] Verify: full session lifecycle end-to-end
