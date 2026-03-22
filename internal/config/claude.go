package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ClaudeConfig struct {
	SystemPrompt   string
	Rules          []string
	AllowedTools   []string
	DeniedTools    []string
	PermissionMode string
	ProjectDir     string // stored for writing back to settings.local.json
}

// LoadClaudeConfig reads the .claude/ directory structure and assembles
// a provider-agnostic configuration.
//
// Resolution order (mirroring Claude Code):
//  1. ~/.claude/CLAUDE.md           (user-global instructions)
//  2. ~/.claude/settings.json       (user-global settings)
//  3. .claude/CLAUDE.md             (project instructions)
//  4. .claude/settings.json         (project settings)
//  5. .claude/rules/*.md            (project rules)
//  6. CLAUDE.md in repo root        (legacy location)
func LoadClaudeConfig(projectDir string) (*ClaudeConfig, error) {
	cfg := &ClaudeConfig{ProjectDir: projectDir}
	home, _ := os.UserHomeDir()
	var promptParts []string

	// User-global CLAUDE.md
	if content, err := os.ReadFile(filepath.Join(home, ".claude", "CLAUDE.md")); err == nil {
		promptParts = append(promptParts, "# User Instructions\n\n"+string(content))
	}

	// Project CLAUDE.md
	if content, err := os.ReadFile(filepath.Join(projectDir, ".claude", "CLAUDE.md")); err == nil {
		promptParts = append(promptParts, "# Project Instructions\n\n"+string(content))
	}

	// Legacy CLAUDE.md at repo root
	if content, err := os.ReadFile(filepath.Join(projectDir, "CLAUDE.md")); err == nil {
		promptParts = append(promptParts, "# Project Instructions (root)\n\n"+string(content))
	}

	// Rules directory
	rulesDir := filepath.Join(projectDir, ".claude", "rules")
	if entries, err := os.ReadDir(rulesDir); err == nil {
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".md") {
				if content, err := os.ReadFile(filepath.Join(rulesDir, entry.Name())); err == nil {
					cfg.Rules = append(cfg.Rules, string(content))
					promptParts = append(promptParts, "# Rule: "+entry.Name()+"\n\n"+string(content))
				}
			}
		}
	}

	cfg.SystemPrompt = strings.Join(promptParts, "\n\n---\n\n")

	// Load settings for tool permissions.
	// Resolution order: user-global → project shared → project local.
	// Later entries append to AllowedTools/DeniedTools and override PermissionMode.
	for _, settingsPath := range []string{
		filepath.Join(home, ".claude", "settings.json"),
		filepath.Join(projectDir, ".claude", "settings.json"),
		filepath.Join(projectDir, ".claude", "settings.local.json"),
	} {
		if data, err := os.ReadFile(settingsPath); err == nil {
			var settings struct {
				AllowedTools []string `json:"allowedTools"`
				DeniedTools  []string `json:"deniedTools"`
				Permissions  struct {
					Mode string `json:"mode"`
				} `json:"permissions"`
			}
			if json.Unmarshal(data, &settings) == nil {
				if len(settings.AllowedTools) > 0 {
					cfg.AllowedTools = append(cfg.AllowedTools, settings.AllowedTools...)
				}
				if len(settings.DeniedTools) > 0 {
					cfg.DeniedTools = append(cfg.DeniedTools, settings.DeniedTools...)
				}
				if settings.Permissions.Mode != "" {
					cfg.PermissionMode = settings.Permissions.Mode
				}
			}
		}
	}

	return cfg, nil
}

// SaveAllowedTool adds a tool name to the project-local settings file
// (.claude/settings.local.json). Creates the file if it doesn't exist.
func SaveAllowedTool(projectDir, toolName string) error {
	path := filepath.Join(projectDir, ".claude", "settings.local.json")

	// Read existing settings. Only start fresh if the file doesn't exist;
	// other read errors (permissions, I/O) are propagated.
	var settings map[string]any
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &settings); err != nil {
			return fmt.Errorf("cannot parse existing settings file %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("cannot read %s: %w", path, err)
	}
	if settings == nil {
		settings = make(map[string]any)
	}

	// Get or create allowedTools list
	var allowed []string
	if existing, ok := settings["allowedTools"].([]any); ok {
		for _, v := range existing {
			if s, ok := v.(string); ok {
				if s == toolName {
					return nil // already present
				}
				allowed = append(allowed, s)
			}
		}
	}
	allowed = append(allowed, toolName)
	settings["allowedTools"] = allowed

	// Ensure .claude directory exists
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("cannot create .claude directory: %w", err)
	}

	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("cannot marshal settings: %w", err)
	}

	return os.WriteFile(path, data, 0o644)
}
