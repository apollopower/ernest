package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	defaultBashTimeout = 120 * time.Second
	maxOutputBytes     = 100 * 1024 // 100KB
)

type BashTool struct{}

type bashInput struct {
	Command string `json:"command"`
	Timeout int    `json:"timeout"` // milliseconds
}

func (t *BashTool) Name() string        { return "bash" }
func (t *BashTool) Description() string { return "Execute a shell command" }

func (t *BashTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "The shell command to execute",
			},
			"timeout": map[string]any{
				"type":        "integer",
				"description": "Timeout in milliseconds (default: 120000)",
			},
		},
		"required": []string{"command"},
	}
}

func (t *BashTool) RequiresConfirmation(_ json.RawMessage) bool { return true }

func (t *BashTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params bashInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	if params.Command == "" {
		return "", fmt.Errorf("command is required")
	}

	timeout := defaultBashTimeout
	if params.Timeout > 0 {
		timeout = time.Duration(params.Timeout) * time.Millisecond
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", params.Command)
	output, err := cmd.CombinedOutput()

	// Truncate output if too large, avoiding splitting multi-byte UTF-8 characters
	truncated := len(output) > maxOutputBytes
	if truncated {
		output = output[:maxOutputBytes]
		for len(output) > 0 && !utf8.RuneStart(output[len(output)-1]) {
			output = output[:len(output)-1]
		}
	}
	outputStr := string(output)
	if truncated {
		outputStr += "\n... (output truncated at 100KB)"
	}

	exitCode := 0
	if err != nil {
		// Check context first — timeout kills the process, which also
		// produces an ExitError with code -1.
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Sprintf("%s\n\n(command timed out after %s)", outputStr, timeout), nil
		} else if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return "", fmt.Errorf("failed to execute command: %w", err)
		}
	}

	var result strings.Builder
	if outputStr != "" {
		result.WriteString(outputStr)
	}
	if exitCode != 0 {
		if result.Len() > 0 {
			result.WriteString("\n")
		}
		result.WriteString(fmt.Sprintf("(exit code: %d)", exitCode))
	}

	if result.Len() == 0 {
		return "(no output)", nil
	}

	return result.String(), nil
}
