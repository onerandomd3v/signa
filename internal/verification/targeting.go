package verification

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onerandomd3v/signa/internal/config"
	"github.com/onerandomd3v/signa/internal/incidents"
)

type targetingRepository interface {
	TargetIncident(context.Context, uuid.UUID, config.VerificationTargetingPolicy, config.PublicIncidentGeometryPolicy) (int, error)
}

// TargetingProcessor creates passive verification requests for candidates
// selected by the durable incident event stream.
type TargetingProcessor struct {
	repo         targetingRepository
	targetPolicy config.VerificationTargetingPolicy
	publicPolicy config.PublicIncidentGeometryPolicy
}

func NewTargetingProcessor(pool *pgxpool.Pool, targetPolicy config.VerificationTargetingPolicy, publicPolicy config.PublicIncidentGeometryPolicy) (*TargetingProcessor, error) {
	if pool == nil {
		return nil, fmt.Errorf("verification targeting database is required")
	}
	if err := targetPolicy.Validate(); err != nil {
		return nil, fmt.Errorf("invalid verification targeting policy")
	}
	if err := publicPolicy.Validate(); err != nil {
		return nil, fmt.Errorf("invalid verification public projection policy")
	}
	return newTargetingProcessor(&postgresTargetingRepository{pool: pool}, targetPolicy, publicPolicy), nil
}

func newTargetingProcessor(repo targetingRepository, targetPolicy config.VerificationTargetingPolicy, publicPolicy config.PublicIncidentGeometryPolicy) *TargetingProcessor {
	return &TargetingProcessor{repo: repo, targetPolicy: targetPolicy, publicPolicy: publicPolicy}
}

// Process is idempotent across redelivery. Errors are returned so the durable
// consumer leaves the triggering event pending for retry.
func (p *TargetingProcessor) Process(ctx context.Context, message incidents.StreamMessage) error {
	if p == nil || p.repo == nil {
		return fmt.Errorf("verification targeting dependencies are required")
	}
	if err := p.targetPolicy.Validate(); err != nil {
		return fmt.Errorf("verification targeting policy is invalid")
	}
	if err := p.publicPolicy.Validate(); err != nil {
		return fmt.Errorf("verification public projection policy is invalid")
	}
	incidentID, err := parseTargetingTrigger(message.Values)
	if err != nil {
		return err
	}
	_, err = p.repo.TargetIncident(ctx, incidentID, p.targetPolicy, p.publicPolicy)
	if err != nil {
		// The durable consumer logs returned errors. Do not propagate database
		// details that could contain verifier identifiers or query values.
		return fmt.Errorf("target verifier requests failed")
	}
	return nil
}

func parseTargetingTrigger(fields map[string]any) (uuid.UUID, error) {
	name, ok := fields["event_name"].(string)
	if !ok || (name != incidents.IncidentCreatedV1 && name != incidents.IncidentReportAttachedV1) {
		return uuid.Nil, fmt.Errorf("unsupported verification targeting event")
	}
	aggregate, ok := fields["aggregate_type"].(string)
	if !ok || aggregate != "incident" {
		return uuid.Nil, fmt.Errorf("verification targeting event aggregate is invalid")
	}
	payload, ok := fields["payload"].(string)
	if !ok {
		return uuid.Nil, fmt.Errorf("verification targeting event payload is missing")
	}
	var body struct {
		IncidentID string `json:"incident_id"`
	}
	if err := json.Unmarshal([]byte(payload), &body); err != nil {
		return uuid.Nil, fmt.Errorf("verification targeting event payload is invalid")
	}
	incidentID, err := uuid.Parse(body.IncidentID)
	if err != nil || incidentID == uuid.Nil {
		return uuid.Nil, fmt.Errorf("verification targeting incident identifier is invalid")
	}
	return incidentID, nil
}
