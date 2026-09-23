package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onerandomd3v/signa/internal/api"
	"github.com/onerandomd3v/signa/internal/config"
	"github.com/onerandomd3v/signa/internal/logging"
	"github.com/onerandomd3v/signa/internal/reports"
)

func main() {
	logger := logging.New()
	if err := run(context.Background(), logger); err != nil {
		logger.Error("api stopped with error", "error", err)
		os.Exit(1)
	}
}

func run(parent context.Context, logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return err
	}

	server := api.NewServer(cfg.APIAddr, logger, reports.NewStore(pool))
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("api starting", "addr", cfg.APIAddr)
		if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return err
		}
		logger.Info("api stopped", "reason", ctx.Err())
		return nil
	case err := <-serverErrors:
		return err
	}
}
