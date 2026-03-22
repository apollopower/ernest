package config

import (
	"os"
	"strings"
)

// knownEnvVars maps known provider names to their conventional env var names.
// Custom provider names do NOT get automatic env var lookup.
var knownEnvVars = map[string]string{
	"anthropic":   "ANTHROPIC_API_KEY",
	"openai":      "OPENAI_API_KEY",
	"gemini":      "GEMINI_API_KEY",
	"siliconflow": "SILICONFLOW_API_KEY",
}

// ResolveAPIKey returns the API key from the environment variable specified
// in api_key_env. Deprecated — use ResolveAPIKeyWithCredentials instead.
func (p ProviderConfig) ResolveAPIKey() string {
	return os.Getenv(p.APIKeyEnv)
}

// ResolveAPIKeyWithCredentials resolves the API key using the full resolution order:
// 1. api_key_env env var (deprecated, backward compat)
// 2. Conventional env var for known providers (ANTHROPIC_API_KEY, OPENAI_API_KEY, GEMINI_API_KEY)
// 3. Credentials file
// 4. Empty string (unconfigured)
func (p ProviderConfig) ResolveAPIKeyWithCredentials(creds *Credentials) string {
	// 1. Explicit api_key_env (deprecated)
	if p.APIKeyEnv != "" {
		if key := os.Getenv(p.APIKeyEnv); key != "" {
			return key
		}
	}

	// 2. Conventional env var for known providers
	name := strings.ToLower(p.Name)
	if envVar, ok := knownEnvVars[name]; ok {
		if key := os.Getenv(envVar); key != "" {
			return key
		}
	}

	// 3. Credentials file (case-insensitive lookup)
	if creds != nil {
		if key := creds.GetKey(strings.ToLower(p.Name)); key != "" {
			return key
		}
	}

	return ""
}

// HasAPIKey returns true if the provider's API key environment variable is set.
func (p ProviderConfig) HasAPIKey() bool {
	return p.ResolveAPIKey() != ""
}

// HasAPIKeyWithCredentials returns true if the provider has an API key from any source.
func (p ProviderConfig) HasAPIKeyWithCredentials(creds *Credentials) bool {
	return p.ResolveAPIKeyWithCredentials(creds) != ""
}
