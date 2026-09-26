package verification

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/onerandomd3v/signa/internal/incidents"
)

var (
	ErrNotTrustedVerifier  = errors.New("not a trusted verifier")
	ErrRequestNotFound     = errors.New("verification request not found")
	ErrInvalidResponse     = errors.New("invalid verification response")
	ErrIdempotencyConflict = errors.New("idempotency key conflicts with an existing response")
	ErrUnavailable         = errors.New("verification unavailable")
)

type Conclusion string

const (
	ConclusionConfirm       Conclusion = "CONFIRM"
	ConclusionCannotConfirm Conclusion = "CANNOT_CONFIRM"
	ConclusionDispute       Conclusion = "DISPUTE"
)

type Observation string

const (
	ObservationSaw   Observation = "SAW"
	ObservationHeard Observation = "HEARD"
)

type ResponseInput struct {
	Conclusion  *Conclusion  `json:"conclusion"`
	Observation *Observation `json:"observation"`
}

const SafetyPrompt = "Respond only from what you already safely know or observed. Never approach an incident or unsafe area to verify it."

type RequestView struct {
	ID           uuid.UUID                `json:"id"`
	CreatedAt    time.Time                `json:"created_at"`
	ExpiresAt    time.Time                `json:"expires_at"`
	Incident     incidents.PublicIncident `json:"incident"`
	SafetyPrompt string                   `json:"safety_prompt"`
}

type Response struct {
	ID          uuid.UUID    `json:"id"`
	RequestID   uuid.UUID    `json:"request_id"`
	IncidentID  uuid.UUID    `json:"incident_id"`
	Conclusion  *Conclusion  `json:"conclusion"`
	Observation *Observation `json:"observation"`
	CreatedAt   time.Time    `json:"created_at"`
}

type Service interface {
	ListRequests(context.Context, uuid.UUID) ([]RequestView, error)
	GetRequest(context.Context, uuid.UUID, uuid.UUID) (RequestView, error)
	SubmitResponse(context.Context, uuid.UUID, uuid.UUID, string, ResponseInput) (Response, bool, error)
}

type requestRecord struct {
	ID         uuid.UUID
	IncidentID uuid.UUID
	CreatedAt  time.Time
	ExpiresAt  time.Time
}
