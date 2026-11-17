// Package config loads runtime configuration from environment variables (12-factor).
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// Config holds all runtime settings. Add new settings here and in .env.example.
type Config struct {
	Env         string // development | staging | production
	HTTPAddr    string
	LogLevel    slog.Level
	DatabaseURL string
	RedisURL    string
	JWTSecret   string
}

// Load reads configuration from the environment and validates it.
func Load() (Config, error) {
	cfg := Config{
		Env:         get("APP_ENV", "development"),
		HTTPAddr:    get("HTTP_ADDR", ":8080"),
		DatabaseURL: get("DATABASE_URL", ""),
		RedisURL:    get("REDIS_URL", ""),
		JWTSecret:   get("JWT_SECRET", ""),
	}

	if err := cfg.LogLevel.UnmarshalText([]byte(get("LOG_LEVEL", "info"))); err != nil {
		return Config{}, fmt.Errorf("invalid LOG_LEVEL: %w", err)
	}

	if cfg.Env == "production" {
		if len(cfg.JWTSecret) < 32 {
			return Config{}, fmt.Errorf("JWT_SECRET must be at least 32 characters in production")
		}
		if cfg.DatabaseURL == "" {
			return Config{}, fmt.Errorf("DATABASE_URL is required in production")
		}
	}
	return cfg, nil
}

func get(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
