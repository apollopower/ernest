package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

const maxGlobResults = 1000

type GlobTool struct{}

type globInput struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path"`
}

func (t *GlobTool) Name() string        { return "glob" }
func (t *GlobTool) Description() string { return "Find files matching a glob pattern" }

func (t *GlobTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{
				"type":        "string",
				"description": "Glob pattern (e.g. '**/*.go', 'src/**/*.ts')",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "Directory to search in (default: current working directory)",
			},
		},
		"required": []string{"pattern"},
	}
}

func (t *GlobTool) RequiresConfirmation(_ json.RawMessage) bool { return false }

func (t *GlobTool) Execute(_ context.Context, input json.RawMessage) (string, error) {
	var params globInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	if params.Pattern == "" {
		return "", fmt.Errorf("pattern is required")
	}

	root := params.Path
	if root == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("cannot determine working directory: %w", err)
		}
	}

	info, err := os.Stat(root)
	if err != nil {
		return "", fmt.Errorf("cannot access path %q: %w", root, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("path %q is not a directory", root)
	}

	fsys := os.DirFS(root)
	matches, err := doublestar.Glob(fsys, params.Pattern)
	if err != nil {
		return "", fmt.Errorf("invalid glob pattern: %w", err)
	}

	// Filter out .git and node_modules
	var filtered []string
	for _, m := range matches {
		if shouldSkipPath(m) {
			continue
		}
		filtered = append(filtered, filepath.Join(root, m))
	}

	if len(filtered) == 0 {
		return "(no matches)", nil
	}

	truncated := false
	if len(filtered) > maxGlobResults {
		filtered = filtered[:maxGlobResults]
		truncated = true
	}

	result := strings.Join(filtered, "\n")
	if truncated {
		result += fmt.Sprintf("\n... (truncated at %d results)", maxGlobResults)
	}

	return result, nil
}
