package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onerandomd3v/signa/internal/api"
	"github.com/onerandomd3v/signa/internal/auth"
	"github.com/onerandomd3v/signa/internal/config"
	"github.com/onerandomd3v/signa/internal/incidents"
	"github.com/onerandomd3v/signa/internal/logging"
	"github.com/onerandomd3v/signa/internal/media"
	platformredis "github.com/onerandomd3v/signa/internal/platform/redis"
	"github.com/onerandomd3v/signa/internal/reports"
	"github.com/onerandomd3v/signa/internal/realtime"
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
	publicGeometryPolicy, err := config.LoadPublicIncidentGeometryPolicy()
	if err != nil {
		return fmt.Errorf("load public incident geometry policy: %w", err)
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
	redisClient, err := platformredis.Connect(ctx, cfg.RedisAddr)
	if err != nil {
		logger.Warn("redis unavailable; realtime events are disabled until restart", "error", err)
	} else {
		defer func() { _ = redisClient.Close() }()
	}

	var storage media.Storage
	if cfg.ObjectStorageEndpoint != "" || cfg.ObjectStorageBucket != "" || cfg.ObjectStorageAccessKeyID != "" || cfg.ObjectStorageSecret != "" {
		storage, err = media.NewConfiguredS3Storage(ctx, media.S3Config{
			Endpoint: cfg.ObjectStorageEndpoint, Region: cfg.ObjectStorageRegion, Bucket: cfg.ObjectStorageBucket,
			AccessKeyID: cfg.ObjectStorageAccessKeyID, SecretAccessKey: cfg.ObjectStorageSecret,
		})
		if err != nil && !errors.Is(err, media.ErrStorageUnavailable) {
			return err
		}
		if errors.Is(err, media.ErrStorageUnavailable) {
			logger.Warn("media storage is not fully configured; media uploads are disabled")
			storage = nil
		}
	}

	var realtimeSource realtime.Source
	if redisClient != nil {
		realtimeSource = &realtime.RedisSource{Client: redisClient, Block: cfg.SSEHeartbeatInterval}
	}
	sseHandler := realtime.NewHandlerWithContext(ctx, realtimeSource, realtime.ScopeAuthorizer{}, cfg.SSEHeartbeatInterval)
	sessionStore := auth.NewPostgresStore(pool)
	server := api.NewServerWithMediaAndCORSAndPublicIncidentsAndAuthAndRealtime(cfg.APIAddr, logger, api.RateLimitConfig{
		PerClientRatePerMinute: cfg.ReportRatePerMinute,
		PerClientBurst:         cfg.ReportRateBurst,
		GlobalRatePerMinute:    cfg.GlobalReportRatePerMinute,
		GlobalBurst:            cfg.GlobalReportRateBurst,
	}, cfg.WebAllowedOrigins, pool, storage, incidents.NewStore(pool), publicGeometryPolicy, api.AuthConfig{
		Store: sessionStore,
	}, sseHandler, reports.NewStore(pool))
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
