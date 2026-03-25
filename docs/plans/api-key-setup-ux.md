# API Key Management UX and First-Run Setup

## Date: 2026-03-25
## Status: Pending Verification
## GitHub Issue: #11

---

## Problem Statement

Ernest has no guided setup experience. A new user who downloads the binary and runs `ernest` gets a TUI with no providers configured and no indication of what to do. The only way to add keys today is to know about `/provider add <type> <key>` — undiscoverable without reading docs.

Additionally, the current `/provider add` requires pasting API keys into the TUI input, which may be visible on screen and saved in session history.

---

## Proposed Solution

### 1. Interactive first-run setup

When Ernest starts with no configured providers (no keys in env vars, credentials.yaml, or config.yaml), show a setup prompt instead of the empty chat:

```
Welcome to Ernest.

No providers configured. Let's set one up.

  Provider:  [anthropic]  openai  siliconflow  ollama
```

Use a picker to select the provider type, then prompt for the API key with masked input. Save to credentials.yaml and config.yaml, rebuild the router, and drop into the normal chat view.

### 2. `ernest --setup` flag

Allow re-running the setup flow at any time via `ernest --setup`. This opens the same interactive setup, useful for adding a second provider or changing keys.

### 3. `/provider add` with masked key input

When `/provider add <type>` is run without an API key argument, prompt for the key with masked input (dots/asterisks) instead of requiring it inline. The current inline syntax (`/provider add anthropic sk-...`) continues to work for scripting.

### 4. `/provider add` for known providers with less typing

For known providers (anthropic, openai, siliconflow, gemini), only the name and key are needed — model and base URL are auto-populated with sensible defaults. This already works today. Document it in the home screen.

### 5. Home screen hints

Update the home screen to show setup instructions when no providers are configured:

```
  No providers configured.
  Type /provider add <type> <key> to get started.
  Supported: anthropic, openai, siliconflow, gemini, ollama
```

---

## Data Model Changes

### New flag in `cmd/ernest/main.go`

```go
setupMode := flag.Bool("setup", false, "Run interactive provider setup")
```

### Masked input in TUI

Add a `masked` mode to `InputModel` that renders input as dots. Activated when prompting for API keys.

```go
type InputModel struct {
    // ... existing fields
    masked bool // render input as dots
}
```

### Setup state in AppModel

```go
type AppModel struct {
    // ... existing fields
    setupMode    bool         // in first-run setup flow
    setupStep    int          // 0=provider picker, 1=key input
    setupProvider string      // selected provider type
}
```

---

## Specific Scenarios to Cover

| # | Scenario | Expected Outcome |
|---|----------|------------------|
| 1 | First run, no keys anywhere | Setup flow starts automatically |
| 2 | First run, ANTHROPIC_API_KEY in env | Normal startup, no setup |
| 3 | `ernest --setup` with existing providers | Setup flow for adding another provider |
| 4 | `/provider add anthropic` (no key) | Masked input prompt for API key |
| 5 | `/provider add anthropic sk-...` (inline) | Works as before, no prompt |
| 6 | `/provider add ollama` (no key needed) | Adds with just base URL, no key prompt |
| 7 | Setup: user picks anthropic, enters key | Saved to credentials.yaml, chat starts |
| 8 | Setup: user presses Esc during setup | Exit Ernest cleanly |
| 9 | Headless mode, no keys | Error with clear instructions, exit 1 |

---

## Implementation Plan

### Step 1: Masked input mode for InputModel

Add `masked` field to `InputModel`. When true, `View()` renders the text content as dots (`•••••`) instead of the actual characters. The underlying text buffer still holds the real value.

### Step 2: First-run detection in main.go

After loading config + credentials, check if any provider has a resolvable API key or base URL. If none, set a flag that the TUI reads to enter setup mode.

```go
hasProvider := false
for _, pc := range cfg.SortedProviders() {
    if pc.ResolveAPIKeyWithCredentials(creds) != "" || pc.BaseURL != "" {
        hasProvider = true
        break
    }
}
```

### Step 3: Setup flow in TUI

When `setupMode` is true, the TUI skips the home screen and shows:

**Step 0 — Provider picker:**
Use the existing `PickerModel` with provider options. On selection, store the provider type and advance to step 1.

**Step 1 — API key input:**
Show a masked input prompt. For `ollama`, skip this step (no key needed, just set default base URL). On submit, save credentials + config, rebuild router, exit setup mode, show the normal chat.

### Step 4: `--setup` flag

Add `--setup` flag to main.go. When set, force `setupMode = true` regardless of whether providers exist.

### Step 5: `/provider add` masked input

When `/provider add <type>` is invoked without a key argument:
- Set `masked = true` on the input model
- Store the pending provider type
- On submit, treat the input as the API key, save, rebuild, reset input

### Step 6: Home screen update

When no providers are configured, replace the command hints section with setup instructions.

---

## Phases & Dependency Graph

Single phase — all changes ship together:

```
Masked input → First-run detection → Setup flow → --setup flag → /provider add prompt → Home screen → Tests → PR
```

---

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| API key visible in terminal scrollback | Low | Medium | Masked input prevents display. Keys are never in session history. |
| Setup flow blocks power users | Low | Low | Setup only triggers when zero providers configured. `--setup` is opt-in. |
| Masked input breaks copy-paste UX | Low | Low | Paste still works, just displays as dots. |

---

## Scope Boundaries

This plan does **NOT** include:
- OAuth flows for providers (only API key-based auth)
- Key rotation or expiration management
- Keychain/secret manager integration (credentials.yaml is the store)
- Multi-user setup (Ernest is a personal tool)

---

## Implementation Checklist

- [x] Add masked input mode to InputModel (render as dots)
- [x] Add first-run detection in main.go (no provider has a key or base URL)
- [x] Add setup flow to TUI: provider picker → masked key input → save → start chat
- [x] Add `--setup` flag to main.go
- [x] Update `/provider add <type>` to prompt for key with masked input when key omitted
- [x] Update home screen to show setup hints when no providers configured
- [x] Write tests for ProviderConfigForName defaults
- [ ] Verify: full flow from zero config to working chat
