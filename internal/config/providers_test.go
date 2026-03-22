package config

import "testing"

func TestResolveAPIKeyWithCredentials_EnvVar(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "env-key")

	pc := ProviderConfig{Name: "anthropic", Model: "test"}
	creds := &Credentials{
		Providers: []ProviderCredential{
			{Name: "anthropic", APIKey: "cred-key"},
		},
	}

	// Env var should take precedence over credentials
	key := pc.ResolveAPIKeyWithCredentials(creds)
	if key != "env-key" {
		t.Errorf("expected env var key, got %q", key)
	}
}

func TestResolveAPIKeyWithCredentials_DeprecatedEnvVar(t *testing.T) {
	t.Setenv("MY_CUSTOM_KEY", "deprecated-key")

	pc := ProviderConfig{Name: "custom", APIKeyEnv: "MY_CUSTOM_KEY"}
	key := pc.ResolveAPIKeyWithCredentials(nil)
	if key != "deprecated-key" {
		t.Errorf("expected deprecated env var key, got %q", key)
	}
}

func TestResolveAPIKeyWithCredentials_Credentials(t *testing.T) {
	// No env vars set — should fall through to credentials
	t.Setenv("ANTHROPIC_API_KEY", "")

	pc := ProviderConfig{Name: "anthropic", Model: "test"}
	creds := &Credentials{
		Providers: []ProviderCredential{
			{Name: "anthropic", APIKey: "cred-key"},
		},
	}

	key := pc.ResolveAPIKeyWithCredentials(creds)
	if key != "cred-key" {
		t.Errorf("expected credentials key, got %q", key)
	}
}

func TestResolveAPIKeyWithCredentials_CaseInsensitive(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")

	pc := ProviderConfig{Name: "Anthropic", Model: "test"}
	creds := &Credentials{
		Providers: []ProviderCredential{
			{Name: "anthropic", APIKey: "cred-key"},
		},
	}

	key := pc.ResolveAPIKeyWithCredentials(creds)
	if key != "cred-key" {
		t.Errorf("expected case-insensitive match, got %q", key)
	}
}

func TestResolveAPIKeyWithCredentials_Empty(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")

	pc := ProviderConfig{Name: "anthropic"}
	key := pc.ResolveAPIKeyWithCredentials(&Credentials{})
	if key != "" {
		t.Errorf("expected empty key, got %q", key)
	}
}

func TestResolveAPIKeyWithCredentials_NilCredentials(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")

	pc := ProviderConfig{Name: "anthropic"}
	key := pc.ResolveAPIKeyWithCredentials(nil)
	if key != "" {
		t.Errorf("expected empty key with nil credentials, got %q", key)
	}
}

func TestResolveAPIKeyWithCredentials_CustomProviderNoEnvVar(t *testing.T) {
	// Custom provider names (not in knownEnvVars) don't get automatic env var lookup
	t.Setenv("MYCUSTOM_API_KEY", "should-not-match")

	pc := ProviderConfig{Name: "mycustom", Model: "test"}
	creds := &Credentials{
		Providers: []ProviderCredential{
			{Name: "mycustom", APIKey: "cred-key"},
		},
	}

	key := pc.ResolveAPIKeyWithCredentials(creds)
	// Should get credentials key, not env var (mycustom is not in knownEnvVars)
	if key != "cred-key" {
		t.Errorf("expected credentials key for custom provider, got %q", key)
	}
}

func TestResolveAPIKeyWithCredentials_SiliconFlowEnvVar(t *testing.T) {
	// SiliconFlow is a known provider — env var should work
	t.Setenv("SILICONFLOW_API_KEY", "sf-env-key")

	pc := ProviderConfig{Name: "siliconflow", Model: "test"}
	key := pc.ResolveAPIKeyWithCredentials(nil)
	if key != "sf-env-key" {
		t.Errorf("expected SiliconFlow env var key, got %q", key)
	}
}
