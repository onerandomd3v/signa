package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strings"

	"github.com/onerandomd3v/signa/internal/reports"
)

type reportRequest struct {
	RawText        string                 `json:"raw_text"`
	DeviceLocation *reportLocationRequest `json:"device_location"`
}

type reportLocationRequest struct {
	Latitude  *float64 `json:"latitude"`
	Longitude *float64 `json:"longitude"`
	Accuracy  *float64 `json:"accuracy"`
}

type reportAcknowledgement struct {
	ReportID    string `json:"report_id"`
	Status      string `json:"status"`
	SubmittedAt string `json:"submitted_at"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type errorResponse struct {
	Error errorBody `json:"error"`
}

const maxIdempotencyKeyLength = 255

func reportIngestHandler(logger *slog.Logger, ingestor reports.Ingestor) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		idempotencyKey := strings.TrimSpace(request.Header.Get("Idempotency-Key"))
		if idempotencyKey == "" {
			writeError(writer, http.StatusBadRequest, "missing_idempotency_key", "Idempotency-Key is required")
			return
		}
		if len(idempotencyKey) > maxIdempotencyKeyLength {
			writeError(writer, http.StatusBadRequest, "invalid_idempotency_key", "Idempotency-Key must be at most 255 bytes")
			return
		}

		var body reportRequest
		decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil {
			writeError(writer, http.StatusBadRequest, "invalid_request", "request body must be valid JSON")
			return
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			writeError(writer, http.StatusBadRequest, "invalid_request", "request body must contain one JSON object")
			return
		}

		input, err := validateReportRequest(body)
		if err != nil {
			writeError(writer, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		if ingestor == nil {
			writeError(writer, http.StatusInternalServerError, "internal_error", "report ingestion is unavailable")
			return
		}

		acknowledgement, err := ingestor.Ingest(request.Context(), idempotencyKey, input)
		if err != nil {
			if errors.Is(err, reports.ErrIdempotencyConflict) {
				writeError(writer, http.StatusConflict, "idempotency_conflict", "Idempotency-Key was already used for a different request")
				return
			}
			if logger != nil {
				logger.Error("report ingestion failed", "error", err)
			}
			writeError(writer, http.StatusInternalServerError, "internal_error", "report could not be accepted")
			return
		}

		writeJSON(writer, http.StatusAccepted, reportAcknowledgement{
			ReportID:    acknowledgement.ReportID,
			Status:      "accepted",
			SubmittedAt: acknowledgement.SubmittedAt,
		})
	}
}

func validateReportRequest(body reportRequest) (reports.IngestRequest, error) {
	if strings.TrimSpace(body.RawText) == "" {
		return reports.IngestRequest{}, errors.New("raw_text must be non-empty")
	}

	var location *reports.DeviceLocation
	if body.DeviceLocation != nil {
		if body.DeviceLocation.Latitude == nil || body.DeviceLocation.Longitude == nil {
			return reports.IngestRequest{}, errors.New("device_location requires latitude and longitude")
		}
		latitude := *body.DeviceLocation.Latitude
		longitude := *body.DeviceLocation.Longitude
		if math.IsNaN(latitude) || math.IsInf(latitude, 0) || latitude < -90 || latitude > 90 {
			return reports.IngestRequest{}, errors.New("latitude must be between -90 and 90")
		}
		if math.IsNaN(longitude) || math.IsInf(longitude, 0) || longitude < -180 || longitude > 180 {
			return reports.IngestRequest{}, errors.New("longitude must be between -180 and 180")
		}
		if body.DeviceLocation.Accuracy != nil && (*body.DeviceLocation.Accuracy < 0 || math.IsNaN(*body.DeviceLocation.Accuracy) || math.IsInf(*body.DeviceLocation.Accuracy, 0)) {
			return reports.IngestRequest{}, errors.New("accuracy must be non-negative")
		}
		location = &reports.DeviceLocation{
			Latitude:  latitude,
			Longitude: longitude,
			Accuracy:  body.DeviceLocation.Accuracy,
		}
	}

	return reports.IngestRequest{
		RawText:        strings.TrimSpace(body.RawText),
		DeviceLocation: location,
	}, nil
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeError(writer http.ResponseWriter, status int, code string, message string) {
	writeJSON(writer, status, errorResponse{Error: errorBody{Code: code, Message: message}})
}
