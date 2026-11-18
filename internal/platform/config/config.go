// Package config loads runtime configuration from environment variables (12-factor).
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
)

// Config holds all runtime settings. Add new settings here and in .env.example.
type Config struct {
	Env         string // development | staging | production
	HTTPAddr    string
	LogLevel    slog.Level
	DatabaseURL string
	RedisURL    string

	JWTSecret  string
	JWTIssuer  string
	AccessTTL  time.Duration
	RefreshTTL time.Duration

	// CORSAllowedOrigins are the exact browser origins (web app) allowed to call the API.
	CORSAllowedOrigins []string
	// TrustProxy makes rate limiting read the client IP from X-Forwarded-For. Enable only behind
	// a proxy you control (ngrok, Caddy); otherwise clients could spoof their IP.
	TrustProxy bool
}

// Load reads configuration from the environment and validates it.
func Load() (Config, error) {
	cfg := Config{
		Env:         get("APP_ENV", "development"),
		HTTPAddr:    get("HTTP_ADDR", ":8080"),
		DatabaseURL: get("DATABASE_URL", ""),
		RedisURL:    get("REDIS_URL", ""),
		JWTSecret:   get("JWT_SECRET", ""),
		JWTIssuer:   get("JWT_ISSUER", "medikmu"),
	}

	if err := cfg.LogLevel.UnmarshalText([]byte(get("LOG_LEVEL", "info"))); err != nil {
		return Config{}, fmt.Errorf("invalid LOG_LEVEL: %w", err)
	}

	var err error
	if cfg.AccessTTL, err = duration("ACCESS_TOKEN_TTL", "15m"); err != nil {
		return Config{}, err
	}
	if cfg.RefreshTTL, err = duration("REFRESH_TOKEN_TTL", "720h"); err != nil { // 30 days
		return Config{}, err
	}

	for o := range strings.SplitSeq(get("CORS_ALLOWED_ORIGINS", ""), ",") {
		if o = strings.TrimRight(strings.TrimSpace(o), "/"); o == "" {
			continue
		}
		if o == "*" || !(strings.HasPrefix(o, "http://") || strings.HasPrefix(o, "https://")) {
			return Config{}, fmt.Errorf("invalid CORS_ALLOWED_ORIGINS entry %q: use exact origins like https://app.example.com", o)
		}
		cfg.CORSAllowedOrigins = append(cfg.CORSAllowedOrigins, o)
	}
	cfg.TrustProxy = get("TRUST_PROXY", "false") == "true"

	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	if len(cfg.JWTSecret) < 32 {
		return Config{}, fmt.Errorf("JWT_SECRET must be at least 32 characters (generate: openssl rand -hex 32)")
	}
	// The placeholder in .env.example is public. Anyone who can reach the API (production, staging, or a dev
	// tunnel behind TRUST_PROXY) could forge tokens with it, so it is only accepted for plain local development.
	if (cfg.Env != "development" || cfg.TrustProxy) && isPlaceholderSecret(cfg.JWTSecret) {
		return Config{}, fmt.Errorf("JWT_SECRET is the public placeholder from .env.example; generate a real one: openssl rand -hex 32")
	}
	return cfg, nil
}

func isPlaceholderSecret(s string) bool {
	s = strings.ToLower(s)
	return strings.HasPrefix(s, "dev-only") || strings.HasPrefix(s, "change-me")
}

func get(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func duration(key, fallback string) (time.Duration, error) {
	d, err := time.ParseDuration(get(key, fallback))
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("invalid %s: must be a positive duration like 15m or 720h", key)
	}
	return d, nil
}
