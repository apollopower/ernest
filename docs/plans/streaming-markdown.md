# Streaming Markdown Rendering

## Date: 2026-03-25
## Status: Pending Verification
## GitHub Issue: #42

---

## Problem Statement

Markdown formatting (headers, bold, code blocks, tables) only renders after the full LLM response completes. During streaming, raw markdown syntax is visible (`**bold**`, `# heading`, triple backticks). Long responses are hard to read while streaming.

---

## Proposed Solution

Render markdown incrementally during streaming using glamour. The key changes:

1. **Apply glamour during streaming**, not just after finalization
2. **Debounce rendering** — don't call glamour on every character delta; batch at intervals
3. **Sanitize partial markdown** — close unclosed code fences and other block-level elements before passing to glamour so it doesn't produce broken output

### Design:

The current flow is:
```
text_delta → AppendToMessage → renderMessages → renderAssistantContent → plain text (streaming)
```

The new flow:
```
text_delta → AppendToMessage → renderMessages → renderAssistantContent → glamour(sanitize(content))
```

With debouncing:
```
text_delta → AppendToMessage (accumulate) → [debounce timer fires] → renderMessages with glamour
```

---

## Data Model Changes

### ChatMessage changes

Add a cached rendered string to avoid re-rendering unchanged messages:

```go
type ChatMessage struct {
    // ... existing fields
    rendered       string // cached glamour output
    renderedLen    int    // content length when rendered was computed
}
```

### Debounce mechanism

Use a `time.Ticker` or `tea.Tick` to batch streaming renders. Instead of rendering on every `AppendToMessage`, set a dirty flag and render on the next tick (e.g., every 50ms). This gives 20fps rendering which is smooth enough for reading while keeping CPU usage manageable.

---

## Specific Scenarios to Cover

| # | Scenario | Expected Outcome |
|---|----------|------------------|
| 1 | Streaming plain text | Renders immediately, no markdown artifacts |
| 2 | Streaming with `**bold**` mid-sentence | Bold renders as text streams past closing `**` |
| 3 | Streaming a code block (triple backticks) | Fence opens, code renders, closes when fence closes |
| 4 | Partial code fence (open but not closed) | Sanitizer closes it, renders as code block |
| 5 | Streaming a markdown table | Table renders progressively as rows arrive |
| 6 | Very long response (10K+ tokens) | Debouncing prevents lag, scroll stays at bottom |
| 7 | Finalization after streaming | Final render matches streaming render (no visual jump) |
| 8 | Multiple messages in history | Only re-render the streaming message, use cache for others |

---

## Implementation Plan

### Step 1: Sanitize partial markdown

Add `sanitizePartialMarkdown(content string) string` that:
- Counts unclosed triple-backtick fences and closes them
- This is the minimal fix that handles the most common case (code blocks are the most visually broken element during streaming)
- Other elements (bold, italic, headers) degrade gracefully without sanitization

### Step 2: Enable glamour during streaming

In `renderAssistantContent`, remove the `if msg.streaming` guard that falls back to plain text. Instead, always render through glamour with sanitization:

```go
func (m *ChatModel) renderAssistantContent(msg ChatMessage) string {
    if msg.Content == "" {
        // ... dot animation
    }
    content := msg.Content
    if msg.streaming {
        content = sanitizePartialMarkdown(content)
    }
    rendered, err := m.renderer.Render(content)
    if err != nil {
        return assistantMsgStyle.Render(msg.Content)
    }
    return strings.TrimSpace(rendered)
}
```

### Step 3: Debounce streaming renders

Instead of calling `renderMessages()` on every `AppendToMessage`, accumulate text and render on a timer:

- Add `renderDirty bool` flag to ChatModel
- `AppendToMessage` sets `renderDirty = true` but does NOT call `renderMessages()`
- A `streamRenderTickMsg` fires every 50ms; if `renderDirty`, call `renderMessages()` and reset the flag
- `FinalizeMessage` forces an immediate render (no waiting for tick)

### Step 4: Cache rendered output for non-streaming messages

To avoid re-rendering the entire history on every tick:
- Store rendered output in `ChatMessage.rendered`
- Only re-render if `len(msg.Content) != msg.renderedLen`
- Streaming message is always re-rendered (content changes)
- Finalized messages use cache

---

## Phases & Dependency Graph

Single phase:

```
Sanitizer → Glamour during streaming → Debounce → Cache → Tests → PR
```

---

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Glamour is slow on large content | Medium | Medium | Debounce at 50ms + cache non-streaming messages |
| Partial markdown produces broken rendering | Medium | Low | Sanitizer closes unclosed fences; glamour handles most other cases |
| Scroll position jumps on re-render | Medium | Medium | GotoBottom only during streaming; preserve position otherwise |
| Visual "flash" between streaming and final render | Low | Low | Both use glamour — output should be identical |

---

## Scope Boundaries

This plan does **NOT** include:
- Syntax highlighting within code blocks (glamour handles this if the terminal supports it)
- Incremental/partial glamour rendering (re-renders full message each time, with debounce)
- Custom markdown extensions beyond standard CommonMark

---

## Implementation Checklist

- [x] Add `sanitizePartialMarkdown` function (close unclosed code fences)
- [x] Enable glamour rendering during streaming with sanitization
- [x] Add debounce: `renderDirty` flag + 50ms tick for streaming renders
- [x] Cache rendered output on ChatMessage to avoid re-rendering history
- [x] Write tests for sanitizePartialMarkdown
- [ ] Verify: streaming markdown looks correct, no lag on long responses
