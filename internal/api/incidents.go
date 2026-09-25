package api

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/onerandomd3v/signa/internal/config"
	"github.com/onerandomd3v/signa/internal/incidents"
)

func publicIncidentListHandler(logger *slog.Logger, reader incidents.PublicIncidentReader, policy config.PublicIncidentGeometryPolicy) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		items, err := reader.ListPublicIncidents(request.Context(), policy)
		if err != nil {
			if logger != nil {
				logger.Error("list public incidents failed", "error", err)
			}
			writeError(writer, http.StatusInternalServerError, "internal_error", "incidents could not be loaded")
			return
		}
		if items == nil {
			items = []incidents.PublicIncident{}
		}
		writeJSON(writer, http.StatusOK, items)
	}
}

func publicIncidentDetailHandler(logger *slog.Logger, reader incidents.PublicIncidentReader, policy config.PublicIncidentGeometryPolicy) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		incidentID, err := uuid.Parse(chi.URLParam(request, "incident_id"))
		if err != nil {
			writeError(writer, http.StatusBadRequest, "invalid_incident_id", "incident_id must be a UUID")
			return
		}
		item, err := reader.GetPublicIncident(request.Context(), incidentID, policy)
		if errors.Is(err, incidents.ErrPublicIncidentNotFound) {
			writeError(writer, http.StatusNotFound, "incident_not_found", "incident was not found")
			return
		}
		if err != nil {
			if logger != nil {
				logger.Error("get public incident failed", "error", err, "incident_id", incidentID)
			}
			writeError(writer, http.StatusInternalServerError, "internal_error", "incident could not be loaded")
			return
		}
		writeJSON(writer, http.StatusOK, item)
	}
}
