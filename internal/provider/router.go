package provider

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

const (
	maxRetries     = 3
	baseRetryDelay = 1 * time.Second
	maxRetryAfter  = 30 * time.Second
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

// Stream tries each provider in priority order with retry for transient errors.
// Retryable errors (429, 5xx) are retried up to 3 times with exponential backoff
// before falling back to the next provider. Non-retryable errors fall back immediately.
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

	// Try candidates with retry for transient errors
	for _, p := range candidates {
		var lastErr error
		for attempt := 0; attempt <= maxRetries; attempt++ {
			if attempt > 0 {
				delay := retryDelay(lastErr, attempt)
				log.Printf("[router] retrying %s in %v (attempt %d/%d)", p.Name(), delay, attempt, maxRetries)
				select {
				case <-time.After(delay):
				case <-ctx.Done():
					return nil, "", ctx.Err()
				}
			}

			ch, err := p.Stream(ctx, systemPrompt, messages, tools)
			if err == nil {
				r.mu.Lock()
				r.markHealthy(p.Name())
				r.mu.Unlock()
				return ch, p.Name(), nil
			}

			lastErr = err
			if !IsRetryable(err) {
				break // non-retryable, fall back immediately
			}
		}

		log.Printf("[router] %s failed: %v, trying next", p.Name(), lastErr)
		r.mu.Lock()
		r.markUnhealthy(p.Name())
		r.mu.Unlock()
	}

	return nil, "", fmt.Errorf("all providers exhausted")
}

// retryDelay computes the delay before a retry attempt.
// Uses Retry-After from 429 responses when available, otherwise exponential backoff.
func retryDelay(err error, attempt int) time.Duration {
	if apiErr, ok := err.(*APIError); ok && apiErr.RetryAfter > 0 {
		if apiErr.RetryAfter > maxRetryAfter {
			log.Printf("[router] capping Retry-After %v to %v", apiErr.RetryAfter, maxRetryAfter)
			return maxRetryAfter
		}
		return apiErr.RetryAfter
	}
	// Exponential backoff: 1s, 2s, 4s
	return baseRetryDelay * (1 << (attempt - 1))
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
