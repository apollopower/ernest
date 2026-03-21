package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type StrReplaceTool struct{}

type strReplaceInput struct {
	FilePath   string `json:"file_path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all"`
}

func (t *StrReplaceTool) Name() string        { return "str_replace" }
func (t *StrReplaceTool) Description() string { return "Replace a string in a file" }

func (t *StrReplaceTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file_path": map[string]any{
				"type":        "string",
				"description": "Path to the file",
			},
			"old_string": map[string]any{
				"type":        "string",
				"description": "Exact string to find and replace",
			},
			"new_string": map[string]any{
				"type":        "string",
				"description": "Replacement string (empty string to delete the match)",
			},
			"replace_all": map[string]any{
				"type":        "boolean",
				"description": "Replace all occurrences (default: false)",
			},
		},
		"required": []string{"file_path", "old_string", "new_string"},
	}
}

func (t *StrReplaceTool) RequiresConfirmation(_ json.RawMessage) bool { return true }

func (t *StrReplaceTool) Execute(_ context.Context, input json.RawMessage) (string, error) {
	var params strReplaceInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	if params.FilePath == "" {
		return "", fmt.Errorf("file_path is required")
	}
	if params.OldString == "" {
		return "", fmt.Errorf("old_string is required")
	}

	info, err := os.Stat(params.FilePath)
	if err != nil {
		return "", fmt.Errorf("cannot access %s: %w", params.FilePath, err)
	}

	data, err := os.ReadFile(params.FilePath)
	if err != nil {
		return "", fmt.Errorf("cannot read %s: %w", params.FilePath, err)
	}

	content := string(data)
	count := strings.Count(content, params.OldString)

	if count == 0 {
		return "", fmt.Errorf("old_string not found in %s", params.FilePath)
	}

	if !params.ReplaceAll && count > 1 {
		return "", fmt.Errorf("old_string is not unique in %s, found %d occurrences. Use replace_all or provide more context.", params.FilePath, count)
	}

	var newContent string
	if params.ReplaceAll {
		newContent = strings.ReplaceAll(content, params.OldString, params.NewString)
	} else {
		newContent = strings.Replace(content, params.OldString, params.NewString, 1)
	}

	if err := os.WriteFile(params.FilePath, []byte(newContent), info.Mode()); err != nil {
		return "", fmt.Errorf("cannot write %s: %w", params.FilePath, err)
	}

	if params.ReplaceAll {
		return fmt.Sprintf("Replaced %d occurrences in %s", count, params.FilePath), nil
	}
	return fmt.Sprintf("Replaced 1 occurrence in %s", params.FilePath), nil
}
