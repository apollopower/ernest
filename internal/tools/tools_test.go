package tools

import (
	"context"
	"encoding/json"
	"testing"
)

// mockTool implements Tool for testing the registry.
type mockTool struct {
	name string
}

func (t *mockTool) Name() string                                                  { return t.name }
func (t *mockTool) Description() string                                           { return "mock " + t.name }
func (t *mockTool) InputSchema() map[string]any                                   { return map[string]any{"type": "object"} }
func (t *mockTool) Execute(_ context.Context, _ json.RawMessage) (string, error)  { return "ok", nil }
func (t *mockTool) RequiresConfirmation(_ json.RawMessage) bool                   { return false }

func TestRegistry_GetAndToolDefs(t *testing.T) {
	r := NewRegistry(&mockTool{name: "zebra"}, &mockTool{name: "alpha"}, &mockTool{name: "mid"})

	// Get existing tool
	tool, ok := r.Get("alpha")
	if !ok || tool.Name() != "alpha" {
		t.Error("expected to find 'alpha'")
	}

	// Get missing tool
	_, ok = r.Get("missing")
	if ok {
		t.Error("expected 'missing' to not be found")
	}

	// ToolDefs should be sorted by name
	defs := r.ToolDefs()
	if len(defs) != 3 {
		t.Fatalf("expected 3 defs, got %d", len(defs))
	}
	if defs[0].Name != "alpha" || defs[1].Name != "mid" || defs[2].Name != "zebra" {
		t.Errorf("expected sorted order [alpha, mid, zebra], got [%s, %s, %s]",
			defs[0].Name, defs[1].Name, defs[2].Name)
	}
}

func TestRegistry_Empty(t *testing.T) {
	r := NewRegistry()
	defs := r.ToolDefs()
	if len(defs) != 0 {
		t.Errorf("expected 0 defs, got %d", len(defs))
	}
}
