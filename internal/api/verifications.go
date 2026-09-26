package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/onerandomd3v/signa/internal/auth"
	"github.com/onerandomd3v/signa/internal/incidents"
	"github.com/onerandomd3v/signa/internal/verification"
)

type verificationRequestJSON struct {
	ID           uuid.UUID                `json:"id"`
	CreatedAt    time.Time                `json:"created_at"`
	ExpiresAt    time.Time                `json:"expires_at"`
	Incident     incidents.PublicIncident `json:"incident"`
	SafetyPrompt string                   `json:"safety_prompt"`
}
type verificationResponseJSON struct {
	ID          uuid.UUID                 `json:"id"`
	RequestID   uuid.UUID                 `json:"request_id"`
	IncidentID  uuid.UUID                 `json:"incident_id"`
	Conclusion  *verification.Conclusion  `json:"conclusion"`
	Observation *verification.Observation `json:"observation"`
	CreatedAt   time.Time                 `json:"created_at"`
}

func publicVerificationRequest(view verification.RequestView) verificationRequestJSON {
	return verificationRequestJSON{ID: view.ID, CreatedAt: view.CreatedAt, ExpiresAt: view.ExpiresAt, Incident: view.Incident, SafetyPrompt: verification.SafetyPrompt}
}
func publicVerificationResponse(item verification.Response) verificationResponseJSON {
	return verificationResponseJSON{ID: item.ID, RequestID: item.RequestID, IncidentID: item.IncidentID, Conclusion: item.Conclusion, Observation: item.Observation, CreatedAt: item.CreatedAt}
}
func registerVerificationRoutes(router chi.Router, principalMiddleware func(http.Handler) http.Handler, service verification.Service) {
	router.With(principalMiddleware).Get("/v1/verifications/requests", func(w http.ResponseWriter, r *http.Request) {
		principal, ok := auth.PrincipalFromContext(r.Context())
		if !ok {
			writeError(w, 401, "unauthenticated", "authentication is required")
			return
		}
		views, err := service.ListRequests(r.Context(), principal.UserID)
		if writeVerificationServiceError(w, err) {
			return
		}
		result := make([]verificationRequestJSON, 0, len(views))
		for _, view := range views {
			result = append(result, publicVerificationRequest(view))
		}
		writeJSON(w, 200, result)
	})
	router.With(principalMiddleware).Get("/v1/verifications/requests/{request_id}", func(w http.ResponseWriter, r *http.Request) {
		principal, ok := auth.PrincipalFromContext(r.Context())
		if !ok {
			writeError(w, 401, "unauthenticated", "authentication is required")
			return
		}
		requestID, err := uuid.Parse(chi.URLParam(r, "request_id"))
		if err != nil {
			writeError(w, 400, "invalid_request", "request_id must be a valid UUID")
			return
		}
		view, err := service.GetRequest(r.Context(), principal.UserID, requestID)
		if writeVerificationServiceError(w, err) {
			return
		}
		writeJSON(w, 200, publicVerificationRequest(view))
	})
	router.With(principalMiddleware).Post("/v1/verifications/requests/{request_id}/responses", func(w http.ResponseWriter, r *http.Request) {
		principal, ok := auth.PrincipalFromContext(r.Context())
		if !ok {
			writeError(w, 401, "unauthenticated", "authentication is required")
			return
		}
		requestID, err := uuid.Parse(chi.URLParam(r, "request_id"))
		if err != nil {
			writeError(w, 400, "invalid_request", "request_id must be a valid UUID")
			return
		}
		key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
		if key == "" || len(key) > 255 {
			writeError(w, 400, "invalid_idempotency_key", "Idempotency-Key must be nonempty and at most 255 bytes")
			return
		}
		input, err := decodeVerificationResponse(w, r)
		if err != nil {
			writeError(w, 400, "invalid_request", "request body must contain one valid response object")
			return
		}
		response, created, err := service.SubmitResponse(r.Context(), principal.UserID, requestID, key, input)
		if writeVerificationServiceError(w, err) {
			return
		}
		status := 200
		if created {
			status = 201
		}
		writeJSON(w, status, publicVerificationResponse(response))
	})
}
func decodeVerificationResponse(w http.ResponseWriter, r *http.Request) (verification.ResponseInput, error) {
	var input *verification.ResponseInput
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return verification.ResponseInput{}, err
	}
	if input == nil {
		return verification.ResponseInput{}, verification.ErrInvalidResponse
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return verification.ResponseInput{}, verification.ErrInvalidResponse
	}
	if input.Conclusion == nil && input.Observation == nil {
		return verification.ResponseInput{}, verification.ErrInvalidResponse
	}
	if input.Conclusion != nil {
		switch *input.Conclusion {
		case verification.ConclusionConfirm, verification.ConclusionCannotConfirm, verification.ConclusionDispute:
		default:
			return verification.ResponseInput{}, verification.ErrInvalidResponse
		}
	}
	if input.Observation != nil {
		switch *input.Observation {
		case verification.ObservationSaw, verification.ObservationHeard:
		default:
			return verification.ResponseInput{}, verification.ErrInvalidResponse
		}
	}
	return *input, nil
}
func writeVerificationNotFound(w http.ResponseWriter) {
	writeError(w, 404, "verification_request_not_found", "verification request was not found")
}
func writeVerificationServiceError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, verification.ErrNotTrustedVerifier):
		writeError(w, 403, "forbidden", "trusted verifier access is required")
	case errors.Is(err, verification.ErrRequestNotFound):
		writeVerificationNotFound(w)
	case errors.Is(err, verification.ErrInvalidResponse):
		writeError(w, 400, "invalid_request", "request body must contain one valid response object")
	case errors.Is(err, verification.ErrIdempotencyConflict):
		writeError(w, 409, "idempotency_conflict", "Idempotency-Key was already used for a different response")
	default:
		writeError(w, 503, "verification_unavailable", "verification service is unavailable")
	}
	return true
}
