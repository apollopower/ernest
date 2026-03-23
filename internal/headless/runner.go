package headless

import (
	"bufio"
	"context"
	"encoding/json"
	"ernest/internal/agent"
	"ernest/internal/session"
	"fmt"
	"io"
	"os"
)

// Runner executes prompts in headless mode (no TUI).
type Runner struct {
	agent   *agent.Agent
	session *session.Session
	format  OutputFormat
	out     io.Writer
}

// NewRunner creates a headless runner.
func NewRunner(a *agent.Agent, sess *session.Session, format OutputFormat, out io.Writer) *Runner {
	return &Runner{
		agent:   a,
		session: sess,
		format:  format,
		out:     out,
	}
}

// RunPrompt executes a single prompt and writes output.
func (r *Runner) RunPrompt(ctx context.Context, prompt string) error {
	// Emit session event in JSON mode
	if r.format == FormatJSON {
		writeJSON(r.out, OutputEvent{
			Version: 1,
			Type:    "session",
			ID:      r.session.ID,
			Project: r.session.ProjectDir,
		})
	}

	events := r.agent.Run(ctx, prompt)
	return r.consumeEvents(ctx, events)
}

// RunConversation reads prompts from stdin line by line and responds to each.
func (r *Runner) RunConversation(ctx context.Context, in io.Reader) error {
	// Emit session event in JSON mode
	if r.format == FormatJSON {
		writeJSON(r.out, OutputEvent{
			Version: 1,
			Type:    "session",
			ID:      r.session.ID,
			Project: r.session.ProjectDir,
		})
	}

	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024) // 1MB max line

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line := scanner.Text()
		if line == "" {
			continue // skip empty lines
		}

		events := r.agent.Run(ctx, line)
		if err := r.consumeEvents(ctx, events); err != nil {
			return err
		}

		// Check compaction after each turn
		if r.agent.NeedsCompaction() {
			before, after, err := r.agent.Compact(ctx)
			if err != nil {
				fmt.Fprintf(os.Stderr, "compaction failed: %v\n", err)
			} else if before != after {
				if r.format == FormatJSON {
					writeJSON(r.out, OutputEvent{
						Type:    "compacted",
						Content: fmt.Sprintf("%d → %d tokens", before, after),
					})
				}
			}
		}
	}

	return scanner.Err()
}

// consumeEvents reads all events from the agent and writes output.
func (r *Runner) consumeEvents(ctx context.Context, events <-chan agent.AgentEvent) error {
	var lastError error
	var lastUsage *agent.AgentEvent // track last usage event for done

	for evt := range events {
		switch evt.Type {
		case "text":
			if r.format == FormatText {
				fmt.Fprint(r.out, evt.Text)
			} else {
				writeJSON(r.out, OutputEvent{Type: "text", Content: evt.Text})
			}

		case "tool_call":
			if r.format == FormatJSON {
				var input any
				if err := json.Unmarshal([]byte(evt.ToolInput), &input); err != nil {
					input = evt.ToolInput // fall back to raw string
				}
				writeJSON(r.out, OutputEvent{
					Type:  "tool_call",
					Tool:  evt.ToolName,
					Input: input,
				})
			}

		case "tool_result":
			if r.format == FormatJSON {
				writeJSON(r.out, OutputEvent{
					Type:   "tool_result",
					Tool:   evt.ToolName,
					Output: evt.ToolResult,
				})
			}

		case "tool_confirm":
			// In headless mode without --auto-approve, auto-deny
			r.agent.ResolveTool(evt.ToolUseID, false)
			denyErr := fmt.Errorf("tool %s denied (headless mode, use --auto-approve)", evt.ToolName)
			lastError = denyErr
			if r.format == FormatJSON {
				writeJSON(r.out, OutputEvent{Type: "error", Error: denyErr.Error()})
			} else {
				fmt.Fprintln(os.Stderr, denyErr.Error())
			}

		case "usage":
			if evt.Usage != nil {
				evtCopy := evt
				lastUsage = &evtCopy
			}

		case "error":
			if r.format == FormatJSON {
				errMsg := "unknown error"
				if evt.Error != nil {
					errMsg = evt.Error.Error()
				}
				writeJSON(r.out, OutputEvent{Type: "error", Error: errMsg})
			} else {
				if evt.Error != nil {
					fmt.Fprintf(os.Stderr, "error: %v\n", evt.Error)
				}
			}
			lastError = evt.Error

		case "done":
			if r.format == FormatText {
				fmt.Fprintln(r.out) // newline after response
			} else {
				doneEvt := OutputEvent{Type: "done"}
				if lastUsage != nil && lastUsage.Usage != nil {
					doneEvt.Tokens = &Tokens{
						Input:  lastUsage.Usage.InputTokens,
						Output: lastUsage.Usage.OutputTokens,
					}
				}
				// Omit tokens entirely when no usage data available
				// to avoid misleading zero values
				writeJSON(r.out, doneEvt)
				lastUsage = nil // reset for next turn
			}

		case "provider_switch":
			// Silent in headless mode
		}
	}

	return lastError
}

// SaveSession saves the current session to disk — only if there are messages.
func (r *Runner) SaveSession() {
	history := r.agent.History()
	if len(history) == 0 {
		return // don't save empty sessions
	}
	r.session.SetMessages(history)
	if err := r.session.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to save session: %v\n", err)
	}
}
