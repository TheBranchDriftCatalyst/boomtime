// config_github_test.go — inert-safe invariant for the GitHub-connect gate
// (boom-2ip Phase 1). GithubConnectEnabled() must fail closed unless ALL of:
// the master gate, client id, client secret, and state signing key are present.
package config

import "testing"

func TestGithubConnectEnabled(t *testing.T) {
	full := func() *Config {
		return &Config{
			FeatureGithubStats:      true,
			GithubOAuthClientID:     "cid",
			GithubOAuthClientSecret: "csecret",
			OAuthStateSigningKey:    "signing-key",
		}
	}

	if !full().GithubConnectEnabled() {
		t.Fatal("fully configured → expected enabled")
	}

	// Default zero-value config (the inert default boot) MUST be off.
	if (&Config{}).GithubConnectEnabled() {
		t.Fatal("zero-value config → expected inert (disabled)")
	}

	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"gate off", func(c *Config) { c.FeatureGithubStats = false }},
		{"no client id", func(c *Config) { c.GithubOAuthClientID = "" }},
		{"no client secret", func(c *Config) { c.GithubOAuthClientSecret = "" }},
		{"no signing key", func(c *Config) { c.OAuthStateSigningKey = "" }},
		{"whitespace client id", func(c *Config) { c.GithubOAuthClientID = "   " }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := full()
			tc.mutate(c)
			if c.GithubConnectEnabled() {
				t.Fatalf("%s → expected disabled (fail closed)", tc.name)
			}
		})
	}
}
