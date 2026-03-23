package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// MCPServerConfig represents a single MCP server definition.
type MCPServerConfig struct {
	Command string            `json:"command,omitempty"`  // stdio: command to run
	Args    []string          `json:"args,omitempty"`     // stdio: command arguments
	Env     map[string]string `json:"env,omitempty"`      // stdio: environment variables
	Type    string            `json:"type,omitempty"`     // "http" or "sse" for remote
	URL     string            `json:"url,omitempty"`      // remote: server URL
	Headers map[string]string `json:"headers,omitempty"`  // remote: HTTP headers
}

// MCPConfig holds all configured MCP servers from all scopes.
type MCPConfig struct {
	Servers map[string]MCPServerConfig // name → config
}

// mcpConfigFile is the JSON structure of .mcp.json.
type mcpConfigFile struct {
	MCPServers map[string]MCPServerConfig `json:"mcpServers"`
}

// claudeJSONFile is the structure of ~/.claude.json which has both top-level
// and project-scoped mcpServers under projects.<path>.mcpServers.
type claudeJSONFile struct {
	MCPServers map[string]MCPServerConfig            `json:"mcpServers"`
	Projects   map[string]claudeJSONProjectConfig     `json:"projects"`
}

type claudeJSONProjectConfig struct {
	MCPServers map[string]MCPServerConfig `json:"mcpServers"`
}

// envVarRegex matches ${VAR} and ${VAR:-default} patterns.
var envVarRegex = regexp.MustCompile(`\$\{([^}:]+?)(?::-(.*?))?\}`)

// LoadMCPConfig reads MCP server configurations from:
// 1. ~/.claude.json (user scope — baseline)
// 2. .mcp.json at project root (project scope — overrides user on name collision)
func LoadMCPConfig(projectDir string) (*MCPConfig, error) {
	config := &MCPConfig{
		Servers: make(map[string]MCPServerConfig),
	}

	// 1. User scope: ~/.claude.json (top-level + project-scoped)
	home, err := os.UserHomeDir()
	if err == nil {
		userPath := filepath.Join(home, ".claude.json")
		if servers, err := loadClaudeJSON(userPath, projectDir); err == nil {
			for name, srv := range servers {
				config.Servers[name] = srv
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			log.Printf("[mcp] warning: %v", err)
		}
	}

	// 2. Project scope: .mcp.json (overrides user on name collision)
	if projectDir != "" {
		projectPath := filepath.Join(projectDir, ".mcp.json")
		if servers, err := loadMCPFile(projectPath); err == nil {
			for name, srv := range servers {
				config.Servers[name] = srv
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			log.Printf("[mcp] warning: %v", err)
		}
	}

	// Validate server names and expand env vars
	validated := make(map[string]MCPServerConfig)
	for name, srv := range config.Servers {
		if strings.Contains(name, "__") {
			// Reject names with __ to prevent tool namespacing ambiguity
			continue
		}
		validated[name] = expandServerConfig(srv)
	}
	config.Servers = validated

	return config, nil
}

// loadClaudeJSON reads ~/.claude.json, returning servers from both the top-level
// mcpServers and any project-scoped entry matching projectDir.
// Project-scoped servers override top-level on name collision.
func loadClaudeJSON(path, projectDir string) (map[string]MCPServerConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var file claudeJSONFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("cannot parse %s: %w", path, err)
	}

	servers := make(map[string]MCPServerConfig)

	// Top-level mcpServers (user scope baseline)
	for name, srv := range file.MCPServers {
		servers[name] = srv
	}

	// Project-scoped: projects.<projectDir>.mcpServers overrides top-level
	if projectDir != "" {
		if projCfg, ok := file.Projects[projectDir]; ok {
			for name, srv := range projCfg.MCPServers {
				servers[name] = srv
			}
		}
	}

	return servers, nil
}

// loadMCPFile reads and parses a single MCP config file (.mcp.json format).
func loadMCPFile(path string) (map[string]MCPServerConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var file mcpConfigFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("cannot parse %s: %w", path, err)
	}

	return file.MCPServers, nil
}

// expandServerConfig expands ${VAR} and ${VAR:-default} in all string fields.
func expandServerConfig(srv MCPServerConfig) MCPServerConfig {
	srv.Command = expandEnvVars(srv.Command)
	srv.URL = expandEnvVars(srv.URL)

	if srv.Args != nil {
		expanded := make([]string, len(srv.Args))
		for i, arg := range srv.Args {
			expanded[i] = expandEnvVars(arg)
		}
		srv.Args = expanded
	}

	if srv.Env != nil {
		expanded := make(map[string]string)
		for k, v := range srv.Env {
			expanded[k] = expandEnvVars(v)
		}
		srv.Env = expanded
	}

	if srv.Headers != nil {
		expanded := make(map[string]string)
		for k, v := range srv.Headers {
			expanded[k] = expandEnvVars(v)
		}
		srv.Headers = expanded
	}

	return srv
}

// expandEnvVars expands ${VAR} and ${VAR:-default} patterns in a string.
func expandEnvVars(s string) string {
	return envVarRegex.ReplaceAllStringFunc(s, func(match string) string {
		parts := envVarRegex.FindStringSubmatch(match)
		if len(parts) < 2 {
			return match
		}
		varName := parts[1]
		defaultVal := ""
		if len(parts) >= 3 {
			defaultVal = parts[2]
		}

		if val := os.Getenv(strings.TrimSpace(varName)); val != "" {
			return val
		}
		return defaultVal
	})
}
