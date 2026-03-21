package tools

import (
	"context"
	"encoding/json"
	"ernest/internal/provider"
	"fmt"
	"sort"
)

// Tool is a capability the agent can invoke (file ops, bash, etc.)
type Tool interface {
	Name() string
	Description() string
	InputSchema() map[string]any
	Execute(ctx context.Context, input json.RawMessage) (string, error)
	RequiresConfirmation(input json.RawMessage) bool
}

// Registry holds all available tools and converts them to provider ToolDefs.
type Registry struct {
	tools map[string]Tool
	order []string // stable ordering for ToolDefs()
}

func NewRegistry(tools ...Tool) *Registry {
	r := &Registry{tools: make(map[string]Tool)}
	for _, t := range tools {
		if _, exists := r.tools[t.Name()]; exists {
			panic(fmt.Sprintf("duplicate tool name: %s", t.Name()))
		}
		r.tools[t.Name()] = t
		r.order = append(r.order, t.Name())
	}
	sort.Strings(r.order)
	return r
}

func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// ToolDefs returns tool definitions in stable sorted order for provider API
// calls. Stable ordering preserves Anthropic prompt cache hits.
func (r *Registry) ToolDefs() []provider.ToolDef {
	defs := make([]provider.ToolDef, 0, len(r.order))
	for _, name := range r.order {
		t := r.tools[name]
		defs = append(defs, provider.ToolDef{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: t.InputSchema(),
		})
	}
	return defs
}
