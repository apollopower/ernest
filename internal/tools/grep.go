package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

const maxGrepMatches = 500

type GrepTool struct{}

type grepInput struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path"`
	Include string `json:"include"`
}

func (t *GrepTool) Name() string        { return "grep" }
func (t *GrepTool) Description() string { return "Search file contents with a regex pattern" }

func (t *GrepTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{
				"type":        "string",
				"description": "Regex pattern to search for",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "File or directory to search in (default: current working directory)",
			},
			"include": map[string]any{
				"type":        "string",
				"description": "Glob pattern to filter files (e.g. '*.go')",
			},
		},
		"required": []string{"pattern"},
	}
}

func (t *GrepTool) RequiresConfirmation(_ json.RawMessage) bool { return false }

func (t *GrepTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params grepInput
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	if params.Pattern == "" {
		return "", fmt.Errorf("pattern is required")
	}

	re, err := regexp.Compile(params.Pattern)
	if err != nil {
		return "", fmt.Errorf("invalid regex pattern: %w", err)
	}

	root := params.Path
	if root == "" {
		root, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("cannot determine working directory: %w", err)
		}
	}

	info, err := os.Stat(root)
	if err != nil {
		return "", fmt.Errorf("cannot access %s: %w", root, err)
	}

	var matches []string

	if !info.IsDir() {
		// Single file search
		matches, err = grepFile(ctx, root, re)
		if err != nil {
			return "", err
		}
	} else {
		// Directory walk
		walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil // skip errors
			}

			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			if d.IsDir() {
				if shouldSkipDir(d.Name()) {
					return filepath.SkipDir
				}
				return nil
			}

			// Apply include filter
			if params.Include != "" {
				matched, _ := filepath.Match(params.Include, d.Name())
				if !matched {
					return nil
				}
			}

			if len(matches) >= maxGrepMatches {
				return filepath.SkipAll
			}

			fileMatches, _ := grepFile(ctx, path, re)
			matches = append(matches, fileMatches...)
			return nil
		})
		if walkErr != nil && walkErr != filepath.SkipAll {
			return "", fmt.Errorf("error searching %s: %w", root, walkErr)
		}
	}

	if len(matches) == 0 {
		return "(no matches)", nil
	}

	truncated := false
	if len(matches) > maxGrepMatches {
		matches = matches[:maxGrepMatches]
		truncated = true
	}

	result := strings.Join(matches, "\n")
	if truncated {
		result += fmt.Sprintf("\n... (truncated at %d matches)", maxGrepMatches)
	}

	return result, nil
}

func grepFile(ctx context.Context, path string, re *regexp.Regexp) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var matches []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024) // 1MB max line
	lineNum := 0

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return matches, ctx.Err()
		default:
		}

		lineNum++
		line := scanner.Text()

		// Skip binary files (check first line for non-UTF8)
		if lineNum == 1 && !utf8.ValidString(line) {
			return nil, nil
		}

		if re.MatchString(line) {
			matches = append(matches, fmt.Sprintf("%s:%d:%s", path, lineNum, line))
		}
	}

	return matches, scanner.Err()
}
