package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/onerandomd3v/signa/internal/reports"
)

// NewHandler builds the HTTP handler while keeping the endpoint compatible with net/http.
func NewHandler(logger *slog.Logger, ingestors ...reports.Ingestor) http.Handler {
	var ingestor reports.Ingestor
	if len(ingestors) > 0 {
		ingestor = ingestors[0]
	}
	router := chi.NewRouter()
	router.Get("/healthz", healthHandler(logger))
	router.Post("/reports", reportIngestHandler(ingestor))
	return router
}

// NewServer creates the API server with its foundation routes.
func NewServer(addr string, logger *slog.Logger, ingestors ...reports.Ingestor) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           NewHandler(logger, ingestors...),
		ReadHeaderTimeout: 5 * time.Second,
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
