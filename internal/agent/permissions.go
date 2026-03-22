package agent

import (
	"encoding/json"
	"ernest/internal/config"
	"log"
	"strings"
	"sync"
)

// Permission represents the result of a tool permission check.
type Permission int

const (
	PermissionAsk     Permission = iota // prompt the user
	PermissionAllowed                   // auto-approve
	PermissionDenied                    // auto-deny
)

// PermissionChecker determines whether a tool should be allowed, denied,
// or require user confirmation based on .claude/settings.json.
//
// Supports granular permission entries:
//   - "bash" — allows all bash commands
//   - "bash(git pull)" — allows only this exact command
//   - "bash(git *)" — allows any command starting with "git "
//   - "read_file", "write_file", etc. — exact tool name match
type PermissionChecker struct {
	mu           sync.RWMutex
	allowedTools []string // preserves order and patterns
	deniedTools  []string
	// Fast lookup for exact tool name matches (no pattern)
	allowedExact map[string]bool
	deniedExact  map[string]bool
}

// NewPermissionChecker creates a checker from the Claude config's
// allowedTools and deniedTools lists.
func NewPermissionChecker(claudeCfg *config.ClaudeConfig) *PermissionChecker {
	pc := &PermissionChecker{
		allowedExact: make(map[string]bool),
		deniedExact:  make(map[string]bool),
	}

	if claudeCfg != nil {
		for _, entry := range claudeCfg.AllowedTools {
			pc.addAllowed(entry)
		}
		for _, entry := range claudeCfg.DeniedTools {
			pc.addDenied(entry)
		}
	}

	return pc
}

// addAllowed/addDenied must be called with p.mu held (or during construction).
func (p *PermissionChecker) addAllowed(entry string) {
	if !strings.Contains(entry, "(") {
		p.allowedExact[entry] = true
	} else if !isValidPatternEntry(entry) {
		log.Printf("[permissions] skipping malformed allowed entry: %q", entry)
		return
	}
	p.allowedTools = append(p.allowedTools, entry)
}

func (p *PermissionChecker) addDenied(entry string) {
	if !strings.Contains(entry, "(") {
		p.deniedExact[entry] = true
	} else if !isValidPatternEntry(entry) {
		log.Printf("[permissions] skipping malformed denied entry: %q", entry)
		return
	}
	p.deniedTools = append(p.deniedTools, entry)
}

// isValidPatternEntry checks that a "tool(pattern)" entry is well-formed.
func isValidPatternEntry(entry string) bool {
	parenIdx := strings.Index(entry, "(")
	return parenIdx > 0 && strings.HasSuffix(entry, ")")
}

// Check returns the permission for a given tool invocation.
// toolInput is the raw JSON input (used for granular matching like bash commands).
// Denied takes precedence over Allowed if a tool appears in both lists.
func (p *PermissionChecker) Check(toolName string, toolInput json.RawMessage) Permission {
	p.mu.RLock()
	defer p.mu.RUnlock()

	// Check denied first (takes precedence)
	if p.deniedExact[toolName] {
		return PermissionDenied
	}
	for _, entry := range p.deniedTools {
		if !strings.Contains(entry, "(") {
			continue // already handled by deniedExact
		}
		if matchPermission(entry, toolName, toolInput) {
			return PermissionDenied
		}
	}

	// Check allowed
	if p.allowedExact[toolName] {
		return PermissionAllowed
	}
	for _, entry := range p.allowedTools {
		if !strings.Contains(entry, "(") {
			continue // already handled by allowedExact
		}
		if matchPermission(entry, toolName, toolInput) {
			return PermissionAllowed
		}
	}

	return PermissionAsk
}

// Allow adds a permission entry to the in-memory allowed list.
// entry can be "bash", "bash(git pull)", "bash(git *)", etc.
// Deduplicates: no-op if the entry already exists.
func (p *PermissionChecker) Allow(entry string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	// Check for duplicate
	for _, e := range p.allowedTools {
		if e == entry {
			return
		}
	}
	p.addAllowed(entry)
}

// PermissionKey builds the permission entry string for a tool invocation.
// For bash: "bash(command)". Returns empty string if the command can't be
// extracted — callers should skip persisting in that case to avoid saving
// an overly broad "bash" allow-all entry.
func PermissionKey(toolName string, toolInput json.RawMessage) string {
	if toolName == "bash" {
		var input struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal(toolInput, &input); err != nil || strings.TrimSpace(input.Command) == "" {
			return "" // don't fall back to plain "bash"
		}
		return "bash(" + input.Command + ")"
	}
	return toolName
}

// matchPermission checks if a permission entry matches a tool invocation.
func matchPermission(entry, toolName string, toolInput json.RawMessage) bool {
	// Parse "tool(pattern)" format — validated on load, so this should always pass
	parenIdx := strings.Index(entry, "(")
	if parenIdx < 0 || !strings.HasSuffix(entry, ")") {
		return false
	}

	entryTool := entry[:parenIdx]
	pattern := entry[parenIdx+1 : len(entry)-1]

	if entryTool != toolName {
		return false
	}

	// Extract the relevant value from tool input
	value := extractMatchValue(toolName, toolInput)
	if value == "" {
		return false
	}

	return matchGlob(pattern, value)
}

// matchGlob performs simple glob matching where * matches any characters
// (including /). Unlike filepath.Match, this does not treat / as a special
// separator — important for matching shell commands like "go test ./...".
func matchGlob(pattern, value string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == value // no wildcard, exact match
	}

	// Must start with the first part
	if !strings.HasPrefix(value, parts[0]) {
		return false
	}

	pos := len(parts[0])
	for i := 1; i < len(parts)-1; i++ {
		idx := strings.Index(value[pos:], parts[i])
		if idx < 0 {
			return false
		}
		pos += idx + len(parts[i])
	}

	// Must end with the last part
	return strings.HasSuffix(value[pos:], parts[len(parts)-1])
}

// extractMatchValue gets the string to match against for a given tool.
// Currently only bash commands support granular matching.
func extractMatchValue(toolName string, toolInput json.RawMessage) string {
	if toolInput == nil {
		return ""
	}

	if toolName == "bash" {
		var input struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(toolInput, &input) == nil {
			return input.Command
		}
	}

	return ""
}
