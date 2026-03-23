# Adding a New Provider

## Implementation Pattern

1. Create `internal/provider/<name>.go` implementing the `Provider` interface
2. Implement `Name() string`, `Healthy() bool`, `Stream()` method
3. Add SSE parser goroutine that emits `StreamEvent` values on a channel

## StreamEvent Types

The provider must emit these event types for the agent loop to work:

| Event | When | Required Fields |
|-------|------|-----------------|
| `text_delta` | Text content streaming | `Text` |
| `tool_use_start` | Tool call begins | `ToolUseID`, `ToolName` |
| `tool_input_delta` | Tool input JSON fragment | `ToolInput` |
| `content_block_stop` | Content block finalized | (none) |
| `message_delta` | Message metadata | `StopReason`, `Usage` |
| `done` | Stream complete | (none) |
| `error` | Error occurred | `Error` |

## SSE Parsing

- Use `bufio.Scanner` with 1MB buffer (`scanner.Buffer(make([]byte, 0, 1<<20), 1<<20)`)
- Check `ctx.Done()` between reads for cancellation
- Flush pending state on EOF (stream may end without trailing blank line)
- Limit error response body reads to 4KB (`io.LimitReader`)

## Message Conversion

Each provider converts between Ernest's `provider.Message` format and its own API format:
- System prompt → provider-specific system message format
- `ContentBlock{Type: "tool_use"}` → provider's tool call format
- `ContentBlock{Type: "tool_result"}` → provider's tool result format

## Tool Call Accumulation

OpenAI-compatible providers use index-based multiplexing for parallel tool calls:
- Maintain `map[int]*pendingToolCall` to accumulate fragments by index
- Emit tool calls in sorted index order for determinism
- Emit `message_delta` with `StopReason: "tool_use"` after flushing all pending calls

## Provider Factory

In `cmd/ernest/main.go`, add the new provider to the factory switch:
- `"anthropic"` → `NewAnthropic()`
- Everything else → `NewOpenAICompat()` (covers most providers via configurable base URL)
- Only add a dedicated implementation if the API format is fundamentally different (e.g., Gemini)
