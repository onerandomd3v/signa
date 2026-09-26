package verification

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onerandomd3v/signa/internal/config"
	"github.com/onerandomd3v/signa/internal/incidents"
)

const maxRequestListLimit = 100

type repository interface {
	ActiveGrant(context.Context, uuid.UUID) (bool, error)
	ListEligible(context.Context, uuid.UUID) ([]requestRecord, error)
	GetEligible(context.Context, uuid.UUID, uuid.UUID) (requestRecord, error)
	Submit(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, string, ResponseInput, int16, [32]byte, config.PublicIncidentGeometryPolicy) (Response, bool, error)
}

type service struct {
	repo   repository
	public incidents.PublicIncidentReader
	policy config.PublicIncidentGeometryPolicy
}

// NewService constructs the verifier service using the existing public incident projection.
func NewService(pool *pgxpool.Pool, public incidents.PublicIncidentReader, policy config.PublicIncidentGeometryPolicy) Service {
	return newService(&postgresRepository{pool: pool}, public, policy)
}
func newService(repo repository, public incidents.PublicIncidentReader, policy config.PublicIncidentGeometryPolicy) Service {
	return &service{repo: repo, public: public, policy: policy}
}

func (s *service) authorize(ctx context.Context, verifierID uuid.UUID) error {
	if s == nil || s.repo == nil || verifierID == uuid.Nil {
		return ErrNotTrustedVerifier
	}
	active, err := s.repo.ActiveGrant(ctx, verifierID)
	if err != nil {
		return ErrUnavailable
	}
	if !active {
		return ErrNotTrustedVerifier
	}
	return nil
}
func (s *service) view(ctx context.Context, request requestRecord) (RequestView, error) {
	if s.public == nil {
		return RequestView{}, ErrUnavailable
	}
	incident, err := s.public.GetPublicIncident(ctx, request.IncidentID, s.policy)
	if errors.Is(err, incidents.ErrPublicIncidentNotFound) {
		return RequestView{}, ErrRequestNotFound
	}
	if err != nil {
		return RequestView{}, ErrUnavailable
	}
	if incident.ID != request.IncidentID {
		return RequestView{}, ErrRequestNotFound
	}
	return RequestView{ID: request.ID, CreatedAt: request.CreatedAt, ExpiresAt: request.ExpiresAt, Incident: incident, SafetyPrompt: SafetyPrompt}, nil
}
func (s *service) ListRequests(ctx context.Context, verifierID uuid.UUID) ([]RequestView, error) {
	if err := s.authorize(ctx, verifierID); err != nil {
		return nil, err
	}
	requests, err := s.repo.ListEligible(ctx, verifierID)
	if err != nil {
		return nil, ErrUnavailable
	}
	views := make([]RequestView, 0, len(requests))
	for _, request := range requests {
		view, err := s.view(ctx, request)
		if errors.Is(err, ErrRequestNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	return views, nil
}
func (s *service) GetRequest(ctx context.Context, verifierID, requestID uuid.UUID) (RequestView, error) {
	if err := s.authorize(ctx, verifierID); err != nil {
		return RequestView{}, err
	}
	request, err := s.repo.GetEligible(ctx, verifierID, requestID)
	if errors.Is(err, ErrRequestNotFound) {
		return RequestView{}, ErrRequestNotFound
	}
	if err != nil {
		return RequestView{}, ErrUnavailable
	}
	return s.view(ctx, request)
}
func (s *service) SubmitResponse(ctx context.Context, verifierID, requestID uuid.UUID, key string, input ResponseInput) (Response, bool, error) {
	if err := s.authorize(ctx, verifierID); err != nil {
		return Response{}, false, err
	}
	if key == "" || strings.TrimSpace(key) != key || len(key) > 255 {
		return Response{}, false, ErrInvalidResponse
	}
	version, digest, err := fingerprintResponse(input)
	if err != nil {
		return Response{}, false, ErrInvalidResponse
	}
	request, err := s.repo.GetEligible(ctx, verifierID, requestID)
	if errors.Is(err, ErrRequestNotFound) {
		return Response{}, false, ErrRequestNotFound
	}
	if err != nil {
		return Response{}, false, ErrUnavailable
	}
	if _, err = s.view(ctx, request); err != nil {
		return Response{}, false, err
	}
	response, created, err := s.repo.Submit(ctx, verifierID, requestID, request.IncidentID, key, input, version, digest, s.policy)
	if errors.Is(err, ErrNotTrustedVerifier) || errors.Is(err, ErrRequestNotFound) || errors.Is(err, ErrIdempotencyConflict) {
		return Response{}, false, err
	}
	if err != nil {
		return Response{}, false, ErrUnavailable
	}
	return response, created, nil
}
