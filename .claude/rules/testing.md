# Testing Conventions

## Mock Providers

Use `mockProvider` (single response) or `multiTurnProvider` (different responses per call) for testing the agent loop:

```go
type mockProvider struct {
    name   string
    events []provider.StreamEvent
}

type multiTurnProvider struct {
    name  string
    turns [][]provider.StreamEvent
    call  int
}
```

## Filesystem Tests

- Always use `t.TempDir()` for filesystem operations
- Use `t.Setenv()` (not `os.Setenv`) for environment variables — automatically restored
- For cross-platform session/config tests, set all three: `XDG_CONFIG_HOME`, `APPDATA`, `HOME`

## Race Detector

All tests run with `-race`. Design for this:
- No shared mutable state between test goroutines
- Mock call counters may need `atomic` if accessed concurrently (though most tests drain channels before reading)

## What to Test

- **Provider**: SSE parsing with realistic payloads, message format conversion, error responses
- **Agent**: tool call detection/execution, multi-turn loops, confirmation flow, compaction
- **Tools**: happy path + error cases, filesystem operations in temp dirs
- **Config**: round-trip save/load, credential resolution order, case sensitivity
- **Session**: save/load with all ContentBlock types, ToolInput serialization

## Test Structure

Tests are in the same package (not `_test` package) for access to unexported types. This is intentional — agent tests need access to `history`, mock tools need to implement the `Tool` interface directly.
