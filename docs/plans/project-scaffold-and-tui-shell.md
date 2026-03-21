# Project Scaffold, Config System, and TUI Shell

## Date: 2026-03-21
## Status: Complete
## GitHub Issue: TBD

---

## Problem Statement

Ernest has a detailed spec (`SPEC.md`) but no code. Before we can build the Anthropic streaming provider or agent loop, we need the foundational project structure: a Go module, the config system, and a minimal BubbleTea TUI that launches, accepts input, and displays messages. This plan covers everything up to — but not including — the first API call.

Without this foundation, subsequent plans (Anthropic provider, tool system, agent loop) have nowhere to land.

---

## Proposed Solution

Initialize a Go module with the directory structure from the spec. Implement two config loaders — Ernest's own config (`~/.config/ernest/config.yaml`) and the Claude config loader (`.claude/` directory parsing). Build a minimal BubbleTea TUI with: a scrollable chat view, a multi-line input box, a status bar, and vim-style navigation keybindings (j/k scroll, gg/G jump, Esc to unfocus, i/Enter to focus input). The TUI will echo user messages back into the chat view as a placeholder until the provider is wired in.

---

## Data Model Changes

### Ernest Config (`internal/config/config.go`)

```go
type Config struct {
    Providers       []ProviderConfig `yaml:"providers"`
    CooldownSeconds int              `yaml:"cooldown_seconds"`
    MaxContextTokens int             `yaml:"max_context_tokens"`
}

type ProviderConfig struct {
    Name      string `yaml:"name"`
    APIKeyEnv string `yaml:"api_key_env"`
    Model     string `yaml:"model"`
    Priority  int    `yaml:"priority"`
}
```

### Claude Config (`internal/config/claude.go`)

```go
type ClaudeConfig struct {
    SystemPrompt   string
    Rules          []string
    AllowedTools   []string
    DeniedTools    []string
    PermissionMode string
}
```

### TUI Model (`internal/tui/app.go`)

```go
type AppModel struct {
    chatView    ChatModel      // scrollable message list
    input       InputModel     // multi-line textarea
    statusBar   StatusModel    // provider + model + tokens
    inputFocused bool          // toggles vim nav vs text input
    width       int
    height      int
}
```

---

## Specific Scenarios to Cover

| # | Scenario | Action | Expected Outcome |
|---|----------|--------|------------------|
| 1 | User runs `ernest` with no config file | Launch app | App starts with sensible defaults (Anthropic provider, claude-sonnet-4-20250514), shows warning in status bar that config was not found |
| 2 | User runs `ernest` with a valid config.yaml | Launch app | Config loaded, status bar shows configured provider and model |
| 3 | User runs `ernest` in a directory with `.claude/CLAUDE.md` | Launch app | Claude config is parsed and system prompt is assembled from CLAUDE.md content |
| 4 | User types a message and presses Enter (or Ctrl+D) | Submit input | Message appears in chat view as a "user" message. Placeholder response echoes it back (no API call yet). Input box clears. |
| 5 | User presses `j`/`k` when input is unfocused | Scroll | Chat view scrolls down/up |
| 6 | User presses `gg` when input is unfocused | Jump to top | Chat view scrolls to the first message |
| 7 | User presses `G` when input is unfocused | Jump to bottom | Chat view scrolls to the most recent message |
| 8 | User presses `Esc` when input is focused | Unfocus | Input loses focus, vim navigation becomes active |
| 9 | User presses `i` or `Enter` when input is unfocused | Focus input | Input box gains focus, ready for typing |
| 10 | User presses `Ctrl+C` | Exit | App exits cleanly |
| 11 | User presses `:q` when input is unfocused | Exit | App exits cleanly |

---

## Implementation Plan

### Step 1: Initialize Go Module and Dependencies

Create `go.mod` with module path `ernest`. Install dependencies:
- `github.com/charmbracelet/bubbletea` — TUI framework
- `github.com/charmbracelet/bubbles` — textarea, viewport components
- `github.com/charmbracelet/lipgloss` — terminal styling
- `github.com/charmbracelet/glamour` — markdown rendering (needed soon, install now)
- `gopkg.in/yaml.v3` — YAML config parsing

Create the directory structure from the spec:
```
cmd/ernest/main.go
internal/config/
internal/provider/
internal/tools/
internal/agent/
internal/tui/
```

Only `cmd/ernest/`, `internal/config/`, and `internal/tui/` will have code in this plan. The others get placeholder files so the structure exists.

### Step 2: Implement Config Loader (`internal/config/`)

**`internal/config/config.go`** — Ernest's own config:
- Define `Config` and `ProviderConfig` structs
- `Load()` function that reads `~/.config/ernest/config.yaml`
- Returns sensible defaults if the file doesn't exist (Anthropic, claude-opus-4-6-20250610, priority 1)
- Validates that referenced env vars exist (warning, not fatal)

**`internal/config/claude.go`** — Claude config loader:
- Implement `LoadClaudeConfig(projectDir string)` as specified in the spec
- Resolution order: `~/.claude/CLAUDE.md` → `~/.claude/settings.json` → `.claude/CLAUDE.md` → `.claude/settings.json` → `.claude/rules/*.md` → root `CLAUDE.md`
- Assemble system prompt from all found files

**`internal/config/providers.go`** — Provider config helpers:
- Helper to resolve API key from env var name
- Helper to get the highest-priority provider config

### Step 3: Implement TUI Styles (`internal/tui/styles.go`)

Define Lip Gloss styles for:
- Chat message bubbles (user vs assistant, visually distinct)
- Status bar (bottom of screen, contrasting background)
- Input box (bordered, clear focus indicator)
- General app layout (padding, borders)

Keep the palette minimal — works on both dark and light terminals.

### Step 4: Implement Status Bar (`internal/tui/status.go`)

A simple BubbleTea model that renders a single line at the bottom:
- Shows: provider name, model name, token count (0/0 for now)
- Updates via messages from the parent app model
- Stretches to full terminal width

### Step 5: Implement Input Component (`internal/tui/input.go`)

Wraps BubbleTea's `textarea.Model`:
- Multi-line input with standard editing (no vim modes)
- Submit on `Enter` (single newline on `Shift+Enter` or `Alt+Enter` for multi-line)
- Visual border that changes color when focused vs unfocused
- Returns a `SubmitMsg` to the parent when the user submits

### Step 6: Implement Chat View (`internal/tui/chat.go`)

Wraps BubbleTea's `viewport.Model`:
- Displays a list of messages (role + content)
- User messages styled differently from assistant messages
- Supports scrolling via viewport
- New messages auto-scroll to bottom unless user has scrolled up
- For now: when a user message is submitted, add an echo response ("Echo: <message>") as a placeholder

### Step 7: Implement App Model with Vim Navigation (`internal/tui/app.go`)

Root BubbleTea model that composes chat, input, and status bar:
- Layout: chat view fills available space, input at bottom (above status bar), status bar at very bottom
- Focus management: `inputFocused` bool
- Key dispatch:
  - When `inputFocused`: all keys go to input component, except `Esc` (unfocus)
  - When not focused: `j`/`k` scroll chat, `g` starts a "gg" sequence, `G` jumps to bottom, `i`/`Enter` focus input, `:` starts command mode (just `:q` for now), `Ctrl+C` quits
- Window resize handling: redistribute space to components
- Receives `SubmitMsg` from input, adds to chat, triggers echo response

### Step 8: Implement Entry Point (`cmd/ernest/main.go`)

- Load Ernest config (with defaults fallback)
- Load Claude config from current working directory
- Initialize the TUI app model
- Run `bubbletea.NewProgram(appModel, bubbletea.WithAltScreen())`
- Clean exit on error

### Step 9: Create Makefile

Cross-compilation targets as specified in the spec:
- `build-linux`, `build-mac-arm`, `build-mac-intel`, `build-all`
- Add `build` (native) and `run` targets for development

### Step 10: Write Tests

- `internal/config/config_test.go` — test loading config with/without file, defaults
- `internal/config/claude_test.go` — test Claude config assembly from various file combinations
- Test with temp directories to avoid polluting real config

---

## Phases & Dependency Graph

Single-phase implementation. This plan is small enough to be implemented and shipped in a single PR.

```
Step 1 (Go module init)
  ├── Step 2 (Config loaders)
  ├── Step 3 (Styles)
  │     ├── Step 4 (Status bar)
  │     ├── Step 5 (Input)
  │     └── Step 6 (Chat view)
  │           └── Step 7 (App model — composes 4, 5, 6)
  └── Step 9 (Makefile)

Step 7 + Step 2 → Step 8 (Entry point — wires config into TUI)
Step 2 → Step 10 (Tests)
```

---

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| BubbleTea textarea submit behavior differs from expectation (Enter vs Ctrl+D) | Medium | Low | Test early. BubbleTea textarea uses `Ctrl+D` or custom key handling for submit — we'll need to intercept `Enter` in the parent model |
| Vim "gg" two-key sequence is tricky in BubbleTea | Medium | Low | Use a timer-based approach or state machine: first `g` sets a pending state, second `g` within 500ms triggers the action |
| Config path differences across OS | Low | Medium | Use `os.UserHomeDir()` and `os.UserConfigDir()` — both are cross-platform in Go's stdlib |

---

## Scope Boundaries

This plan does **NOT** include:
- Any API calls to any provider (that's the next plan)
- The provider interface or router implementation (placeholder files only)
- The tool system or agent loop
- The command palette (beyond `:q`)
- Conversation search (`/`)
- Session persistence (save/resume)
- Markdown rendering of messages (plain text for now, glamour comes with the provider plan)

---

## Implementation Checklist

- [x] Initialize Go module with dependencies
- [x] Create directory structure per spec
- [x] Implement `internal/config/config.go` — Ernest config loader with defaults
- [x] Implement `internal/config/claude.go` — Claude config loader
- [x] Implement `internal/config/providers.go` — provider config helpers
- [x] Implement `internal/tui/styles.go` — Lip Gloss style definitions
- [x] Implement `internal/tui/status.go` — status bar component
- [x] Implement `internal/tui/input.go` — multi-line input with submit
- [x] Implement `internal/tui/chat.go` — scrollable chat view with echo placeholder
- [x] Implement `internal/tui/app.go` — root model with vim-style key dispatch
- [x] Implement `cmd/ernest/main.go` — entry point
- [x] Create `Makefile` with build targets
- [x] Write config loader tests
- [x] Verify: `go build ./cmd/ernest` produces a working binary
- [x] Verify: binary launches TUI, accepts input, echoes messages, vim nav works
