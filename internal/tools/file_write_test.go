package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteFile_NewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "new.txt")

	tool := &WriteFileTool{}
	input, _ := json.Marshal(writeFileInput{FilePath: path, Content: "hello world"})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "11 bytes") {
		t.Errorf("expected byte count in result, got: %s", result)
	}

	data, _ := os.ReadFile(path)
	if string(data) != "hello world" {
		t.Errorf("expected 'hello world', got: %s", string(data))
	}
}

func TestWriteFile_Overwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "existing.txt")
	os.WriteFile(path, []byte("old content"), 0o644)

	tool := &WriteFileTool{}
	input, _ := json.Marshal(writeFileInput{FilePath: path, Content: "new content"})
	_, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(path)
	if string(data) != "new content" {
		t.Errorf("expected 'new content', got: %s", string(data))
	}
}

func TestWriteFile_CreateParentDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a", "b", "c", "deep.txt")

	tool := &WriteFileTool{}
	input, _ := json.Marshal(writeFileInput{FilePath: path, Content: "deep"})
	_, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(path)
	if string(data) != "deep" {
		t.Errorf("expected 'deep', got: %s", string(data))
	}
}

func TestWriteFile_RequiresConfirmation(t *testing.T) {
	tool := &WriteFileTool{}
	if !tool.RequiresConfirmation(nil) {
		t.Error("write_file should require confirmation")
	}
}
