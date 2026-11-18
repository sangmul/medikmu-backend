package config

import (
	"strings"
	"testing"
	"time"
)

const goodSecret = "0123456789abcdef0123456789abcdef"

func setEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	for _, k := range []string{"DATABASE_URL", "JWT_SECRET", "ACCESS_TOKEN_TTL", "REFRESH_TOKEN_TTL", "LOG_LEVEL", "CORS_ALLOWED_ORIGINS", "TRUST_PROXY"} {
		t.Setenv(k, "")
	}
	for k, v := range kv {
		t.Setenv(k, v)
	}
}

func TestLoad_Defaults(t *testing.T) {
	setEnv(t, map[string]string{"DATABASE_URL": "postgres://x", "JWT_SECRET": goodSecret})
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AccessTTL != 15*time.Minute || cfg.RefreshTTL != 720*time.Hour || cfg.HTTPAddr != ":8080" {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}

func TestLoad_Errors(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"missing db", map[string]string{"JWT_SECRET": goodSecret}, "DATABASE_URL"},
		{"short secret", map[string]string{"DATABASE_URL": "postgres://x", "JWT_SECRET": "short"}, "JWT_SECRET"},
		{"bad ttl", map[string]string{"DATABASE_URL": "postgres://x", "JWT_SECRET": goodSecret, "ACCESS_TOKEN_TTL": "soon"}, "ACCESS_TOKEN_TTL"},
		{"bad log level", map[string]string{"DATABASE_URL": "postgres://x", "JWT_SECRET": goodSecret, "LOG_LEVEL": "loud"}, "LOG_LEVEL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setEnv(t, tt.env)
			if _, err := Load(); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("want error containing %q, got %v", tt.want, err)
			}
		})
	}
}
