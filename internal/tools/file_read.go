package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const defaultReadLimit = 2000

type ReadFileTool struct{}

type readFileInput struct {
	FilePath string `json:"file_path"`
	Offset   int    `json:"offset"`
	Limit    int    `json:"limit"`
}

func (t *ReadFileTool) Name() string        { return "read_file" }
func (t *ReadFileTool) Description() string { return "Read file contents with line numbers" }

func (t *ReadFileTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file_path": map[string]any{
				"type":        "string",
				"description": "Path to the file to read",
			},
			"offset": map[string]any{
				"type":        "integer",
				"description": "Line number to start reading from (1-based)",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Maximum number of lines to read",
			},
		},
		"required": []string{"file_path"},
	}
}

func (t *ReadFileTool) RequiresConfirmation(_ json.RawMessage) bool { return false }

func (t *ReadFileTool) Execute(_ context.Context, input json.RawMessage) (string, error) {
	var params readFileInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	if params.FilePath == "" {
		return "", fmt.Errorf("file_path is required")
	}

	info, err := os.Stat(params.FilePath)
	if err != nil {
		return "", fmt.Errorf("cannot access %s: %w", params.FilePath, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory, not a file", params.FilePath)
	}

	f, err := os.Open(params.FilePath)
	if err != nil {
		return "", fmt.Errorf("cannot open %s: %w", params.FilePath, err)
	}
	defer f.Close()

	offset := params.Offset
	if offset < 1 {
		offset = 1
	}

	limit := params.Limit
	if limit <= 0 {
		limit = defaultReadLimit
	}

	var lines []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024) // 1MB max line
	lineNum := 0
	collected := 0

	for scanner.Scan() {
		lineNum++
		if lineNum < offset {
			continue
		}
		if collected >= limit {
			lines = append(lines, fmt.Sprintf("... (truncated at %d lines)", limit))
			break
		}
		lines = append(lines, fmt.Sprintf("%6d\t%s", lineNum, scanner.Text()))
		collected++
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("error reading %s: %w", params.FilePath, err)
	}

	if len(lines) == 0 {
		if info.Size() == 0 {
			return "(empty file)", nil
		}
		return fmt.Sprintf("(no lines in range: offset %d, limit %d, file has %d lines)", offset, limit, lineNum), nil
	}

	return strings.Join(lines, "\n"), nil
}
