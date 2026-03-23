# BubbleTea Patterns

## Value Receivers

`AppModel.Update()` uses a value receiver. Every mutation must happen on the local `m` and be returned — mutations inside closures or goroutines are silently lost. The `tea.Cmd` functions return messages; they do not mutate state directly.

## Async Work via tea.Cmd

Never block `Update()`. For async operations:

```go
// Good: return a tea.Cmd that does the work
func waitForAgentEvent(ch <-chan agent.AgentEvent) tea.Cmd {
    return func() tea.Msg {
        event, ok := <-ch
        if !ok { return StreamDoneMsg{} }
        return AgentEventMsg{Event: event}
    }
}

// Bad: blocking in Update
case SubmitMsg:
    result := <-someChannel // BLOCKS THE TUI
```

## Channel Reading Pattern

Read one event at a time from the agent channel. Each read returns one `tea.Msg`, and the handler schedules the next read:

```
SubmitMsg → waitForAgentEvent(ch)
AgentEventMsg → process event → waitForAgentEvent(ch)
StreamDoneMsg → done
```

Use `tea.Batch` when you need multiple concurrent commands (e.g., dot animation + event reading).

## Modal Overlays

Confirmation dialogs and pickers follow the same pattern:
1. Store as pointer field on `AppModel` (nil = inactive)
2. In `Update()`, check if active and route keys to the overlay
3. Overlay emits a result message (e.g., `ToolApproveMsg`, `PickerResult`)
4. Parent handles the result, sets overlay to nil
5. In `View()`, render overlay instead of input box when active

Ctrl+C and Esc are handled before overlays to ensure escape hatches always work.

## Status Bar Updates

Use `StatusUpdateMsg` to update the status bar. Fields with zero/empty values are not applied (preserves existing values). Exception: `Tokens >= 0` is applied (allows reset to zero).
