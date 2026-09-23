package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onerandomd3v/signa/internal/media"
	"github.com/onerandomd3v/signa/internal/reports"
)

// NewHandler builds the HTTP handler while keeping the endpoint compatible with net/http.
func NewHandler(logger *slog.Logger, ingestors ...reports.Ingestor) http.Handler {
	return NewHandlerWithRateLimit(logger, DefaultRateLimitConfig(), ingestors...)
}

func NewHandlerWithRateLimit(logger *slog.Logger, rateConfig RateLimitConfig, ingestors ...reports.Ingestor) http.Handler {
	return NewHandlerWithMedia(logger, rateConfig, nil, nil, ingestors...)
}

func NewHandlerWithMedia(logger *slog.Logger, rateConfig RateLimitConfig, pool *pgxpool.Pool, storage media.Storage, ingestors ...reports.Ingestor) http.Handler {
	var ingestor reports.Ingestor
	if len(ingestors) > 0 {
		ingestor = ingestors[0]
	}
	router := chi.NewRouter()
	router.Get("/healthz", healthHandler(logger))
	router.With(NewRateLimiter(rateConfig, RateLimiterOptions{}).Middleware).Post("/reports", reportIngestHandler(logger, ingestor))
	mediaLimiter := NewRateLimiter(rateConfig, RateLimiterOptions{})
	handler := &mediaHandler{logger: logger, pool: pool, storage: storage}
	router.With(mediaLimiter.Middleware).Post("/reports/{report_id}/media/uploads", handler.authorizeUpload)
	router.With(mediaLimiter.Middleware).Post("/reports/{report_id}/media", handler.confirmUpload)
	return router
}

// NewServer creates the API server with its foundation routes.
func NewServer(addr string, logger *slog.Logger, ingestors ...reports.Ingestor) *http.Server {
	return NewServerWithRateLimit(addr, logger, DefaultRateLimitConfig(), ingestors...)
}

func NewServerWithRateLimit(addr string, logger *slog.Logger, rateConfig RateLimitConfig, ingestors ...reports.Ingestor) *http.Server {
	return NewServerWithMedia(addr, logger, rateConfig, nil, nil, ingestors...)
}

func NewServerWithMedia(addr string, logger *slog.Logger, rateConfig RateLimitConfig, pool *pgxpool.Pool, storage media.Storage, ingestors ...reports.Ingestor) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           NewHandlerWithMedia(logger, rateConfig, pool, storage, ingestors...),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}

func healthHandler(logger *slog.Logger) http.HandlerFunc {
	return func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(writer).Encode(map[string]string{"status": "ok"}); err != nil && logger != nil {
			logger.Error("write health response", "error", err)
		}
	}
}
