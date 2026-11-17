// Command api is the Medikmu HTTP API server.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	authhttp "github.com/sangmul/medikmu-backend/internal/auth/http"
	authservice "github.com/sangmul/medikmu-backend/internal/auth/service"
	authstore "github.com/sangmul/medikmu-backend/internal/auth/store"
	patientshttp "github.com/sangmul/medikmu-backend/internal/patients/http"
	patientsservice "github.com/sangmul/medikmu-backend/internal/patients/service"
	patientsstore "github.com/sangmul/medikmu-backend/internal/patients/store"
	"github.com/sangmul/medikmu-backend/internal/platform/audit"
	"github.com/sangmul/medikmu-backend/internal/platform/config"
	"github.com/sangmul/medikmu-backend/internal/platform/db"
	"github.com/sangmul/medikmu-backend/internal/platform/httpx"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	authSvc, err := authservice.New(authstore.New(pool), authservice.Config{
		JWTSecret:  []byte(cfg.JWTSecret),
		Issuer:     cfg.JWTIssuer,
		AccessTTL:  cfg.AccessTTL,
		RefreshTTL: cfg.RefreshTTL,
	})
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: httpx.NewRouter(cfg,
			authhttp.New(authSvc).Mount,
			patientshttp.New(
				patientsservice.New(patientsstore.New(pool), audit.NewPostgres(pool)),
				authSvc.VerifyAccessToken,
			).Mount,
		),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", cfg.HTTPAddr, "env", cfg.Env)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		slog.Info("shutting down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
