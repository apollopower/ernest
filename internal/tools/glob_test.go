package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGlob_BasicPattern(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.go"), []byte(""), 0o644)
	os.WriteFile(filepath.Join(dir, "b.go"), []byte(""), 0o644)
	os.WriteFile(filepath.Join(dir, "c.txt"), []byte(""), 0o644)

	tool := &GlobTool{}
	input, _ := json.Marshal(globInput{Pattern: "*.go", Path: dir})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "a.go") || !strings.Contains(result, "b.go") {
		t.Errorf("expected a.go and b.go in results, got:\n%s", result)
	}
	if strings.Contains(result, "c.txt") {
		t.Error("should not include c.txt")
	}
}

func TestGlob_DoublestarPattern(t *testing.T) {
	dir := t.TempDir()
	subdir := filepath.Join(dir, "sub")
	os.MkdirAll(subdir, 0o755)
	os.WriteFile(filepath.Join(dir, "root.go"), []byte(""), 0o644)
	os.WriteFile(filepath.Join(subdir, "deep.go"), []byte(""), 0o644)

	tool := &GlobTool{}
	input, _ := json.Marshal(globInput{Pattern: "**/*.go", Path: dir})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "root.go") || !strings.Contains(result, "deep.go") {
		t.Errorf("expected both files, got:\n%s", result)
	}
}

func TestGlob_SkipsGitDir(t *testing.T) {
	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	os.MkdirAll(gitDir, 0o755)
	os.WriteFile(filepath.Join(gitDir, "config"), []byte(""), 0o644)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte(""), 0o644)

	tool := &GlobTool{}
	input, _ := json.Marshal(globInput{Pattern: "**/*", Path: dir})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Contains(result, ".git") {
		t.Error("should not include .git directory contents")
	}
}

func TestGlob_NoMatches(t *testing.T) {
	dir := t.TempDir()

	tool := &GlobTool{}
	input, _ := json.Marshal(globInput{Pattern: "*.xyz", Path: dir})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "(no matches)" {
		t.Errorf("expected '(no matches)', got: %s", result)
	}
}

func TestGlob_RequiresConfirmation(t *testing.T) {
	tool := &GlobTool{}
	if tool.RequiresConfirmation(nil) {
		t.Error("glob should not require confirmation")
	}
}
