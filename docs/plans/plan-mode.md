# Plan Mode: Structured Planning Workflow

## Date: 2026-03-23
## Status: Draft
## GitHub Issue: #16

---

## Problem Statement

Ernest is built on the principle "no code without a plan" — every non-trivial change should have a plan file in `docs/plans/` before implementation begins. Currently, this planning workflow is manual: the user writes the plan themselves or asks the model to help, but the model has full write access and no structural guidance. There's no distinction between "thinking about what to build" and "building it."

Plan mode makes this workflow first-class: a dedicated mode where Ernest focuses on understanding the codebase, designing solutions, and producing structured plan documents — without accidentally modifying files along the way.

---

## Proposed Solution

Add a modal mode system to Ernest with two modes: **plan mode** and **build mode** (the default).

**Plan mode** (`/plan`):
- Write tools (write_file, str_replace, bash) are disabled — only read-only tools (read_file, glob, grep) are available
- The system prompt is augmented with planning-specific instructions and the plan file convention from the spec
- The status bar shows a "PLAN" indicator
- The model is guided to explore the codebase, ask clarifying questions, and produce a structured plan document following the required sections
- When the plan is ready, the user can save it with `/plan save <filename>` which writes to `docs/plans/<filename>.md`

**Build mode** (`/build`):
- All tools are available (the current default behavior)
- Status bar shows nothing (clean default)
- The model can execute the plan by reading it and implementing step by step

The mode is a property of the agent — it persists across turns, is saved with the session, and survives compaction (since compaction replaces conversation history but not agent state).

**Mode transitions inject a message into conversation history** so the model is explicitly informed in-context that the rules changed.

---

## Data Model Changes

### Agent Mode (`internal/agent/loop.go`)

```go
type AgentMode string

const (
    ModeBuild AgentMode = "build"  // default — all tools available
    ModePlan  AgentMode = "plan"   // read-only tools + planning prompt
)
```

Add `mode AgentMode` field to `Agent` struct. The mode affects:
1. Which tools are passed to `router.Stream()` (filtered by mode)
2. The system prompt (planning instructions appended in plan mode)

### Plan Mode System Prompt

```go
const planModePrompt = `You are in PLAN MODE. Your job is to design, not implement.

You only have read-only tools available. Use them to explore the codebase
and understand the existing architecture before designing your solution.

Guidelines:
- Explore the codebase to understand what exists before proposing changes
- Ask clarifying questions if the user's goal is ambiguous
- Produce a structured plan following this format:

# Plan Title
## Date: YYYY-MM-DD
## Status: Draft
## GitHub Issue: #<number>

Required sections (in order):
1. Problem Statement
2. Proposed Solution
3. Data Model Changes
4. Specific Scenarios to Cover
5. Implementation Plan
6. Phases & Dependency Graph
7. Risks and Mitigations
8. Scope Boundaries
9. Implementation Checklist

Be thorough but terse. Reference specific file paths and function names.`
```

### Status Bar Update

Add mode display to `StatusModel`:
- Plan mode: show `PLAN` in a distinct color (yellow/amber) before the provider name
- Build mode: show nothing (default, clean)

### Session Persistence

Add `Mode AgentMode` field to `session.Session` so the mode persists across restarts.

---

## Specific Scenarios to Cover

| # | Scenario | Action | Expected Outcome |
|---|----------|--------|------------------|
| 1 | User enters plan mode | `/plan` | Status bar shows PLAN indicator, system message confirms mode change |
| 2 | User asks to read a file in plan mode | "Read main.go" | read_file tool works normally |
| 3 | Model tries to write a file in plan mode | Model calls write_file | Tool is not available — model's tool list doesn't include it |
| 4 | User asks to save the plan | `/plan save my-feature` | Model consolidates plan into single message, written to `docs/plans/my-feature.md` |
| 5 | User exits plan mode | `/build` | All tools restored, status bar updated, system message confirms |
| 6 | User resumes a session that was in plan mode | `/resume {id}` | Plan mode restored, status bar shows PLAN |
| 7 | User enters plan mode in headless | `ernest --headless --plan` | Plan mode active, write tools disabled |
| 8 | Mode persists across turns | Multiple prompts in plan mode | Mode doesn't reset between turns |
| 9 | User types `/plan` while already in plan mode | `/plan` | System message: "Already in plan mode" |
| 10 | User types `/build` while already in build mode | `/build` | System message: "Already in build mode" |

---

## Implementation Plan

### Step 1: Add AgentMode to Agent

In `internal/agent/loop.go`:
- Add `mode AgentMode` field to `Agent` struct
- Add `mode AgentMode` parameter to `agent.New()` (default: `ModeBuild`)
- Add `SetMode(mode AgentMode)` method (for runtime switching via `/plan` and `/build`)
- Add `Mode() AgentMode` getter
- In `Run()`, filter `toolDefs` based on mode:
  - `ModeBuild`: all tools (current behavior)
  - `ModePlan`: only read-only tools — hardcoded set: `read_file`, `glob`, `grep`
- In `Run()`, append `planModePrompt` to system prompt when in plan mode
- **Defense in depth:** In `executeToolWithConfirmation`, reject any tool call not in the read-only set when in plan mode. This guards against model hallucination of tool names that aren't in the tool definitions but exist in the registry.

### Step 2: Add Plan Mode System Prompt

In `internal/agent/system_prompt.go`:
- Add `PlanModePrompt` constant with planning instructions and plan file format
- The plan format mirrors the spec's required sections

### Step 3: Add Mode to Session

In `internal/session/session.go`:
- Add `Mode string` field to `Session` struct (json tag: `"mode,omitempty"`)
- On save: persist current agent mode
- On load: restore mode. Invalid or empty values default to `"build"`.

### Step 4: Add Status Bar Mode Indicator

In `internal/tui/status.go`:
- Add `mode string` field to `StatusModel`
- Add `Mode` field to `StatusUpdateMsg`
- Render mode indicator before provider name:
  - Plan mode: `[PLAN]` in amber/yellow
  - Build mode: nothing (clean default)

### Step 5: Add /plan and /build Commands

In `internal/tui/app.go`:

**`/plan`** command:
- Set agent mode to `ModePlan` via `agent.SetMode()`
- Inject mode-change message into agent history: `[Mode changed to PLAN. Only read-only tools are available.]`
- Update status bar with mode indicator
- Show system message: "Entered plan mode. Read-only tools only. Use /build to return."

**`/plan save <filename>`** command:
- Send a synthetic prompt to the agent: "Output the complete plan document in a single markdown message, following the required plan format."
- Capture the streamed response
- Write the response text to `docs/plans/<filename>.md` (use `os.MkdirAll` to create directory if needed)
- Show system message confirming the save with file path
- This approach ensures a consolidated document even when the plan was developed across multiple tool-using turns

**`/build`** command:
- Set agent mode to `ModeBuild` via `agent.SetMode()`
- Inject mode-change message into agent history
- Update status bar (remove mode indicator)
- Show system message: "Entered build mode. All tools available."

### Step 6: Add Read-Only Tool Filter

In `internal/agent/loop.go` (not tools.go — this is an agent concern, not a registry concern):

Hardcoded read-only tool set:
```go
var readOnlyTools = map[string]bool{
    "read_file": true,
    "glob":      true,
    "grep":      true,
}
```

In `Run()`, when `mode == ModePlan`, filter `registry.ToolDefs()` to only include tools in `readOnlyTools`. Do not use `RequiresConfirmation` as a proxy — the correlation between "read-only" and "no confirmation needed" is coincidental and will break if new tools are added.

### Step 7: Headless Mode Support

In `cmd/ernest/main.go`:
- Add `--plan` flag that starts in plan mode
- Pass to agent on creation

### Step 8: Tests

- Agent mode switching (build → plan → build)
- Tool filtering in plan mode (write tools excluded)
- System prompt includes plan instructions in plan mode
- Session save/load with mode
- `/plan save` writes correct file

---

## Phases & Dependency Graph

Single-phase implementation. This is small enough for a single PR.

```
Step 1 (AgentMode) + Step 2 (Plan prompt)
    │
    ├── Step 3 (Session persistence)
    ├── Step 4 (Status bar)
    ├── Step 5 (Commands)
    └── Step 6 (Tool filter)
          │
          └── Step 7 (Headless) + Step 8 (Tests)
```

---

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Model ignores plan mode and tries to write anyway | Medium | Low | Tool definitions are filtered — write tools aren't in the tool list, so the model can't call them. This is enforced at the API level, not just prompt-level. |
| `/plan save` extracts wrong content | Medium | Medium | Extract from the last assistant message. If the model produced the plan across multiple messages, the user may need to copy manually. Document this limitation. |
| Plan format drift from spec | Low | Low | Plan mode prompt includes the exact required sections. If the spec changes, update the prompt. |
| Mode indicator clutters the status bar | Low | Low | Only shown in plan mode. Build mode is clean (no indicator). |

---

## Scope Boundaries

This plan does **NOT** include:
- Plan file templates or scaffolding (the model generates the full plan)
- Plan review/approval workflow
- Automatic GitHub issue creation from plans
- Plan file parsing or validation
- Diff between plan and implementation
- Plan-specific JSON output event types (mode works with `--output json` but no `mode_changed` events)

---

## Implementation Checklist

- [ ] Add `AgentMode` type and `mode` field to Agent (constructor param + SetMode/Mode)
- [ ] Add `PlanModePrompt` constant with plan format instructions (no write tool names)
- [ ] Add hardcoded `readOnlyTools` set in agent
- [ ] Filter tool definitions by mode in `Run()` — plan mode gets read-only only
- [ ] Add defense-in-depth guard in `executeToolWithConfirmation` for plan mode
- [ ] Inject mode-change messages into agent history on `/plan` and `/build`
- [ ] Add `Mode` field to Session struct for persistence (validate on load)
- [ ] Add mode indicator to status bar (PLAN in amber, nothing for build)
- [ ] Implement `/plan` command (enter plan mode)
- [ ] Implement `/plan save <filename>` command (prompt model to consolidate, write to docs/plans/)
- [ ] Implement `/build` command (exit plan mode)
- [ ] Add `--plan` flag to headless mode (works with both output formats)
- [ ] Add known commands: "plan", "build"
- [ ] Write tests: mode switching, tool filtering, defense-in-depth guard, session persistence
- [ ] Verify: end-to-end plan mode with real API
