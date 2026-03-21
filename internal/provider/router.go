package provider

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// Router tries providers in priority order, falling back on failure.
type Router struct {
	providers   []Provider
	mu          sync.Mutex
	healthCache map[string]healthEntry
	cooldown    time.Duration
}

type healthEntry struct {
	healthy   bool
	lastCheck time.Time
	failures  int
}

// NewRouter creates a router that tries providers in the order given.
// cooldown controls how long an unhealthy provider is skipped before retrying.
func NewRouter(providers []Provider, cooldown time.Duration) *Router {
	return &Router{
		providers:   providers,
		healthCache: make(map[string]healthEntry),
		cooldown:    cooldown,
	}
}

// Stream tries each provider in priority order, falling back on failure.
// Returns the event channel, the name of the provider that succeeded, and any error.
func (r *Router) Stream(ctx context.Context, systemPrompt string,
	messages []Message, tools []ToolDef) (<-chan StreamEvent, string, error) {

	// Snapshot which providers to try (lock only for health cache access)
	r.mu.Lock()
	var candidates []Provider
	for _, p := range r.providers {
		entry := r.healthCache[p.Name()]
		if !entry.healthy && entry.failures > 0 && time.Since(entry.lastCheck) < r.cooldown {
			log.Printf("[router] skipping %s (cooldown, %d failures)", p.Name(), entry.failures)
			continue
		}
		candidates = append(candidates, p)
	}
	r.mu.Unlock()

	// Try candidates without holding the lock (network I/O)
	for _, p := range candidates {
		ch, err := p.Stream(ctx, systemPrompt, messages, tools)
		if err != nil {
			log.Printf("[router] %s failed: %v, trying next", p.Name(), err)
			r.mu.Lock()
			r.markUnhealthy(p.Name())
			r.mu.Unlock()
			continue
		}

		r.mu.Lock()
		r.markHealthy(p.Name())
		r.mu.Unlock()
		return ch, p.Name(), nil
	}

	return nil, "", fmt.Errorf("all providers exhausted")
}

// markUnhealthy must be called with r.mu held.
func (r *Router) markUnhealthy(name string) {
	entry := r.healthCache[name]
	entry.healthy = false
	entry.failures++
	entry.lastCheck = time.Now()
	r.healthCache[name] = entry
}

// markHealthy must be called with r.mu held.
func (r *Router) markHealthy(name string) {
	r.healthCache[name] = healthEntry{healthy: true, lastCheck: time.Now()}
}
