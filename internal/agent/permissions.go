package agent

import (
	"ernest/internal/config"
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
type PermissionChecker struct {
	mu           sync.RWMutex
	allowedTools map[string]bool
	deniedTools  map[string]bool
}

// NewPermissionChecker creates a checker from the Claude config's
// allowedTools and deniedTools lists. Tool names are matched exactly.
func NewPermissionChecker(claudeCfg *config.ClaudeConfig) *PermissionChecker {
	allowed := make(map[string]bool)
	denied := make(map[string]bool)

	if claudeCfg != nil {
		for _, name := range claudeCfg.AllowedTools {
			allowed[name] = true
		}
		for _, name := range claudeCfg.DeniedTools {
			denied[name] = true
		}
	}

	return &PermissionChecker{
		allowedTools: allowed,
		deniedTools:  denied,
	}
}

// Check returns the permission for a given tool name.
// Denied takes precedence over Allowed if a tool appears in both lists.
func (p *PermissionChecker) Check(toolName string) Permission {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.deniedTools[toolName] {
		return PermissionDenied
	}
	if p.allowedTools[toolName] {
		return PermissionAllowed
	}
	return PermissionAsk
}

// Allow adds a tool to the in-memory allowed list.
func (p *PermissionChecker) Allow(toolName string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.allowedTools[toolName] = true
}
