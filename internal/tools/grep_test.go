package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGrep_BasicMatch(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "test.go"), []byte("func hello() {\n\treturn\n}\n"), 0o644)

	tool := &GrepTool{}
	input, _ := json.Marshal(grepInput{Pattern: "hello", Path: dir})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "test.go:1:func hello()") {
		t.Errorf("expected match with file and line, got:\n%s", result)
	}
}

func TestGrep_RegexPattern(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte("foo123bar\nfoo456bar\nnomatch\n"), 0o644)

	tool := &GrepTool{}
	input, _ := json.Marshal(grepInput{Pattern: `foo\d+bar`, Path: dir})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(result), "\n")
	if len(lines) != 2 {
		t.Errorf("expected 2 matches, got %d: %v", len(lines), lines)
	}
}

func TestGrep_IncludeFilter(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "test.go"), []byte("match here\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte("match here\n"), 0o644)

	tool := &GrepTool{}
	input, _ := json.Marshal(grepInput{Pattern: "match", Path: dir, Include: "*.go"})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "test.go") {
		t.Error("expected test.go in results")
	}
	if strings.Contains(result, "test.txt") {
		t.Error("should not include test.txt")
	}
}

func TestGrep_SkipsGitDir(t *testing.T) {
	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	os.MkdirAll(gitDir, 0o755)
	os.WriteFile(filepath.Join(gitDir, "config"), []byte("match\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("match\n"), 0o644)

	tool := &GrepTool{}
	input, _ := json.Marshal(grepInput{Pattern: "match", Path: dir})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Contains(result, ".git") {
		t.Error("should not search in .git directory")
	}
	if !strings.Contains(result, "main.go") {
		t.Error("expected main.go in results")
	}
}

func TestGrep_NoMatches(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte("hello world\n"), 0o644)

	tool := &GrepTool{}
	input, _ := json.Marshal(grepInput{Pattern: "zzzzz", Path: dir})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "(no matches)" {
		t.Errorf("expected '(no matches)', got: %s", result)
	}
}

func TestGrep_InvalidRegex(t *testing.T) {
	tool := &GrepTool{}
	input, _ := json.Marshal(grepInput{Pattern: "[invalid", Path: "."})
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Error("expected error for invalid regex")
	}
}

func TestGrep_SingleFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("line1 match\nline2\nline3 match\n"), 0o644)

	tool := &GrepTool{}
	input, _ := json.Marshal(grepInput{Pattern: "match", Path: path})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(result), "\n")
	if len(lines) != 2 {
		t.Errorf("expected 2 matches, got %d", len(lines))
	}
}

func TestGrep_RequiresConfirmation(t *testing.T) {
	tool := &GrepTool{}
	if tool.RequiresConfirmation(nil) {
		t.Error("grep should not require confirmation")
	}
}
