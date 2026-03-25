# Provider Retry with Exponential Backoff

## Date: 2026-03-25
## Status: Pending Verification
## GitHub Issue: #38

---

## Problem Statement

When a provider returns a transient error (429 rate limit, 5xx server error), the router immediately falls back to the next provider. This means a brief TPM spike causes the entire conversation to switch providers, even though the original would likely succeed seconds later.

Observed in practice: SiliconFlow/MiniMaxAI hits TPM limits after large MCP tool results, causing immediate fallback to Anthropic. The TPM limit resets within seconds, so a short retry would avoid the switch.

---

## Proposed Solution

Add retry with exponential backoff to the router before falling back to the next provider. Only retryable errors (429, 5xx) trigger retries. Non-retryable errors (401, 403, 400) fall back immediately.

### Retry behavior:
- **Max retries:** 3 attempts per provider before falling back
- **Backoff:** exponential — 1s, 2s, 4s (base 1s, multiplier 2x)
- **Retry-After header:** if present on a 429 response, use it instead of the calculated backoff (capped at 30s to prevent indefinite waits)
- **Cancellation:** retries respect `ctx.Done()` so Ctrl+C aborts immediately
- **Non-retryable errors:** 401, 403, 400, and other 4xx (except 429) skip retries and fall back immediately

### What changes:
1. A structured `APIError` type replaces the current `fmt.Errorf("API error (status %d)")` strings, carrying the HTTP status code and optional `Retry-After` duration
2. Both providers (`anthropic.go`, `openai_compat.go`) return `*APIError` instead of plain errors for HTTP failures
3. The router's `Stream()` method retries retryable errors with backoff before trying the next provider

---

## Data Model Changes

### `provider.APIError` (new type in `provider.go`)

```go
type APIError struct {
    StatusCode  int
    Body        string
    RetryAfter  time.Duration // from Retry-After header, 0 if absent
}

func NewAPIError(resp *http.Response, body []byte) *APIError
func IsRetryable(err error) bool  // true for 429, 5xx
```

### Router config (constants in `router.go`)

```go
const (
    maxRetries     = 3
    baseRetryDelay = 1 * time.Second
    maxRetryAfter  = 30 * time.Second
)
```

---

## Specific Scenarios to Cover

| # | Scenario | Expected Outcome |
|---|----------|------------------|
| 1 | Provider returns 429 (rate limit) | Retry up to 3 times with backoff, then fall back |
| 2 | Provider returns 429 with Retry-After: 2 | Wait 2s, retry, succeed on second attempt |
| 3 | Provider returns 429 with Retry-After: 120 | Cap at 30s, retry after 30s |
| 4 | Provider returns 500 (server error) | Retry up to 3 times with backoff, then fall back |
| 5 | Provider returns 401 (unauthorized) | No retry, fall back immediately |
| 6 | Provider returns 400 (bad request) | No retry, fall back immediately |
| 7 | All retries exhausted, fallback succeeds | Fallback provider used, user sees switch message |
| 8 | All retries exhausted, all providers fail | "all providers exhausted" error |
| 9 | User presses Ctrl+C during retry wait | Retry cancelled immediately |
| 10 | First attempt succeeds | No retries, no delay — zero overhead for happy path |

---

## Implementation Plan

### Step 1: Add `APIError` type to `provider.go`

- `APIError` struct with `StatusCode`, `Body`, `RetryAfter`
- `NewAPIError(resp, body)` constructor that parses `Retry-After` header
- `IsRetryable(err)` helper: true for 429, 5xx

### Step 2: Update providers to return `*APIError`

- `anthropic.go`: replace `fmt.Errorf("API error (status %d): %s", ...)` with `return nil, NewAPIError(resp, bodyBytes)`
- `openai_compat.go`: same change
- Existing tests that check for `"status 500"` in error strings will still work because `APIError.Error()` produces the same format

### Step 3: Add retry loop to `router.go`

Replace the current single-attempt-per-provider loop with:

```go
for _, p := range candidates {
    var lastErr error
    for attempt := 0; attempt <= maxRetries; attempt++ {
        if attempt > 0 {
            delay := retryDelay(lastErr, attempt)
            log.Printf("[router] retrying %s in %v (attempt %d/%d)", p.Name(), delay, attempt, maxRetries)
            select {
            case <-time.After(delay):
            case <-ctx.Done():
                return nil, "", ctx.Err()
            }
        }
        ch, err := p.Stream(ctx, systemPrompt, messages, tools)
        if err == nil {
            r.mu.Lock()
            r.markHealthy(p.Name())
            r.mu.Unlock()
            return ch, p.Name(), nil
        }
        lastErr = err
        if !IsRetryable(err) {
            break // non-retryable, fall back immediately
        }
    }
    log.Printf("[router] %s failed after retries: %v, trying next", p.Name(), lastErr)
    r.mu.Lock()
    r.markUnhealthy(p.Name())
    r.mu.Unlock()
}
```

The `retryDelay` function computes the delay:
- If the error has a `RetryAfter` value (from 429), use it (capped at `maxRetryAfter`)
- Otherwise, use exponential backoff: `baseRetryDelay * 2^(attempt-1)`

### Step 4: Tests

- `TestRetryOn429`: mock provider fails with 429 twice, succeeds on third — no fallback
- `TestRetryRespectRetryAfter`: mock provider returns 429 with RetryAfter, verify delay is used
- `TestNoRetryOn401`: mock provider fails with 401 — immediate fallback, no retries
- `TestRetryExhausted`: mock provider fails 429 all attempts — falls back to second provider
- `TestRetryCtxCancel`: cancel context during retry wait — returns immediately
- `TestAPIError`: verify Error() string format, IsRetryable for various status codes

---

## Phases & Dependency Graph

Single phase — all changes ship together:

```
APIError type → Provider updates → Router retry loop → Tests → PR
```

---

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Retry adds latency on genuine failures | Medium | Low | Max total retry time is ~7s (1+2+4). Ctrl+C cancels immediately. |
| Retry-After header has absurd value | Low | Medium | Cap at 30s. Log the original value for debugging. |
| Retry masks a real configuration error | Low | Low | Non-retryable errors (401, 403) skip retries entirely. |
| Happy path overhead | None | None | Zero overhead — retry loop only activates on error. |

---

## Scope Boundaries

This plan does **NOT** include:
- Circuit breaker pattern (tracked via existing cooldown mechanism)
- Jitter on backoff delays (can add later if retry storms become an issue)
- Per-provider retry configuration (all providers share the same retry policy)
- Retry on stream errors mid-response (only retries the initial connection)

---

## Implementation Checklist

- [x] Add `APIError` type with `NewAPIError`, `IsRetryable` to `provider.go`
- [x] Update `anthropic.go` to return `*APIError` for HTTP errors
- [x] Update `openai_compat.go` to return `*APIError` for HTTP errors
- [x] Add retry loop with exponential backoff to `router.go`
- [x] Add `retryDelay` helper that respects `Retry-After` header
- [x] Write router retry tests (429, 401, exhausted, cancel, RetryAfter)
- [x] Write `APIError`/`IsRetryable` unit tests
- [x] Verify: existing provider tests still pass unchanged
