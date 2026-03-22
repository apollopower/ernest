# Multi-Provider Support with TUI Configuration

## Date: 2026-03-22
## Status: Draft
## GitHub Issue: #10

---

## Problem Statement

Ernest currently only connects to Anthropic, and requires the API key to be set as a shell environment variable (`export ANTHROPIC_API_KEY=...`). This creates three problems:

1. **No resilience** — if Anthropic is down, Ernest is unusable. The router exists but has only one provider.
2. **No portability** — API keys are per-shell session, lost on reboot. Every new terminal needs re-configuration.
3. **No choice** — users can't pick a different model or provider for different tasks (e.g., a cheaper model for simple questions).

This plan adds multi-provider support with machine-wide credential storage and a TUI interface for managing providers on the fly.

---

## Proposed Solution

Implement in three phases:

**Phase 1** adds the OpenAI-compatible provider implementation (covers OpenAI, SiliconFlow, Together, Ollama, and any API that speaks the Chat Completions protocol). Also adds machine-wide credential storage in `~/.config/ernest/credentials.yaml` with `0600` permissions. First-launch experience prompts users to configure a provider.

**Phase 2** adds the Gemini provider (separate API format) and the TUI provider management commands: `/providers`, `/provider add`, `/provider remove`, `/model`.

**Phase 3** adds fallback priority management and the `/provider order` command for reordering providers.

---

## Data Model Changes

### Credentials File (`~/.config/ernest/credentials.yaml`)

```yaml
# Machine-wide credentials — follows you everywhere.
# File permissions: 0600 (owner read/write only).
# Contains ONLY secrets — no config like base_url or model.
providers:
  - name: anthropic
    api_key: sk-ant-...

  - name: siliconflow
    api_key: sf-...

  - name: openai
    api_key: sk-...
```

Separate from `config.yaml` so credentials are never mixed with shareable configuration. Only secrets live here — `base_url`, `model`, and `priority` live in config.yaml. The `api_key_env` approach still works as an override for known providers (env vars take precedence over stored credentials).

### Updated Config (`~/.config/ernest/config.yaml`)

```yaml
providers:
  - name: anthropic
    model: claude-opus-4-6
    priority: 1

  - name: siliconflow
    model: deepseek-ai/DeepSeek-R1
    base_url: https://api.siliconflow.com/v1
    priority: 2

  - name: openai
    model: gpt-4.1
    priority: 3

cooldown_seconds: 30
max_context_tokens: 180000
```

Note: `api_key_env` is deprecated but still read for backward compatibility. New setups use credentials.yaml for API keys. Config.yaml specifies model, priority, and optional base_url.

### Provider Config Resolution

```
1. Read providers from config.yaml (model, priority, base_url)
2. For each provider, resolve API key:
   a. Check config.yaml api_key_env if present (deprecated, backward compat)
   b. Check conventional env var for known providers only:
      - "anthropic" → ANTHROPIC_API_KEY
      - "openai" → OPENAI_API_KEY
      - "gemini" → GEMINI_API_KEY
      (Custom provider names do NOT get automatic env var lookup)
   c. Check credentials.yaml — machine-wide stored key
   d. If none found: provider is unconfigured (skip or prompt)
3. Build provider instances, pass to router in priority order
```

### OpenAI-Compatible Provider (`internal/provider/openai_compat.go`)

```go
type OpenAICompat struct {
    name   string       // "openai", "siliconflow", custom name
    apiKey string
    model  string
    baseURL string      // e.g., "https://api.openai.com/v1" or "https://api.siliconflow.com/v1"
    client *http.Client
}
```

Implements the `Provider` interface. Handles:
- Chat Completions streaming (`/chat/completions` with `stream: true`)
- SSE parsing (OpenAI format: `data: {"choices": [{"delta": {"content": "..."}}]}`)
- Tool use mapping (Ernest tools → OpenAI function calling format)
- Message format conversion (Ernest messages → OpenAI chat format)

### Gemini Provider (`internal/provider/gemini.go`)

```go
type Gemini struct {
    apiKey string
    model  string
    client *http.Client
}
```

Implements `Provider` for Google's Gemini API. Separate format from OpenAI.

### Credential Store (`internal/config/credentials.go`)

```go
type Credentials struct {
    Providers []ProviderCredential `yaml:"providers"`
}

type ProviderCredential struct {
    Name   string `yaml:"name"`
    APIKey string `yaml:"api_key"`
}

func LoadCredentials() (*Credentials, error)
func SaveCredentials(creds *Credentials) error   // atomic: write-to-temp-then-rename
func (c *Credentials) GetKey(providerName string) string
func (c *Credentials) SetKey(providerName, apiKey string)
func (c *Credentials) Remove(providerName string)
```

File: `~/.config/ernest/credentials.yaml`, permissions `0600`.

**Atomic writes:** `SaveCredentials` writes to a temp file in the same directory, then `os.Rename` to the target path. Rename is atomic on POSIX filesystems. This prevents credential loss on crash mid-write.

---

## Specific Scenarios to Cover

| # | Scenario | Action | Expected Outcome |
|---|----------|--------|------------------|
| 1 | First launch, no config | Start ernest | "No providers configured" message + instructions to run `/provider add` |
| 2 | `/provider add` for Anthropic | Enter API key | Key saved to credentials.yaml, provider available immediately |
| 3 | `/provider add` for SiliconFlow | Enter API key + base URL | OpenAI-compatible provider configured with custom base URL |
| 4 | API key already in env var | Launch with ANTHROPIC_API_KEY set | Env var used, credentials.yaml not needed |
| 5 | `/providers` | Run command | Lists all configured providers with status (connected/unconfigured), current model, priority |
| 6 | `/model siliconflow deepseek-ai/DeepSeek-R1` | Switch model | Model for siliconflow changes, next prompt uses new model |
| 7 | Anthropic down, SiliconFlow configured | Send prompt | Router falls back to SiliconFlow, status bar shows active provider |
| 8 | `/provider remove siliconflow` | Remove provider | Provider removed from config and credentials |
| 9 | New terminal, new directory | Launch ernest | Credentials loaded from ~/.config/ernest/credentials.yaml, all providers available |
| 10 | `/provider order` | Reorder | Change fallback priority, saved to config.yaml |
| 11 | OpenAI-compatible with tools | Tool call via SiliconFlow | Tools mapped to OpenAI function calling format, results fed back |

---

## Implementation Plan

### Phase 1: OpenAI-Compatible Provider and Credential Storage

#### Step 1.1: Credential Store (`internal/config/credentials.go`)

```go
func LoadCredentials() (*Credentials, error)   // reads ~/.config/ernest/credentials.yaml
func SaveCredentials(creds *Credentials) error  // atomic write (temp + rename), 0600 permissions
func (c *Credentials) GetKey(name string) string
func (c *Credentials) SetKey(name, apiKey string)
func (c *Credentials) Remove(name string)
func CredentialsPath() string                   // ~/.config/ernest/credentials.yaml
```

- Atomic writes: `SaveCredentials` writes to a temp file then renames, preventing credential loss on crash
- Create file with `0600` permissions on first write
- `GetKey` returns empty string if not found (callers check env var first)

#### Step 1.2: Update Config Resolution (`internal/config/config.go`)

Update `ProviderConfig`:
```go
type ProviderConfig struct {
    Name      string `yaml:"name"`
    APIKeyEnv string `yaml:"api_key_env,omitempty"` // optional, for backward compat
    Model     string `yaml:"model"`
    Priority  int    `yaml:"priority"`
    BaseURL   string `yaml:"base_url,omitempty"`    // for OpenAI-compatible providers
}
```

Add `ResolveAPIKeyWithCredentials(creds *Credentials) string`:
1. Check `os.Getenv(APIKeyEnv)` if `APIKeyEnv` is set (deprecated, backward compat)
2. Check conventional env var for known providers only (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY`). Custom provider names do not get automatic env var lookup.
3. Check `creds.GetKey(Name)`
4. Return empty if none found

Update `SaveConfig(cfg *Config) error` — write config.yaml back to disk (needed for `/provider add` and `/provider order`).

#### Step 1.3: OpenAI-Compatible Provider (`internal/provider/openai_compat.go`)

Implements `Provider` interface for any OpenAI Chat Completions-compatible API:

**Constructor:**
```go
func NewOpenAICompat(name, apiKey, model, baseURL string) *OpenAICompat
```

**Stream method:**
- POST to `{baseURL}/chat/completions`
- Headers: `Authorization: Bearer {apiKey}`, `Content-Type: application/json`
- Body: OpenAI chat format with `stream: true`
- Parse SSE: `data: {"choices": [{"delta": {"content": "..."}}]}`
- Map `finish_reason: "tool_calls"` to tool use events
- Handle `data: [DONE]` sentinel

**Message format conversion:**
- Ernest `Message` → OpenAI `{"role": "...", "content": "..."}`
- Ernest `ContentBlock{Type: "tool_use"}` → OpenAI `tool_calls` format
- Ernest `ContentBlock{Type: "tool_result"}` → OpenAI `{"role": "tool", "tool_call_id": "...", "content": "..."}`

**Tool definition mapping:**
- Ernest `ToolDef` → OpenAI `{"type": "function", "function": {"name": "...", "description": "...", "parameters": {...}}}`

**Tool call streaming accumulation:**
OpenAI streams tool calls differently from Anthropic. Tool call chunks arrive in `choices[0].delta.tool_calls[]` with an `index` field that multiplexes parallel tool calls. Key details:
- `id` and `function.name` appear only on the first chunk for each tool call
- Subsequent chunks carry only `function.arguments` fragments
- `finish_reason: "tool_calls"` appears on the final chunk, not alongside tool data
- Maintain a `map[int]*pendingToolCall` to accumulate fragments by index
- On `finish_reason: "tool_calls"`, emit all accumulated tool calls
- Some providers (SiliconFlow, Ollama) may not support parallel tool calls — the parser should handle both single and multi-tool responses gracefully

#### Step 1.4: Update Provider Factory (`cmd/ernest/main.go`)

Update provider creation to:
1. Load credentials
2. For each provider in config, resolve API key (env → credentials → skip)
3. Create provider based on name:
   - `"anthropic"` → `NewAnthropic()`
   - `"openai"`, `"siliconflow"`, or any with `base_url` → `NewOpenAICompat()`
   - `"gemini"` → Phase 2
4. Sort by priority, pass to router

#### Step 1.5: First-Launch Experience

If no providers have API keys (neither env vars nor credentials):
- TUI: show system message "No providers configured. Use /provider add to get started."
- Headless: print to stderr and exit 1

#### Step 1.6: Tests for Phase 1

- Credential store: save/load/get/set/remove, file permissions
- OpenAI-compatible provider: message conversion, SSE parsing, tool mapping
- Config resolution: env var → credentials → empty
- Provider factory: creates correct provider types

### Phase 2: Gemini Provider and TUI Provider Management

#### Step 2.1: Gemini Provider (`internal/provider/gemini.go`)

Google Gemini API implementation. Separate from OpenAI-compatible (different message format, different streaming protocol).

#### Step 2.2: `/providers` Command

Display formatted list of configured providers:
```
Providers:
  1. anthropic (claude-opus-4-6) — connected
  2. siliconflow (deepseek-ai/DeepSeek-R1) — connected
  3. openai (gpt-4.1) — no API key

Active: anthropic
```

#### Step 2.3: `/provider add` Command

Single-line syntax for terseness:

```
/provider add anthropic <api-key>
/provider add anthropic <api-key> --model claude-sonnet-4-6
/provider add siliconflow <api-key> --base-url https://api.siliconflow.com/v1 --model deepseek-ai/DeepSeek-R1
/provider add openai <api-key>
/provider add gemini <api-key>
```

Default models per provider type: `claude-opus-4-6` (anthropic), `gpt-4.1` (openai), `gemini-2.5-pro` (gemini).

**API key validation:** Before saving, make a lightweight API call to verify the key works (e.g., a minimal completion or models-list endpoint). If validation fails, show error and don't save. Validation is best-effort — skip for providers where it's not feasible (e.g., Ollama).

Save to credentials.yaml (key) and config.yaml (model, priority, base_url). Priority defaults to the next available slot (after existing providers).

**Router hot-swap:** After adding a provider, rebuild the router with the new provider set and swap it on the agent via `agent.SetRouter(newRouter)`. This requires adding a `SetRouter` method to the Agent struct (behind the existing mutex). The same mechanism is used by `/provider remove`, `/provider order`, and `/model`.

#### Step 2.4: Router Hot-Swap (`internal/agent/loop.go`)

Add `agent.SetRouter(router *provider.Router)` method:
```go
func (a *Agent) SetRouter(router *provider.Router) {
    a.mu.Lock()
    defer a.mu.Unlock()
    a.router = router
}
```

Called by TUI after `/provider add`, `/provider remove`, `/provider order`, or `/model` to apply provider changes without restart. Safe because:
- `Run()` copies the router reference at the start of each turn
- `SetRouter` is called between turns (input is blocked during streaming)

#### Step 2.5: `/provider remove <name>` Command

Remove provider from config.yaml and credentials.yaml. Confirm before removing.

#### Step 2.5: `/model <provider> <model>` Command

Switch the model for a specific provider. Example: `/model anthropic claude-sonnet-4-6` or `/model siliconflow deepseek-ai/DeepSeek-V3`. Updates config.yaml. Takes effect on next prompt (no restart).

The provider name is required to avoid ambiguity in multi-provider setups.

#### Step 2.6: Tests for Phase 2

- Gemini provider: message conversion, streaming
- `/providers` output format
- `/provider add` flow with mock input
- `/model` switching

### Phase 3: Fallback Priority Management

#### Step 3.1: `/provider order` Command

Show current priority order, allow reordering:
```
Current provider order:
  1. anthropic
  2. siliconflow
  3. openai

Enter new order (e.g., "2 1 3"):
```

Saves to config.yaml. Router rebuilt with new priority.

#### Step 3.2: Runtime Provider Switching

When the router falls back to a different provider:
- Status bar updates to show active provider name
- System message: "Switched to siliconflow (anthropic unavailable)"
- Cooldown timer shown in `/providers` output

#### Step 3.3: Tests for Phase 3

- Priority reordering
- Fallback with multiple providers
- Cooldown display

---

## Phases & Dependency Graph

```
Phase 1 (Credential store + OpenAI-compatible provider) ──→ PR #1
    │
    ▼
Phase 2 (Gemini + TUI provider management) ──→ PR #2
    │
    ▼
Phase 3 (Fallback priority management) ──→ PR #3
```

Each phase produces a working, testable state:
- After Phase 1: multi-provider works via config file editing, credentials stored machine-wide
- After Phase 2: full TUI management, add/remove/switch from within Ernest
- After Phase 3: reorder priorities, full fallback visibility

---

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| API key stored in plaintext YAML | High | Medium | File permissions 0600. Same approach as most CLI tools (gh, aws, gcloud). Users who need more can use env vars. |
| OpenAI function calling format differs from Anthropic tool use | Medium | Medium | Map Ernest's tool definitions to OpenAI's function calling schema. Test with real API calls. |
| SiliconFlow models don't support tool use | Medium | Medium | Graceful degradation: if model doesn't support tools, send prompts without tool definitions. Agent works in text-only mode. |
| Provider add flow is complex for TUI | Medium | Low | Keep it simple: numbered options + text input. No fancy UI. Falls back to config file editing for advanced cases. |
| Gemini API changes frequently | Low | Medium | Implement against the stable v1 API. Version-pin if needed. |
| Credential file conflicts on multi-machine setups | Low | Low | Credentials are per-machine by design. Not synced. Each machine configures independently. |

---

## Scope Boundaries

This plan does **NOT** include:
- OAuth/SSO authentication (API keys only)
- Credential encryption at rest (plaintext YAML, protected by file permissions)
- Cloud credential management (AWS Secrets Manager, etc.)
- Provider-specific features (Anthropic extended thinking, OpenAI vision, etc.)
- Streaming format auto-detection (provider type must be specified)
- Cost tracking per provider (separate issue #13)

---

## Implementation Checklist

### Phase 1: Credential Store and OpenAI-Compatible Provider
- [ ] Create `internal/config/credentials.go` — LoadCredentials, SaveCredentials, GetKey, SetKey, Remove
- [ ] Update `ProviderConfig` with BaseURL field
- [ ] Add `ResolveAPIKeyWithCredentials` to provider config
- [ ] Add `SaveConfig` for writing config.yaml back to disk
- [ ] Implement `internal/provider/openai_compat.go` — OpenAI Chat Completions streaming
- [ ] Implement OpenAI message format conversion (messages, tools, tool results)
- [ ] Update provider factory in main.go to use credentials + create OpenAI-compatible providers
- [ ] Add first-launch "no providers configured" message
- [ ] Write credential store tests
- [ ] Write OpenAI-compatible provider tests (message conversion, SSE parsing)
- [ ] Verify: end-to-end with SiliconFlow or OpenAI API

### Phase 2: Gemini Provider and TUI Management
- [ ] Implement `internal/provider/gemini.go` — Gemini API streaming
- [ ] Implement `agent.SetRouter()` for runtime router hot-swap
- [ ] Implement `/providers` command
- [ ] Implement `/provider add <type> <key>` command with API key validation
- [ ] Implement `/provider remove <name>` command
- [ ] Implement `/model <provider> <model>` command
- [ ] Add `SaveConfig` for writing config.yaml back to disk
- [ ] Rebuild router and call agent.SetRouter after provider changes
- [ ] Write Gemini provider tests
- [ ] Write provider management tests
- [ ] Verify: add provider from TUI, switch models, remove provider

### Phase 3: Fallback Priority Management
- [ ] Implement `/provider order` command
- [ ] Add runtime provider switch notification (system message + status bar)
- [ ] Show cooldown in `/providers` output
- [ ] Write priority reordering tests
- [ ] Verify: multi-provider fallback with reordering
