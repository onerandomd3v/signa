package api

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/onerandomd3v/signa/internal/alerts"
	"github.com/onerandomd3v/signa/internal/auth"
)

func alertReadHandler(logger *slog.Logger, reader alerts.AlertReader) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		alertID, err := uuid.Parse(chi.URLParam(request, "alert_id"))
		if err != nil {
			writeError(writer, http.StatusBadRequest, "invalid_alert_id", "alert_id must be a UUID")
			return
		}
		principal, ok := auth.PrincipalFromContext(request.Context())
		if !ok || principal.UserID == uuid.Nil {
			writeError(writer, http.StatusUnauthorized, "unauthenticated", "authentication is required")
			return
		}
		item, err := reader.ReadAuthorized(request.Context(), principal.UserID, alertID)
		if errors.Is(err, alerts.ErrAlertNotVisible) {
			writeError(writer, http.StatusNotFound, "alert_not_found", "alert was not found")
			return
		}
		if err != nil {
			if logger != nil {
				logger.Error("read alert failed", "error", err, "alert_id", alertID)
			}
			writeError(writer, http.StatusServiceUnavailable, "alert_unavailable", "alert could not be loaded")
			return
		}
		writeJSON(writer, http.StatusOK, item)
	}
}
