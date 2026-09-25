package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onerandomd3v/signa/internal/auth"
	"github.com/onerandomd3v/signa/internal/config"
	"github.com/onerandomd3v/signa/internal/incidents"
	"github.com/onerandomd3v/signa/internal/media"
	"github.com/onerandomd3v/signa/internal/reports"
)

// NewHandler builds the HTTP handler while keeping the endpoint compatible with net/http.
func NewHandler(logger *slog.Logger, ingestors ...reports.Ingestor) http.Handler {
	return NewHandlerWithRateLimit(logger, DefaultRateLimitConfig(), ingestors...)
}

func NewHandlerWithRateLimit(logger *slog.Logger, rateConfig RateLimitConfig, ingestors ...reports.Ingestor) http.Handler {
	return NewHandlerWithCORS(logger, rateConfig, defaultCORSOrigins, ingestors...)
}

func NewHandlerWithMedia(logger *slog.Logger, rateConfig RateLimitConfig, pool *pgxpool.Pool, storage media.Storage, ingestors ...reports.Ingestor) http.Handler {
	return NewHandlerWithMediaAndCORS(logger, rateConfig, defaultCORSOrigins, pool, storage, ingestors...)
}

func NewHandlerWithCORS(logger *slog.Logger, rateConfig RateLimitConfig, allowedOrigins []string, ingestors ...reports.Ingestor) http.Handler {
	return NewHandlerWithMediaAndCORS(logger, rateConfig, allowedOrigins, nil, nil, ingestors...)
}

func NewHandlerWithMediaAndCORS(logger *slog.Logger, rateConfig RateLimitConfig, allowedOrigins []string, pool *pgxpool.Pool, storage media.Storage, ingestors ...reports.Ingestor) http.Handler {
	return NewHandlerWithMediaAndCORSAndPublicIncidents(logger, rateConfig, allowedOrigins, pool, storage, nil, config.PublicIncidentGeometryPolicy{}, ingestors...)
}

func NewHandlerWithMediaAndCORSAndPublicIncidents(logger *slog.Logger, rateConfig RateLimitConfig, allowedOrigins []string, pool *pgxpool.Pool, storage media.Storage, publicReader incidents.PublicIncidentReader, publicPolicy config.PublicIncidentGeometryPolicy, ingestors ...reports.Ingestor) http.Handler {
	return NewHandlerWithMediaAndCORSAndPublicIncidentsAndAuthAndRealtime(logger, rateConfig, allowedOrigins, pool, storage, publicReader, publicPolicy, AuthConfig{}, nil, ingestors...)
}

type AuthConfig struct {
	Store auth.SessionStore
}

func NewHandlerWithMediaAndCORSAndPublicIncidentsAndAuth(logger *slog.Logger, rateConfig RateLimitConfig, allowedOrigins []string, pool *pgxpool.Pool, storage media.Storage, publicReader incidents.PublicIncidentReader, publicPolicy config.PublicIncidentGeometryPolicy, authConfig AuthConfig, ingestors ...reports.Ingestor) http.Handler {
	return NewHandlerWithMediaAndCORSAndPublicIncidentsAndAuthAndRealtime(logger, rateConfig, allowedOrigins, pool, storage, publicReader, publicPolicy, authConfig, nil, ingestors...)
}

func NewHandlerWithMediaAndCORSAndPublicIncidentsAndAuthAndRealtime(logger *slog.Logger, rateConfig RateLimitConfig, allowedOrigins []string, pool *pgxpool.Pool, storage media.Storage, publicReader incidents.PublicIncidentReader, publicPolicy config.PublicIncidentGeometryPolicy, authConfig AuthConfig, realtimeHandler http.Handler, ingestors ...reports.Ingestor) http.Handler {
	var ingestor reports.Ingestor
	if len(ingestors) > 0 {
		ingestor = ingestors[0]
	}
	router := chi.NewRouter()
	router.Use(newCORSMiddleware(allowedOrigins).Middleware)
	router.Get("/healthz", healthHandler(logger))
	router.With(NewRateLimiter(rateConfig, RateLimiterOptions{}).Middleware).Post("/reports", reportIngestHandler(logger, ingestor))
	mediaLimiter := NewRateLimiter(rateConfig, RateLimiterOptions{})
	handler := &mediaHandler{logger: logger, pool: pool, storage: storage}
	router.With(mediaLimiter.Middleware).Post("/reports/{report_id}/media/uploads", handler.authorizeUpload)
	router.With(mediaLimiter.Middleware).Post("/reports/{report_id}/media", handler.confirmUpload)
	if publicReader != nil {
		router.Get("/incidents", publicIncidentListHandler(logger, publicReader, publicPolicy))
		router.Get("/incidents/{incident_id}", publicIncidentDetailHandler(logger, publicReader, publicPolicy))
	}
	if authConfig.Store != nil {
		principalMiddleware := auth.RequirePrincipalWithLogger(authConfig.Store, logger)
		router.With(principalMiddleware).Get("/auth/session", currentSessionHandler)
		if realtimeHandler != nil {
			router.With(principalMiddleware).Get("/events", realtimeHandler.ServeHTTP)
		}
	}
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
	return NewServerWithMediaAndCORS(addr, logger, rateConfig, defaultCORSOrigins, pool, storage, ingestors...)
}

func NewServerWithMediaAndCORS(addr string, logger *slog.Logger, rateConfig RateLimitConfig, allowedOrigins []string, pool *pgxpool.Pool, storage media.Storage, ingestors ...reports.Ingestor) *http.Server {
	return NewServerWithMediaAndCORSAndPublicIncidents(addr, logger, rateConfig, allowedOrigins, pool, storage, nil, config.PublicIncidentGeometryPolicy{}, ingestors...)
}

func NewServerWithMediaAndCORSAndPublicIncidents(addr string, logger *slog.Logger, rateConfig RateLimitConfig, allowedOrigins []string, pool *pgxpool.Pool, storage media.Storage, publicReader incidents.PublicIncidentReader, publicPolicy config.PublicIncidentGeometryPolicy, ingestors ...reports.Ingestor) *http.Server {
	return NewServerWithMediaAndCORSAndPublicIncidentsAndAuthAndRealtime(addr, logger, rateConfig, allowedOrigins, pool, storage, publicReader, publicPolicy, AuthConfig{}, nil, ingestors...)
}

func NewServerWithMediaAndCORSAndPublicIncidentsAndAuth(addr string, logger *slog.Logger, rateConfig RateLimitConfig, allowedOrigins []string, pool *pgxpool.Pool, storage media.Storage, publicReader incidents.PublicIncidentReader, publicPolicy config.PublicIncidentGeometryPolicy, authConfig AuthConfig, ingestors ...reports.Ingestor) *http.Server {
	return NewServerWithMediaAndCORSAndPublicIncidentsAndAuthAndRealtime(addr, logger, rateConfig, allowedOrigins, pool, storage, publicReader, publicPolicy, authConfig, nil, ingestors...)
}

func NewServerWithMediaAndCORSAndPublicIncidentsAndAuthAndRealtime(addr string, logger *slog.Logger, rateConfig RateLimitConfig, allowedOrigins []string, pool *pgxpool.Pool, storage media.Storage, publicReader incidents.PublicIncidentReader, publicPolicy config.PublicIncidentGeometryPolicy, authConfig AuthConfig, realtimeHandler http.Handler, ingestors ...reports.Ingestor) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           NewHandlerWithMediaAndCORSAndPublicIncidentsAndAuthAndRealtime(logger, rateConfig, allowedOrigins, pool, storage, publicReader, publicPolicy, authConfig, realtimeHandler, ingestors...),
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

func currentSessionHandler(writer http.ResponseWriter, request *http.Request) {
	principal, ok := auth.PrincipalFromContext(request.Context())
	if !ok {
		http.Error(writer, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(map[string]string{"user_id": principal.UserID.String()})
}
