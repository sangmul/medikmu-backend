package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestLoad_CORSAndProxy(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_URL": "postgres://x", "JWT_SECRET": goodSecret,
		"CORS_ALLOWED_ORIGINS": " http://localhost:5173 , https://web.example.com/ ,",
		"TRUST_PROXY":          "true",
	})
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"http://localhost:5173", "https://web.example.com"}
	if !reflect.DeepEqual(cfg.CORSAllowedOrigins, want) || !cfg.TrustProxy {
		t.Fatalf("got %v trust=%v", cfg.CORSAllowedOrigins, cfg.TrustProxy)
	}
}

func TestLoad_CORSDefaultsAreClosed(t *testing.T) {
	setEnv(t, map[string]string{"DATABASE_URL": "postgres://x", "JWT_SECRET": goodSecret})
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.CORSAllowedOrigins) != 0 || cfg.TrustProxy {
		t.Fatalf("defaults must be closed: %+v", cfg)
	}
}

func TestLoad_CORSRejectsWildcardsAndGarbage(t *testing.T) {
	for _, v := range []string{"*", "web.example.com", "ftp://x.test"} {
		t.Run(v, func(t *testing.T) {
			setEnv(t, map[string]string{"DATABASE_URL": "postgres://x", "JWT_SECRET": goodSecret, "CORS_ALLOWED_ORIGINS": v})
			if _, err := Load(); err == nil || !strings.Contains(err.Error(), "CORS_ALLOWED_ORIGINS") {
				t.Fatalf("want CORS error, got %v", err)
			}
		})
	}
}
