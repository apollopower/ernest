# Slash Command Autocomplete Menu

## Date: 2026-03-25
## Status: Pending Verification
## GitHub Issue: #43

---

## Problem Statement

Users need to know exact slash command names to use them. There's no discoverability beyond the home screen hints. Typos result in "Unknown command" errors. With 12+ commands now available, memorizing them all is unreasonable.

---

## Proposed Solution

Show a filtered autocomplete popup when the user types `/` in the input area. The popup appears above the input box, filters as the user continues typing, and allows selection via arrow keys or Enter. It does not block typing — the user can ignore it.

### Behavior:
- **Trigger:** Input text starts with `/` and the input is focused
- **Filter:** Matches commands where the name starts with the typed prefix (e.g., `/pro` shows `/providers`, `/provider`)
- **Navigation:** Up/Down arrows to move cursor, Enter to complete the selected command, Esc or Backspace past `/` to dismiss
- **Dismiss:** When input no longer starts with `/`, or when the user selects a command, or on Esc
- **Rendering:** Compact list rendered between chat and input, showing command name + short description
- **Non-blocking:** User can keep typing and submit normally — the autocomplete is a suggestion, not a gate

---

## Data Model Changes

### Command registry with descriptions

Replace the bare `knownCommands` map with a structured list:

```go
type CommandDef struct {
    Name string
    Desc string
}

var commands = []CommandDef{
    {"quit", "Exit Ernest"},
    {"status", "Session info"},
    {"save", "Save session"},
    {"clear", "Clear conversation"},
    {"compact", "Compact context"},
    {"resume", "Continue a session"},
    {"providers", "Show connections"},
    {"provider", "Add/remove/set provider"},
    {"model", "Switch provider/model"},
    {"plan", "Enter plan mode"},
    {"build", "Enter build mode"},
    {"mcp", "MCP server status"},
}
```

### AutocompleteModel

```go
type AutocompleteModel struct {
    items   []CommandDef  // filtered matches
    cursor  int           // selected index
    visible bool
}
```

Stored as a value field on `AppModel` (not a pointer — it's always present, just not always visible).

### InputModel change detection

Add a `Value()` accessor to `InputModel` so the app can read current input text on every key event without waiting for submission.

---

## Specific Scenarios to Cover

| # | Scenario | Expected Outcome |
|---|----------|------------------|
| 1 | User types `/` | Autocomplete shows all commands |
| 2 | User types `/pro` | Shows `/providers`, `/provider` |
| 3 | User types `/q` | Shows `/quit`, `/q` |
| 4 | User presses Down arrow | Cursor moves to next item |
| 5 | User presses Enter on selected item | Command name inserted into input, autocomplete dismissed |
| 6 | User presses Esc | Autocomplete dismissed, input unchanged |
| 7 | User deletes back past `/` | Autocomplete dismissed |
| 8 | User types a non-command like `/tmp/foo` | Autocomplete shows nothing (no matches), disappears |
| 9 | User submits with autocomplete visible | Normal submission, autocomplete dismissed |
| 10 | Autocomplete visible during streaming | Not shown (input is blurred during streaming) |

---

## Implementation Plan

### Step 1: Command registry

Replace `knownCommands` map with `commands` slice of `CommandDef`. Update `isKnownCommand` to search the slice. Add `q` as an alias for `quit` (both in the list).

### Step 2: AutocompleteModel

New file `internal/tui/autocomplete.go`:
- `filterCommands(prefix string) []CommandDef` — returns commands matching prefix
- `View()` — renders a compact bordered list above the input
- No `Update()` needed — the app drives cursor movement directly

### Step 3: Wire into AppModel

- Add `autocomplete AutocompleteModel` field
- On every `tea.KeyMsg` when input is focused (before delegating to textarea):
  - Read `m.input.Value()` after the textarea processes the key
  - If starts with `/`, filter and show autocomplete
  - If not, hide autocomplete
- Handle Up/Down arrows: if autocomplete is visible, move cursor instead of scrolling
- Handle Enter: if autocomplete is visible and has a selection, insert the command and dismiss
- Handle Esc: if autocomplete is visible, dismiss it

### Step 4: Rendering

In `View()`, when autocomplete is visible, render it between chat and input:

```
[chat view]
[autocomplete popup]  ← new
[input view]
[status bar]
```

The popup is a small bordered box, max 6 items, showing `name  description` per line with cursor highlight.

---

## Phases & Dependency Graph

Single phase:

```
Command registry → AutocompleteModel → AppModel wiring → View rendering → Tests → PR
```

---

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Autocomplete steals arrow keys from input | Medium | Medium | Only intercept when autocomplete is visible and has items |
| Performance on every keystroke | Low | Low | String prefix matching on 12 items is trivial |
| Layout shift when popup appears | Medium | Low | Fixed max height, chat view absorbs the space |

---

## Scope Boundaries

This plan does **NOT** include:
- Autocomplete for command arguments (e.g., provider names after `/provider add`)
- Fuzzy matching (prefix match only)
- Tab completion
- Custom user commands

---

## Implementation Checklist

- [x] Replace `knownCommands` map with `commands []CommandDef` with descriptions
- [x] Add `Value()` and `SetValue()` accessors to InputModel
- [x] Create `AutocompleteModel` in `autocomplete.go`
- [x] Wire autocomplete into AppModel Update (filter on input change)
- [x] Handle Up/Down/Tab/Esc for autocomplete navigation
- [x] Render autocomplete popup between chat and input in View
- [x] Write tests for command filtering, navigation, dismiss
- [ ] Verify: full flow from `/` to selection to execution
