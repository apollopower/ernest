package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type WriteFileTool struct{}

type writeFileInput struct {
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
}

func (t *WriteFileTool) Name() string        { return "write_file" }
func (t *WriteFileTool) Description() string { return "Write content to a file, creating parent directories as needed" }

func (t *WriteFileTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file_path": map[string]any{
				"type":        "string",
				"description": "Absolute path to write to",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "Content to write",
			},
		},
		"required": []string{"file_path", "content"},
	}
}

func (t *WriteFileTool) RequiresConfirmation(_ json.RawMessage) bool { return true }

func (t *WriteFileTool) Execute(_ context.Context, input json.RawMessage) (string, error) {
	var params writeFileInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	if params.FilePath == "" {
		return "", fmt.Errorf("file_path is required")
	}

	dir := filepath.Dir(params.FilePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("cannot create directory %s: %w", dir, err)
	}

	if err := os.WriteFile(params.FilePath, []byte(params.Content), 0o644); err != nil {
		return "", fmt.Errorf("cannot write %s: %w", params.FilePath, err)
	}

	return fmt.Sprintf("Wrote %d bytes to %s", len(params.Content), params.FilePath), nil
}
