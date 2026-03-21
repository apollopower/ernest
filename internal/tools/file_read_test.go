package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFile_Basic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("line1\nline2\nline3\n"), 0o644)

	tool := &ReadFileTool{}
	input, _ := json.Marshal(readFileInput{FilePath: path})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "line1") || !strings.Contains(result, "line3") {
		t.Errorf("expected all lines in output, got:\n%s", result)
	}
	// Check line numbers present
	if !strings.Contains(result, "1\t") {
		t.Error("expected line numbers in output")
	}
}

func TestReadFile_OffsetAndLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	var content string
	for i := 1; i <= 10; i++ {
		content += fmt.Sprintf("line%d\n", i)
	}
	os.WriteFile(path, []byte(content), 0o644)

	tool := &ReadFileTool{}
	input, _ := json.Marshal(readFileInput{FilePath: path, Offset: 3, Limit: 2})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(result), "\n")
	// 2 content lines + 1 truncation notice
	if len(lines) != 3 {
		t.Errorf("expected 3 lines (2 content + truncation notice), got %d: %v", len(lines), lines)
	}
	if !strings.Contains(lines[2], "truncated") {
		t.Errorf("expected truncation notice, got: %s", lines[2])
	}
	if !strings.Contains(lines[0], "3\t") {
		t.Errorf("expected first line to be line 3, got: %s", lines[0])
	}
}

func TestReadFile_Nonexistent(t *testing.T) {
	tool := &ReadFileTool{}
	input, _ := json.Marshal(readFileInput{FilePath: "/nonexistent/file.txt"})
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestReadFile_Directory(t *testing.T) {
	dir := t.TempDir()
	tool := &ReadFileTool{}
	input, _ := json.Marshal(readFileInput{FilePath: dir})
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Error("expected error for directory")
	}
	if !strings.Contains(err.Error(), "directory") {
		t.Errorf("expected directory error, got: %v", err)
	}
}

func TestReadFile_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.txt")
	os.WriteFile(path, []byte(""), 0o644)

	tool := &ReadFileTool{}
	input, _ := json.Marshal(readFileInput{FilePath: path})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "(empty file)" {
		t.Errorf("expected '(empty file)', got: %s", result)
	}
}

func TestReadFile_RequiresConfirmation(t *testing.T) {
	tool := &ReadFileTool{}
	if tool.RequiresConfirmation(nil) {
		t.Error("read_file should not require confirmation")
	}
}
