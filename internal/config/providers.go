package config

import "os"

// ResolveAPIKey returns the API key value from the environment variable
// specified in the provider config. Returns empty string if not set.
func (p ProviderConfig) ResolveAPIKey() string {
	return os.Getenv(p.APIKeyEnv)
}

// HasAPIKey returns true if the provider's API key environment variable is set.
func (p ProviderConfig) HasAPIKey() bool {
	return p.ResolveAPIKey() != ""
}
