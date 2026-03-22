package headless

import (
	"encoding/json"
	"fmt"
	"io"
)

// OutputFormat determines how events are written to stdout.
type OutputFormat string

const (
	FormatText OutputFormat = "text"
	FormatJSON OutputFormat = "json"
)

// OutputEvent is a single JSON line in JSON output mode.
type OutputEvent struct {
	Version int     `json:"version,omitempty"` // format version (1), included in "session" event
	Type    string  `json:"type"`              // "session", "text", "tool_call", "tool_result", "compacted", "error", "done"
	Content string  `json:"content,omitempty"` // text content
	Tool    string  `json:"tool,omitempty"`    // tool name
	Input   any     `json:"input,omitempty"`   // tool input (for tool_call)
	Output  string  `json:"output,omitempty"`  // tool output (for tool_result)
	ID      string  `json:"id,omitempty"`      // session ID (for session event)
	Project string  `json:"project,omitempty"` // project dir (for session event)
	Tokens  *Tokens `json:"tokens,omitempty"`  // token usage (for done event)
	Error   string  `json:"error,omitempty"`   // error message (for error event)
}

// Tokens represents token usage for a turn.
type Tokens struct {
	Input  int `json:"input"`
	Output int `json:"output"`
}

// writeJSON writes a single OutputEvent as a JSON line.
func writeJSON(w io.Writer, event OutputEvent) {
	data, err := json.Marshal(event)
	if err != nil {
		// Fallback: emit a minimal error event
		data = []byte(fmt.Sprintf(`{"type":"error","error":"marshal failed: %s"}`, err.Error()))
	}
	fmt.Fprintf(w, "%s\n", data)
}
