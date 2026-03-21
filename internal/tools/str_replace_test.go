package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStrReplace_SingleOccurrence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.go")
	os.WriteFile(path, []byte("func hello() {\n\treturn\n}\n"), 0o644)

	tool := &StrReplaceTool{}
	input, _ := json.Marshal(strReplaceInput{
		FilePath:  path,
		OldString: "hello",
		NewString: "world",
	})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "1 occurrence") {
		t.Errorf("expected '1 occurrence' in result, got: %s", result)
	}

	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "world") {
		t.Error("expected 'world' in file content")
	}
}

func TestStrReplace_ReplaceAll(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("aaa bbb aaa ccc aaa"), 0o644)

	tool := &StrReplaceTool{}
	input, _ := json.Marshal(strReplaceInput{
		FilePath:   path,
		OldString:  "aaa",
		NewString:  "xxx",
		ReplaceAll: true,
	})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "3 occurrences") {
		t.Errorf("expected '3 occurrences' in result, got: %s", result)
	}

	data, _ := os.ReadFile(path)
	if string(data) != "xxx bbb xxx ccc xxx" {
		t.Errorf("expected all replaced, got: %s", string(data))
	}
}

func TestStrReplace_NotUnique(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("aaa bbb aaa"), 0o644)

	tool := &StrReplaceTool{}
	input, _ := json.Marshal(strReplaceInput{
		FilePath:  path,
		OldString: "aaa",
		NewString: "xxx",
	})
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for non-unique string")
	}
	if !strings.Contains(err.Error(), "not unique") {
		t.Errorf("expected 'not unique' error, got: %v", err)
	}
}

func TestStrReplace_NotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("hello world"), 0o644)

	tool := &StrReplaceTool{}
	input, _ := json.Marshal(strReplaceInput{
		FilePath:  path,
		OldString: "missing",
		NewString: "xxx",
	})
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for not found")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got: %v", err)
	}
}

func TestStrReplace_DeleteText(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("hello world"), 0o644)

	tool := &StrReplaceTool{}
	input, _ := json.Marshal(strReplaceInput{
		FilePath:  path,
		OldString: " world",
		NewString: "",
	})
	_, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(path)
	if string(data) != "hello" {
		t.Errorf("expected 'hello', got: %s", string(data))
	}
}

func TestStrReplace_RequiresConfirmation(t *testing.T) {
	tool := &StrReplaceTool{}
	if !tool.RequiresConfirmation(nil) {
		t.Error("str_replace should require confirmation")
	}
}
