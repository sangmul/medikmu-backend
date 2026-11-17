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

	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	if len(cfg.JWTSecret) < 32 {
		return Config{}, fmt.Errorf("JWT_SECRET must be at least 32 characters (generate: openssl rand -hex 32)")
	}
	return cfg, nil
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
