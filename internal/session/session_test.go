package session

import (
	"ernest/internal/provider"
	"os"
	"path/filepath"
	"testing"
)

func TestNew(t *testing.T) {
	s := New("/tmp/project")
	if s.Version != 1 {
		t.Errorf("expected version 1, got %d", s.Version)
	}
	if len(s.ID) != 8 {
		t.Errorf("expected 8-char ID, got %q", s.ID)
	}
	if s.ProjectDir != "/tmp/project" {
		t.Errorf("expected project dir '/tmp/project', got %q", s.ProjectDir)
	}
}

func TestSaveAndLoad(t *testing.T) {
	// Override session dir for testing
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	s := New("/tmp/project")
	s.SetMessages([]provider.Message{
		{
			Role: provider.RoleUser,
			Content: []provider.ContentBlock{
				{Type: "text", Text: "Hello there"},
			},
		},
		{
			Role: provider.RoleAssistant,
			Content: []provider.ContentBlock{
				{Type: "text", Text: "Hi!"},
			},
		},
	})

	if err := s.Save(); err != nil {
		t.Fatalf("failed to save: %v", err)
	}

	// Verify file exists
	path := filepath.Join(SessionDir(), s.ID+".json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("session file not found: %v", err)
	}

	// Load and verify
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("failed to load: %v", err)
	}

	if loaded.ID != s.ID {
		t.Errorf("expected ID %q, got %q", s.ID, loaded.ID)
	}
	if loaded.Version != 1 {
		t.Errorf("expected version 1, got %d", loaded.Version)
	}
	if len(loaded.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(loaded.Messages))
	}
	if loaded.Messages[0].Content[0].Text != "Hello there" {
		t.Error("first message text mismatch")
	}
	if loaded.Summary != "Hello there" {
		t.Errorf("expected summary 'Hello there', got %q", loaded.Summary)
	}
}

func TestSaveAndLoad_AllContentBlockTypes(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	s := New("/tmp/project")
	s.SetMessages([]provider.Message{
		{
			Role: provider.RoleUser,
			Content: []provider.ContentBlock{
				{Type: "text", Text: "Read my file"},
			},
		},
		{
			Role: provider.RoleAssistant,
			Content: []provider.ContentBlock{
				{Type: "text", Text: "Let me read that."},
				{Type: "tool_use", ToolUseID: "call_1", ToolName: "read_file", ToolInput: map[string]any{"file_path": "/tmp/test.txt"}},
			},
		},
		{
			Role: provider.RoleUser,
			Content: []provider.ContentBlock{
				{Type: "tool_result", ToolUseID: "call_1", Content: "file contents here", IsError: false},
			},
		},
		{
			Role: provider.RoleAssistant,
			Content: []provider.ContentBlock{
				{Type: "text", Text: "The file contains: file contents here"},
			},
		},
	})

	if err := s.Save(); err != nil {
		t.Fatalf("failed to save: %v", err)
	}

	path := filepath.Join(SessionDir(), s.ID+".json")
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("failed to load: %v", err)
	}

	if len(loaded.Messages) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(loaded.Messages))
	}

	// Verify tool_use block round-trips
	assistantMsg := loaded.Messages[1]
	if len(assistantMsg.Content) != 2 {
		t.Fatalf("expected 2 content blocks in assistant msg, got %d", len(assistantMsg.Content))
	}
	toolBlock := assistantMsg.Content[1]
	if toolBlock.Type != "tool_use" {
		t.Errorf("expected tool_use, got %q", toolBlock.Type)
	}
	if toolBlock.ToolName != "read_file" {
		t.Errorf("expected tool name 'read_file', got %q", toolBlock.ToolName)
	}
	// ToolInput round-trips as map[string]any
	inputMap, ok := toolBlock.ToolInput.(map[string]any)
	if !ok {
		t.Fatalf("expected ToolInput to be map[string]any, got %T", toolBlock.ToolInput)
	}
	if inputMap["file_path"] != "/tmp/test.txt" {
		t.Errorf("expected file_path '/tmp/test.txt', got %v", inputMap["file_path"])
	}

	// Verify tool_result block
	resultMsg := loaded.Messages[2]
	if resultMsg.Content[0].Type != "tool_result" {
		t.Errorf("expected tool_result, got %q", resultMsg.Content[0].Type)
	}
	if resultMsg.Content[0].Content != "file contents here" {
		t.Errorf("expected tool result content, got %q", resultMsg.Content[0].Content)
	}
}

func TestListSessions(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	// Create two sessions
	s1 := New("/project1")
	s1.SetMessages([]provider.Message{
		{Role: provider.RoleUser, Content: []provider.ContentBlock{{Type: "text", Text: "First session"}}},
	})
	if err := s1.Save(); err != nil {
		t.Fatalf("failed to save s1: %v", err)
	}

	s2 := New("/project2")
	s2.SetMessages([]provider.Message{
		{Role: provider.RoleUser, Content: []provider.ContentBlock{{Type: "text", Text: "Second session"}}},
	})
	if err := s2.Save(); err != nil {
		t.Fatalf("failed to save s2: %v", err)
	}

	sessions, err := ListSessions()
	if err != nil {
		t.Fatalf("failed to list: %v", err)
	}

	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}

	// Should be sorted by UpdatedAt desc (s2 is more recent)
	if sessions[0].ID != s2.ID {
		t.Errorf("expected most recent session first")
	}
}

func TestListSessions_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	sessions, err := ListSessions()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(sessions))
	}
}

func TestListSessions_SkipsCorruptFiles(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	sessDir := SessionDir()
	os.MkdirAll(sessDir, 0o755)

	// Write a valid session
	s := New("/project")
	s.Save()

	// Write a corrupt file
	os.WriteFile(filepath.Join(sessDir, "corrupt.json"), []byte("{invalid"), 0o644)

	sessions, err := ListSessions()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sessions) != 1 {
		t.Errorf("expected 1 valid session (skip corrupt), got %d", len(sessions))
	}
}

func TestSetMessages_Summary(t *testing.T) {
	s := New("/project")

	s.SetMessages([]provider.Message{
		{Role: provider.RoleUser, Content: []provider.ContentBlock{{Type: "text", Text: "What is Go?"}}},
	})

	if s.Summary != "What is Go?" {
		t.Errorf("expected summary 'What is Go?', got %q", s.Summary)
	}
}

func TestSetMessages_SummaryTruncation(t *testing.T) {
	s := New("/project")

	longText := ""
	for i := 0; i < 200; i++ {
		longText += "a"
	}

	s.SetMessages([]provider.Message{
		{Role: provider.RoleUser, Content: []provider.ContentBlock{{Type: "text", Text: longText}}},
	})

	if len(s.Summary) > 104 { // 100 + "..."
		t.Errorf("expected summary truncated, got length %d", len(s.Summary))
	}
}

func TestToolInputRoundTrip_StringFallback(t *testing.T) {
	// Test that a string ToolInput (from consumeStream fallback) survives JSON round-trip
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	s := New("/project")
	s.SetMessages([]provider.Message{
		{
			Role: provider.RoleAssistant,
			Content: []provider.ContentBlock{
				{Type: "tool_use", ToolUseID: "c1", ToolName: "test", ToolInput: `{"raw": "json"}`},
			},
		},
	})

	if err := s.Save(); err != nil {
		t.Fatalf("failed to save: %v", err)
	}

	path := filepath.Join(SessionDir(), s.ID+".json")
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("failed to load: %v", err)
	}

	// String ToolInput becomes a string after round-trip
	toolBlock := loaded.Messages[0].Content[0]
	_, isString := toolBlock.ToolInput.(string)
	if !isString {
		t.Errorf("expected string ToolInput after round-trip, got %T", toolBlock.ToolInput)
	}

	// Verify the string content is preserved (the raw JSON is intact as a Go string)
	if toolBlock.ToolInput != `{"raw": "json"}` {
		t.Errorf("expected ToolInput string preserved, got %v", toolBlock.ToolInput)
	}
}
