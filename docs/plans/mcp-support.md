# MCP Support: Connect to External Tool Servers

## Date: 2026-03-23
## Status: Pending Verification
## GitHub Issue: #17

---

## Problem Statement

Ernest has 6 built-in tools, but the ecosystem of useful capabilities is much larger — GitHub, Sentry, Notion, databases, custom internal tools. Each of these would require a dedicated implementation in Ernest. MCP (Model Context Protocol) solves this by providing a standard protocol for connecting to external tool servers. Claude Code already uses MCP extensively, and users have existing MCP server configurations they want to reuse.

Ernest should be able to connect to the same MCP servers that Claude Code uses, reading the same config files and speaking the same protocol. This makes Ernest immediately compatible with the entire MCP ecosystem without building custom integrations.

---

## Proposed Solution

Implement in three phases:

**Phase 1** adds the MCP client infrastructure and full agent wiring: config loading (`~/.claude.json`, `.mcp.json`), server lifecycle management (spawn stdio servers), tool discovery, and tool call routing through the agent loop. MCP tools are registered alongside built-in tools, require confirmation, and support permission globs. Uses the official Go MCP SDK (`github.com/modelcontextprotocol/go-sdk`). Stdio transport only — HTTP deferred to Phase 3.

**Phase 2** adds UX and observability: `/mcp` status command, `/mcp reconnect`, and friendly display names for MCP tools in the chat view.

**Phase 3** adds HTTP transport for remote MCP servers, `/mcp add`/`/mcp remove` commands, and dynamic tool refresh via `notifications/tools/list_changed`.

---

## Data Model Changes

### MCP Config Loader (`internal/mcp/config.go`)

```go
// MCPServerConfig represents a single MCP server definition.
type MCPServerConfig struct {
    Command string            `json:"command,omitempty"`  // stdio: command to run
    Args    []string          `json:"args,omitempty"`     // stdio: command arguments
    Env     map[string]string `json:"env,omitempty"`      // stdio: environment variables
    Type    string            `json:"type,omitempty"`     // "http" or "sse" for remote
    URL     string            `json:"url,omitempty"`      // remote: server URL
    Headers map[string]string `json:"headers,omitempty"`  // remote: HTTP headers
}

// MCPConfig holds all configured MCP servers from all scopes.
type MCPConfig struct {
    Servers map[string]MCPServerConfig // name → config
}

func LoadMCPConfig(projectDir string) (*MCPConfig, error)
```

**File format:** Both `.mcp.json` and `~/.claude.json` use the same structure:
```json
{
  "mcpServers": {
    "server-name": { "command": "...", "args": [...], "env": {...} }
  }
}
```
The `mcpServers` key wraps the server map. This matches Claude Code's actual format.

**Note on config location:** MCP config is in `~/.claude.json` and `.mcp.json`, NOT in `~/.claude/settings.json` or `.claude/settings.json`. This is Claude Code's actual convention — settings files handle permissions/tool approvals, while `~/.claude.json` and `.mcp.json` handle MCP server definitions. Ernest's existing `.claude/settings.json` parsing (for `allowedTools`/`deniedTools`) is unaffected — these are separate concerns at separate paths.

**Config resolution order** (user scope loaded first, project scope overrides on name collision):
1. `~/.claude.json` → `mcpServers` section (user scope — baseline)
2. `.mcp.json` at project root (project scope — overrides user on name collision)

**Server name validation:** Names containing `__` are rejected on load (prevents ambiguity with tool namespacing `mcp__server__tool`).

Supports `${VAR}` and `${VAR:-default}` environment variable expansion in all string fields. Nested `${}` is not supported.

### MCP Client Manager (`internal/mcp/manager.go`)

```go
// Manager handles MCP server connections and tool discovery.
type Manager struct {
    servers map[string]*ServerConnection
}

type ServerConnection struct {
    name    string
    config  MCPServerConfig
    session *mcp.ClientSession  // from go-sdk
    tools   []provider.ToolDef  // discovered tools, prefixed with server name
}

func NewManager() *Manager
func (m *Manager) ConnectAll(ctx context.Context, config *MCPConfig) error  // 30s timeout per server
func (m *Manager) Tools() []provider.ToolDef    // all discovered tools, sorted by name
func (m *Manager) CallTool(ctx context.Context, serverName, toolName string, args map[string]any) (string, error)
func (m *Manager) Close()                        // disconnect all servers (SDK handles process cleanup)
func (m *Manager) Reconnect(ctx context.Context, name string) error  // reconnect a single server
func (m *Manager) Status() []ServerStatus        // for /mcp command
```

### Tool Name Namespacing

MCP tools are namespaced to avoid collisions with built-in tools:
- Built-in: `read_file`, `bash`, `grep`, etc.
- MCP: `mcp__<server>__<tool>` (e.g., `mcp__sentry__search_issues`, `mcp__github__create_pr`)

This matches Claude Code's namespacing convention. The agent sees all tools (built-in + MCP) in a single flat list.

**Parsing:** `strings.SplitN(name, "__", 3)` → `["mcp", serverName, toolName]`. Since server names are validated to not contain `__`, this is unambiguous. MCP tool names with `__` are preserved correctly (everything after the second delimiter).

### Integration with Agent

The agent receives MCP tools alongside built-in tools:
```go
// In Run(), combine built-in and MCP tool definitions
toolDefs := registry.ToolDefs()
if mcpManager != nil {
    toolDefs = append(toolDefs, mcpManager.Tools()...)
}
```

When the model calls an MCP tool, the agent detects the `mcp__` prefix and routes to the MCP manager instead of the built-in registry.

**MCP tools and confirmation:** MCP tools always require confirmation (they are external, untrusted). Since MCP tools bypass the `Tool` interface, the agent hardcodes `RequiresConfirmation = true` for any tool with the `mcp__` prefix. This is enforced in Phase 1 — no MCP tools execute without user consent.

**MCP tools and plan mode:** MCP tools with the `readOnlyHint` annotation (from `tools/list`) are allowed in plan mode. Tools without this annotation are excluded from plan mode. The `readOnlyTools` set is extended dynamically when MCP servers connect.

**Sorted tool definitions:** MCP tools are sorted by name before appending to built-in tools. The combined list is: built-in tools (sorted) + MCP tools (sorted). This preserves prompt cache stability across sessions.

**Result truncation:** `CallTool()` truncates MCP tool results to 100KB before returning (same limit as the bash tool). Non-text content blocks (images, binary) are dropped with a log warning.

---

## Specific Scenarios to Cover

| # | Scenario | Action | Expected Outcome |
|---|----------|--------|------------------|
| 1 | User has `.mcp.json` with a stdio server | Launch Ernest in project dir | MCP server spawned, tools discovered, available to model |
| 2 | User has `~/.claude.json` with user-scoped servers | Launch Ernest anywhere | User MCP servers available globally |
| 3 | Model calls an MCP tool | Model generates `mcp__sentry__search_issues` | Ernest proxies call to Sentry MCP server, returns result |
| 4 | MCP server fails to start | Bad command in config | Error logged, other servers still work, Ernest continues |
| 5 | MCP server crashes mid-session | Server process exits | Error on next tool call, logged in /mcp status |
| 6 | Config has `${VAR}` references | Env var set | Values expanded before spawning server |
| 7 | Config has `${VAR:-default}` | Env var not set | Default value used |
| 8 | `/mcp` command | User types /mcp | Shows connected servers, tool count, status (connected/error) |
| 9 | No MCP config exists | Launch Ernest | No MCP servers, built-in tools only, no error |
| 10 | MCP tool requires confirmation | Tool in RequiresConfirmation list | Confirmation dialog shown (same as built-in tools) |
| 11 | Plan mode with MCP tools | Enter /plan | MCP tools available in plan mode if they are read-only (based on annotations) |

---

## Implementation Plan

### Phase 1: MCP Client Infrastructure

#### Step 1.0: Pin SDK Version and Validate API

```bash
go get github.com/modelcontextprotocol/go-sdk@v1.0.0
```

After installing, inspect the SDK's exported API to validate that `mcp.NewClient`, `mcp.CommandTransport`, `ClientSession.ListTools`, and `ClientSession.CallTool` exist with the expected signatures. Update pseudocode in this plan if the API differs.

#### Step 1.1: Add Go MCP SDK Dependency

Use the pinned version from Step 1.0.

#### Step 1.2: MCP Config Loader (`internal/mcp/config.go`)

Load MCP server configurations (user first, project overrides on name collision):
1. `~/.claude.json` (parse `mcpServers` key at top level — user/baseline)
2. `.mcp.json` at project root (overrides user on name collision)

Implement `${VAR}` and `${VAR:-default}` expansion:
```go
func expandEnvVars(s string) string
```

Apply expansion to: `command`, `args` elements, `env` values, `url`, `headers` values.

#### Step 1.3: MCP Manager (`internal/mcp/manager.go`)

The central coordinator for MCP connections:

**`ConnectAll(ctx, config)`**:
- For each server in config (concurrently via goroutines):
  - Validate server name (reject names with `__`)
  - If `command` is set: use `mcp.CommandTransport` (stdio)
  - If `type == "http"`: defer to Phase 3 (skip with warning)
  - Create `mcp.NewClient`, call `client.Connect(ctx, transport, nil)` with **30-second timeout** per server
  - Call `session.ListTools()` to discover tools
  - Parse `readOnlyHint` annotation for plan mode compatibility
  - Convert MCP tool definitions to `provider.ToolDef` with `mcp__<server>__` prefix, sorted by name
  - Store the session for later tool calls
- Errors on individual servers are logged but don't prevent others from connecting
- Server crash detection: lazy (discovered on next `CallTool`, not proactively monitored)

**`CallTool(ctx, serverName, toolName, args)`**:
- Find the server session by name
- Call `session.CallTool(ctx, &mcp.CallToolParams{Name: toolName, Arguments: args})`
- Extract text content from the result's content blocks (non-text content like images dropped with log warning)
- Truncate result to 100KB (same limit as bash tool)
- Return as a string (matching Ernest's tool result format)

**`Tools()`**:
- Return all discovered tool definitions across all servers
- Each tool's `Name` is prefixed: `mcp__<server>__<originalName>`

**`Close()`**:
- Close all sessions and release resources (SDK handles stdio subprocess termination)

**`Status()`**:
- Return name, status (connected/error/disconnected), tool count for each server

#### Step 1.4: Tool Name Routing and Confirmation

In `internal/agent/loop.go`, update `executeToolWithConfirmation`:
- If tool name starts with `mcp__`, parse out server name and original tool name via `strings.SplitN(name, "__", 3)`
- **MCP tools always require confirmation** — hardcode `RequiresConfirmation = true` for `mcp__` prefix (no `Tool` interface instance exists for MCP tools)
- The existing confirmation dialog, permission checker, and auto-approve all work unchanged
- After confirmation: route to `mcpManager.CallTool()` instead of `registry.Get()`
- Wrap result in the same error/success pattern as built-in tools
- In plan mode: check `readOnlyTools` map (which now includes MCP tools with `readOnlyHint`)

**Permission system extension:** Add tool name glob matching to `PermissionChecker.Check()`. Currently, glob patterns only apply inside `tool(pattern)` format (matching tool input). Extend to also match tool names: `"mcp__sentry__*"` should match `"mcp__sentry__search_issues"`. Without this, users cannot auto-approve MCP tool groups.

Implementation details:
- Keep `allowedExact`/`deniedExact` maps for pure exact tool-name entries (no `(` and no glob characters like `*`)
- Update `addAllowed`/`addDenied`:
  - Entry with `(` → input-pattern rule (existing behavior)
  - Entry without `(` and no glob chars → `allowedExact`/`deniedExact` (existing behavior)
  - Entry without `(` but with glob chars (e.g., `mcp__sentry__*`) → new `allowedNameGlobs`/`deniedNameGlobs` lists
- Update `Check()`:
  - First: exact match via `allowedExact`/`deniedExact` (fast path)
  - Then: iterate `allowedNameGlobs`/`deniedNameGlobs` with `matchGlob(pattern, toolName)`
  - Then: input-pattern entries with `(` (existing behavior)
  - Non-parenthesized entries are NOT skipped as "already handled" — they route through either exact or name-glob

#### Step 1.5: Wire into Main and Agent

In `cmd/ernest/main.go`:
- Load MCP config after loading Claude config
- Create MCP manager, call `ConnectAll`
- Pass manager to agent (new field on Agent struct)
- Defer `manager.Close()` for cleanup

In `internal/agent/loop.go`:
- Add `mcpManager *mcp.Manager` field to Agent
- In `Run()`, append MCP tools to tool definitions
- In `executeToolWithConfirmation`, route MCP tools to manager

#### Step 1.6: Tests for Phase 1

- Config loader: parse `.mcp.json`, env var expansion, missing config
- Tool name parsing: `mcp__server__tool` → (server, tool)
- Manager with mock: tool discovery, tool call routing
- Integration: agent with MCP tools alongside built-in tools

### Phase 2: Agent Integration and /mcp Command

#### Step 2.1: MCP Tool Confirmation (moved to Phase 1)

MCP tool confirmation is now part of Phase 1 (Step 1.4). The existing confirmation dialog, permission checker (with glob extension for tool names), and auto-approve all work for MCP tools.

**Headless mode:** MCP tools are auto-denied without `--auto-approve`, same as built-in write tools. This is the correct default — external tools should not execute silently in automation unless explicitly approved.

#### Step 2.2: `/mcp` Status and Reconnect Commands

Add `/mcp` to the TUI commands:
```
MCP Servers:
  sentry — connected (12 tools)
  github — connected (8 tools)
  db     — error: connection refused

Total: 20 MCP tools available
```

**`/mcp reconnect [name]`**: Reconnect a crashed or disconnected MCP server without restarting Ernest. If no name given, reconnect all disconnected servers.

**Session resume note:** Resumed sessions may contain MCP tool history from servers no longer configured. This is fine — the history is just conversation context. No dependency check needed.

#### Step 2.3: MCP Tool Display in Chat

MCP tool calls are displayed the same as built-in tools:
- `[mcp__sentry__search_issues]` label with the tool input
- Result displayed below

Consider showing a shorter display name: `[sentry: search_issues]` instead of the full namespaced name.

#### Step 2.4: Tests for Phase 2

- Confirmation dialog for MCP tools
- Permission checking with `mcp__` prefix
- `/mcp` command output format

### Phase 3: HTTP Transport and TUI Management

#### Step 3.1: HTTP Transport

For servers with `type: "http"`:
- Use the SDK's `StreamableClientTransport` or `SSEClientTransport`
- Pass headers from config
- Handle OAuth if needed (defer complex OAuth flows, support Bearer tokens)

#### Step 3.2: `/mcp add` and `/mcp remove` Commands

```
/mcp add <name> <command> [args...]     — add stdio server
/mcp add --http <name> <url>            — add HTTP server
/mcp remove <name>                       — remove server
```

Saves to `.mcp.json` (project scope) by default.

#### Step 3.3: Dynamic Tool Refresh

Handle `notifications/tools/list_changed` from MCP servers:
- Re-fetch tools when the server notifies of changes
- Update the tool list without restarting Ernest

#### Step 3.4: Tests for Phase 3

- HTTP transport connection
- `/mcp add` / `/mcp remove` with config persistence
- Tool list refresh on notification

---

## Phases & Dependency Graph

```
Phase 1 (Config loader + Manager + Tool routing) ──→ PR #1
    │
    ▼
Phase 2 (Agent integration + /mcp + display) ──→ PR #2
    │
    ▼
Phase 3 (HTTP transport + TUI management) ──→ PR #3
```

Each phase produces a working, testable state:
- After Phase 1: stdio MCP servers connect, tools discovered and callable with confirmation
- After Phase 2: /mcp status, reconnect, friendly display, full permissions
- After Phase 3: remote servers, TUI management, dynamic refresh

---

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Go MCP SDK adds significant binary size | Low | Medium | SDK is focused and well-maintained. Monitor binary size after adding. |
| MCP server startup slows Ernest launch | Medium | Medium | Connect servers in a goroutine. Ernest is usable immediately; MCP tools appear as servers connect. |
| MCP tool names collide with built-in tools | Low | Low | `mcp__` prefix prevents collisions by convention. |
| Env var expansion has security implications | Medium | Medium | Only expand in MCP config fields, not arbitrary strings. Log a warning if sensitive-looking vars are expanded. |
| Claude Code changes config format | Low | Medium | Config format is stable and documented. Version checking on `.mcp.json` if needed. |
| MCP server produces large output | Medium | Medium | Truncate MCP tool results to the same limits as built-in tools (100KB). |

---

## Scope Boundaries

This plan does **NOT** include:
- MCP resources (files, data) — only tools for now
- MCP prompts (templates) — only tools
- MCP server development (only client side)
- OAuth 2.0 flows for remote servers (Bearer token support only)
- MCP server auto-restart on crash (manual reconnect via `/mcp reconnect`)
- Claude Code plugin installation or management
- Bidirectional MCP features (sampling, elicitation — server asking the client)

---

## Implementation Checklist

### Phase 1: MCP Client Infrastructure
- [x] Pin Go MCP SDK version and validate API surface (Step 1.0)
- [x] Add `github.com/modelcontextprotocol/go-sdk` dependency
- [x] Create `internal/mcp/config.go` — config loader with env var expansion, server name validation
- [x] Create `internal/mcp/manager.go` — Manager, ConnectAll (30s timeout), CallTool (100KB truncation), Tools (sorted), Close, Reconnect
- [x] Add MCP tool name parsing (`strings.SplitN`) and routing in agent loop
- [x] MCP tools require confirmation unless explicitly allowed by permissions
- [x] Extend `PermissionChecker.Check()` to support glob patterns in tool names
- [x] Add `mcpManager` field to Agent struct
- [x] Combine built-in + MCP tool definitions in Run() (sorted, plan mode respects readOnlyHint)
- [x] Wire MCP manager into main.go (load config, connect, defer close)
- [x] Write config loader tests (parse, expansion, missing, server name validation)
- [ ] Write manager tests (mock server, tool routing, truncation)
- [ ] Verify: end-to-end with a real MCP server

### Phase 2: /mcp Command and Display
- [x] Add `/mcp` status command
- [x] Add `/mcp reconnect [name]` command
- [x] Friendly display names for MCP tools in chat (e.g., `[sentry: search_issues]`)
- [x] Write permission tests for MCP tool name globs (done in Phase 1)
- [ ] Verify: MCP tool call with confirmation dialog, auto-approve with glob

### Phase 3: HTTP Transport and TUI Management
- [x] Add HTTP transport support for remote servers (StreamableHTTP + SSE)
- [x] Implement `/mcp add` and `/mcp remove` commands
- [x] Handle `notifications/tools/list_changed` via `RefreshTools()`
- [x] Write HTTP transport and management tests
- [ ] Verify: connect to a remote MCP server (e.g., Sentry, GitHub)
