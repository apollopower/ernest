package config

import (
	"encoding/json"
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
	cfg := &ClaudeConfig{}
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

	// Load settings.json for tool permissions
	for _, settingsPath := range []string{
		filepath.Join(home, ".claude", "settings.json"),
		filepath.Join(projectDir, ".claude", "settings.json"),
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
