package tui

import "testing"

func TestFilterCommands_Empty(t *testing.T) {
	// Empty prefix returns all commands except "q" alias
	results := filterCommands("")
	if len(results) == 0 {
		t.Fatal("expected non-empty results for empty prefix")
	}
	for _, r := range results {
		if r.Name == "q" {
			t.Error("expected 'q' alias to be excluded from full list")
		}
	}
}

func TestFilterCommands_Prefix(t *testing.T) {
	tests := []struct {
		prefix string
		want   []string
	}{
		{"pro", []string{"providers", "provider"}},
		{"q", []string{"quit", "q"}},
		{"m", []string{"model", "mcp"}},
		{"plan", []string{"plan"}},
		{"zzz", nil},
	}
	for _, tt := range tests {
		results := filterCommands(tt.prefix)
		got := make([]string, len(results))
		for i, r := range results {
			got[i] = r.Name
		}
		if len(got) != len(tt.want) {
			t.Errorf("filterCommands(%q): got %v, want %v", tt.prefix, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("filterCommands(%q)[%d]: got %q, want %q", tt.prefix, i, got[i], tt.want[i])
			}
		}
	}
}

func TestAutocomplete_Update(t *testing.T) {
	var ac AutocompleteModel

	// Typing "/" shows autocomplete
	if !ac.Update("/") {
		t.Error("expected visible after '/'")
	}
	if len(ac.items) == 0 {
		t.Error("expected items after '/'")
	}

	// Typing "/pro" filters
	ac.Update("/pro")
	if len(ac.items) != 2 {
		t.Errorf("expected 2 items for '/pro', got %d", len(ac.items))
	}

	// Typing a space dismisses (it's now a command + args)
	ac.Update("/provider add")
	if ac.visible {
		t.Error("expected hidden when input contains space")
	}

	// Non-slash input dismisses
	ac.Update("hello")
	if ac.visible {
		t.Error("expected hidden for non-slash input")
	}

	// No matches dismisses
	ac.Update("/zzz")
	if ac.visible {
		t.Error("expected hidden for no matches")
	}
}

func TestAutocomplete_Navigation(t *testing.T) {
	var ac AutocompleteModel
	ac.Update("/")

	initial := ac.Selected()
	if initial == "" {
		t.Fatal("expected a selection")
	}

	// Move down
	ac.MoveDown()
	second := ac.Selected()
	if second == initial {
		t.Error("expected cursor to move down")
	}

	// Move up
	ac.MoveUp()
	if ac.Selected() != initial {
		t.Error("expected cursor to move back up")
	}

	// MoveUp at top stays at top
	ac.MoveUp()
	if ac.Selected() != initial {
		t.Error("expected cursor to stay at top")
	}
}

func TestAutocomplete_Dismiss(t *testing.T) {
	var ac AutocompleteModel
	ac.Update("/")
	if !ac.visible {
		t.Fatal("expected visible")
	}

	ac.Dismiss()
	if ac.visible {
		t.Error("expected hidden after dismiss")
	}
	if len(ac.items) != 0 {
		t.Error("expected items cleared after dismiss")
	}
}
