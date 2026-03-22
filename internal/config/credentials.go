package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Credentials stores API keys for providers, separate from config.
// File: ~/.config/ernest/credentials.yaml (0600 permissions).
type Credentials struct {
	Providers []ProviderCredential `yaml:"providers"`
}

// ProviderCredential holds the API key for a single provider.
// Only secrets live here — base_url, model, priority live in config.yaml.
type ProviderCredential struct {
	Name   string `yaml:"name"`
	APIKey string `yaml:"api_key"`
}

// LoadCredentials reads the credentials file. Returns an empty Credentials
// if the file does not exist.
func LoadCredentials() (*Credentials, error) {
	path := CredentialsPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Credentials{}, nil
		}
		return nil, fmt.Errorf("cannot read credentials: %w", err)
	}

	var creds Credentials
	if err := yaml.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("cannot parse credentials: %w", err)
	}
	return &creds, nil
}

// SaveCredentials writes the credentials file atomically (write-to-temp-then-rename).
// File permissions: 0600 on POSIX systems.
func SaveCredentials(creds *Credentials) error {
	path := CredentialsPath()
	dir := filepath.Dir(path)

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("cannot create config directory: %w", err)
	}

	data, err := yaml.Marshal(creds)
	if err != nil {
		return fmt.Errorf("cannot marshal credentials: %w", err)
	}

	// Atomic write: temp file then rename
	tmp, err := os.CreateTemp(dir, "credentials-*.yaml.tmp")
	if err != nil {
		return fmt.Errorf("cannot create temp file: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("cannot write credentials: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("cannot close temp file: %w", err)
	}

	// Set permissions before rename so the file is never world-readable
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("cannot set permissions: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("cannot rename credentials file: %w", err)
	}

	return nil
}

// GetKey returns the API key for a provider, or empty string if not found.
// Lookup is case-insensitive.
func (c *Credentials) GetKey(providerName string) string {
	name := strings.ToLower(providerName)
	for _, p := range c.Providers {
		if strings.ToLower(p.Name) == name {
			return p.APIKey
		}
	}
	return ""
}

// SetKey sets or updates the API key for a provider.
func (c *Credentials) SetKey(providerName, apiKey string) {
	for i, p := range c.Providers {
		if p.Name == providerName {
			c.Providers[i].APIKey = apiKey
			return
		}
	}
	c.Providers = append(c.Providers, ProviderCredential{
		Name:   providerName,
		APIKey: apiKey,
	})
}

// Remove deletes a provider's credentials.
func (c *Credentials) Remove(providerName string) {
	for i, p := range c.Providers {
		if p.Name == providerName {
			c.Providers = append(c.Providers[:i], c.Providers[i+1:]...)
			return
		}
	}
}

// CredentialsPath returns the path to the credentials file.
func CredentialsPath() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = "."
	}
	return filepath.Join(configDir, "ernest", "credentials.yaml")
}
