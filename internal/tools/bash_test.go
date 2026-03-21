package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestBash_SimpleCommand(t *testing.T) {
	tool := &BashTool{}
	input, _ := json.Marshal(bashInput{Command: "echo hello"})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(result) != "hello" {
		t.Errorf("expected 'hello', got: %q", result)
	}
}

func TestBash_ExitCode(t *testing.T) {
	tool := &BashTool{}
	input, _ := json.Marshal(bashInput{Command: "exit 42"})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "exit code: 42") {
		t.Errorf("expected exit code 42, got: %s", result)
	}
}

func TestBash_Timeout(t *testing.T) {
	tool := &BashTool{}
	input, _ := json.Marshal(bashInput{Command: "sleep 10", Timeout: 100})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "timed out") {
		t.Errorf("expected timeout message, got: %s", result)
	}
}

func TestBash_EmptyCommand(t *testing.T) {
	tool := &BashTool{}
	input, _ := json.Marshal(bashInput{Command: ""})
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Error("expected error for empty command")
	}
}

func TestBash_CombinedOutput(t *testing.T) {
	tool := &BashTool{}
	input, _ := json.Marshal(bashInput{Command: "echo out && echo err >&2"})
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "out") || !strings.Contains(result, "err") {
		t.Errorf("expected both stdout and stderr, got: %s", result)
	}
}

func TestBash_RequiresConfirmation(t *testing.T) {
	tool := &BashTool{}
	if !tool.RequiresConfirmation(nil) {
		t.Error("bash should require confirmation")
	}
}
