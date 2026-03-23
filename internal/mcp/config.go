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
	"time"
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

// LoadMCPConfig reads MCP server configurations from (in priority order):
// 1. Claude Code plugins (lowest — baseline from ~/.claude/plugins/)
// 2. ~/.claude.json (user scope — overrides plugins on name collision)
// 3. .mcp.json at project root (highest — overrides all on name collision)
func LoadMCPConfig(projectDir string) (*MCPConfig, error) {
	config := &MCPConfig{
		Servers: make(map[string]MCPServerConfig),
	}

	// 1. Claude Code plugins (lowest priority)
	home, err := os.UserHomeDir()
	if err == nil {
		if servers, err := loadClaudePlugins(home); err == nil {
			for name, srv := range servers {
				config.Servers[name] = srv
			}
		}
	}

	// 2. User scope: ~/.claude.json (overrides plugins)
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

	// 3. Project scope: .mcp.json (highest priority)
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
		validated[name] = ExpandServerConfig(srv)
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

// installedPluginsFile is the JSON structure of ~/.claude/plugins/installed_plugins.json.
type installedPluginsFile struct {
	Plugins map[string][]pluginInstall `json:"plugins"`
}

type pluginInstall struct {
	Scope       string `json:"scope"`
	InstallPath string `json:"installPath"`
}

// credentialsFile is the JSON structure of ~/.claude/.credentials.json.
type credentialsFile struct {
	MCPOAuth map[string]oauthEntry `json:"mcpOAuth"`
}

type oauthEntry struct {
	ServerURL   string `json:"serverUrl"`
	AccessToken string `json:"accessToken"`
	ExpiresAt   int64  `json:"expiresAt"` // ms since epoch, 0 = no expiry
}

// loadClaudePlugins reads Claude Code installed plugins and resolves OAuth tokens.
// Returns MCP servers from plugins with auth headers injected where tokens exist.
func loadClaudePlugins(home string) (map[string]MCPServerConfig, error) {
	pluginsPath := filepath.Join(home, ".claude", "plugins", "installed_plugins.json")
	data, err := os.ReadFile(pluginsPath)
	if err != nil {
		return nil, err
	}

	var plugins installedPluginsFile
	if err := json.Unmarshal(data, &plugins); err != nil {
		return nil, fmt.Errorf("cannot parse %s: %w", pluginsPath, err)
	}

	// Load OAuth tokens
	tokens := loadOAuthTokens(home)

	servers := make(map[string]MCPServerConfig)
	for _, installs := range plugins.Plugins {
		for _, inst := range installs {
			mcpPath := filepath.Join(inst.InstallPath, ".mcp.json")
			pluginServers, err := loadPluginMCPFile(mcpPath)
			if err != nil {
				if !errors.Is(err, os.ErrNotExist) {
					log.Printf("[mcp] warning: plugin %s: %v", mcpPath, err)
				}
				continue
			}
			for name, srv := range pluginServers {
				// Inject OAuth token if available
				if srv.URL != "" {
					if token, ok := tokens[srv.URL]; ok {
						if srv.Headers == nil {
							srv.Headers = make(map[string]string)
						}
						srv.Headers["Authorization"] = "Bearer " + token
					}
				}
				servers[name] = srv
			}
		}
	}

	return servers, nil
}

// loadOAuthTokens reads Claude Code's OAuth token store and returns a URL → token map.
// Expired tokens are excluded.
func loadOAuthTokens(home string) map[string]string {
	credsPath := filepath.Join(home, ".claude", ".credentials.json")
	data, err := os.ReadFile(credsPath)
	if err != nil {
		return nil
	}

	var creds credentialsFile
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil
	}

	now := time.Now().UnixMilli()
	tokens := make(map[string]string)
	for _, entry := range creds.MCPOAuth {
		if entry.AccessToken == "" || entry.ServerURL == "" {
			continue
		}
		// expiresAt == 0 means no expiration
		if entry.ExpiresAt > 0 && entry.ExpiresAt < now {
			log.Printf("[mcp] skipping expired token for %s", entry.ServerURL)
			continue
		}
		tokens[entry.ServerURL] = entry.AccessToken
	}
	return tokens
}

// loadPluginMCPFile reads a plugin's .mcp.json, handling both formats:
// - Standard: {"mcpServers": {"name": {...}}}
// - Flat:     {"name": {"type": "http", "url": "..."}}
func loadPluginMCPFile(path string) (map[string]MCPServerConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// Try standard format first
	var standard mcpConfigFile
	if err := json.Unmarshal(data, &standard); err == nil && len(standard.MCPServers) > 0 {
		return standard.MCPServers, nil
	}

	// Try flat format: {"name": {"type": "http", "url": "..."}}
	var flat map[string]MCPServerConfig
	if err := json.Unmarshal(data, &flat); err != nil {
		return nil, fmt.Errorf("cannot parse %s: %w", path, err)
	}
	// Filter out entries that aren't valid server configs (e.g., "mcpServers" key parsed as flat)
	for k, v := range flat {
		if v.Command == "" && v.URL == "" {
			delete(flat, k)
		}
	}
	return flat, nil
}

// ExpandServerConfig expands ${VAR} and ${VAR:-default} in all string fields.
func ExpandServerConfig(srv MCPServerConfig) MCPServerConfig {
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

// SaveServerToProjectConfig adds or updates a server in .mcp.json at projectDir.
// Creates the file if it doesn't exist.
func SaveServerToProjectConfig(projectDir, name string, cfg MCPServerConfig) error {
	path := filepath.Join(projectDir, ".mcp.json")

	var file mcpConfigFile
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &file); err != nil {
			return fmt.Errorf("cannot parse %s: %w", path, err)
		}
	}
	if file.MCPServers == nil {
		file.MCPServers = make(map[string]MCPServerConfig)
	}

	file.MCPServers[name] = cfg
	return writeMCPFile(path, &file)
}

// RemoveServerFromProjectConfig removes a server from .mcp.json at projectDir.
func RemoveServerFromProjectConfig(projectDir, name string) error {
	path := filepath.Join(projectDir, ".mcp.json")

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("cannot read %s: %w", path, err)
	}

	var file mcpConfigFile
	if err := json.Unmarshal(data, &file); err != nil {
		return fmt.Errorf("cannot parse %s: %w", path, err)
	}

	if _, ok := file.MCPServers[name]; !ok {
		return fmt.Errorf("server %q not found in %s", name, path)
	}

	delete(file.MCPServers, name)
	return writeMCPFile(path, &file)
}

func writeMCPFile(path string, file *mcpConfigFile) error {
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
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
