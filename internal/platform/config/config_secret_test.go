package config

import (
	"strings"
	"testing"
)

// The placeholder in .env.example is public (it is in the repo). Using it anywhere reachable from outside
// would let anyone forge tokens, so the app refuses to start with it unless it is plain local development.
const placeholderSecret = "dev-only-secret-change-me-dev-only-secret"

func TestLoad_RejectsThePublicPlaceholderSecretWhenExposed(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr bool
	}{
		{"plain local development", map[string]string{"APP_ENV": "development"}, false},
		{"behind a tunnel or proxy", map[string]string{"APP_ENV": "development", "TRUST_PROXY": "true"}, true},
		{"staging", map[string]string{"APP_ENV": "staging"}, true},
		{"production", map[string]string{"APP_ENV": "production"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := map[string]string{"DATABASE_URL": "postgres://x", "JWT_SECRET": placeholderSecret}
			for k, v := range tt.env {
				env[k] = v
			}
			setEnv(t, env)
			_, err := Load()
			if tt.wantErr && (err == nil || !strings.Contains(err.Error(), "JWT_SECRET")) {
				t.Fatalf("want a JWT_SECRET error, got %v", err)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestLoad_RandomSecretIsAcceptedEverywhere(t *testing.T) {
	setEnv(t, map[string]string{"DATABASE_URL": "postgres://x", "JWT_SECRET": goodSecret, "APP_ENV": "production", "TRUST_PROXY": "true"})
	if _, err := Load(); err != nil {
		t.Fatal(err)
	}
}
