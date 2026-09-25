package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onerandomd3v/signa/internal/priority"
)

var (
	ErrIneligible              = errors.New("alert is not eligible")
	ErrSupersedesNotFound      = errors.New("superseded alert was not found")
	ErrSupersedesOtherIncident = errors.New("superseded alert belongs to another incident")
)

type CreateRequest struct {
	IncidentID        string
	Eligibility       EligibilityInput
	Message           string
	AsOf              time.Time
	IdempotencyKey    string
	SupersedesAlertID *string
}

type Alert struct {
	ID                       string
	IncidentID               string
	AlertType                AlertType
	ConfidenceSnapshot       string
	SeveritySnapshot         string
	StatusSnapshot           string
	PrioritySnapshot         priority.Level
	FreshnessSnapshot        FreshnessState
	Message                  string
	EligibilityPolicyVersion string
	EligibilityReasons       []string
	AsOf                     time.Time
	SupersedesAlertID        *string
	IdempotencyKey           string
	CreatedAt                time.Time
}

type CreateResult struct {
	Alert    Alert
	Decision Decision
	Reused   bool
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) Create(ctx context.Context, request CreateRequest) (CreateResult, error) {
	if s == nil || s.pool == nil {
		return CreateResult{}, errors.New("alerts database is required")
	}
	if strings.TrimSpace(request.IncidentID) == "" || strings.TrimSpace(request.IdempotencyKey) == "" {
		return CreateResult{}, fmt.Errorf("incident ID and idempotency key are required")
	}
	if strings.TrimSpace(request.Message) == "" {
		return CreateResult{}, fmt.Errorf("alert message is required after safe-summary validation")
	}
	if request.AsOf.IsZero() {
		return CreateResult{}, fmt.Errorf("alert as_of is required")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CreateResult{}, fmt.Errorf("begin alert transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if existing, err := loadByIdempotencyKey(ctx, tx, request.IdempotencyKey); err != nil {
		return CreateResult{}, err
	} else if existing != nil {
		return CreateResult{Alert: *existing, Decision: Decision{Eligible: true, AlertType: existing.AlertType, Reasons: []string{"idempotent_reuse"}, PolicyVersion: EligibilityPolicyVersion}, Reused: true}, nil
	}

	decision, err := EvaluateEligibility(request.Eligibility)
	if err != nil {
		return CreateResult{}, err
	}
	if !decision.Eligible {
		return CreateResult{Decision: decision}, ErrIneligible
	}
	if request.SupersedesAlertID != nil {
		if err := validateSupersession(ctx, tx, request.IncidentID, *request.SupersedesAlertID); err != nil {
			return CreateResult{Decision: decision}, err
		}
	}

	reasons, err := json.Marshal(decision.Reasons)
	if err != nil {
		return CreateResult{}, fmt.Errorf("marshal alert eligibility reasons: %w", err)
	}
	var alert Alert
	var supersedes any
	if request.SupersedesAlertID != nil {
		supersedes = *request.SupersedesAlertID
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO alerts (
			incident_id, alert_type, confidence_snapshot, severity_snapshot,
			status_snapshot, priority_snapshot, freshness_snapshot, message,
			eligibility_policy_version, eligibility_reasons, as_of,
			supersedes_alert_id, idempotency_key
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb, $11, $12, $13)
		ON CONFLICT (idempotency_key) DO NOTHING
		RETURNING id::text, created_at
	`, request.IncidentID, decision.AlertType, request.Eligibility.Confidence, request.Eligibility.Severity,
		request.Eligibility.Status, request.Eligibility.Priority, request.Eligibility.Freshness, request.Message,
		EligibilityPolicyVersion, reasons, request.AsOf.UTC(), supersedes, request.IdempotencyKey).Scan(&alert.ID, &alert.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		existing, loadErr := loadByIdempotencyKey(ctx, tx, request.IdempotencyKey)
		if loadErr != nil {
			return CreateResult{}, loadErr
		}
		if existing == nil {
			return CreateResult{}, fmt.Errorf("idempotent alert insert returned no canonical row")
		}
		return CreateResult{Alert: *existing, Decision: Decision{Eligible: true, AlertType: existing.AlertType, Reasons: []string{"idempotent_reuse"}, PolicyVersion: EligibilityPolicyVersion}, Reused: true}, nil
	}
	if err != nil {
		return CreateResult{}, fmt.Errorf("insert alert: %w", err)
	}
	alert.IncidentID = request.IncidentID
	alert.AlertType = decision.AlertType
	alert.ConfidenceSnapshot = request.Eligibility.Confidence
	alert.SeveritySnapshot = request.Eligibility.Severity
	alert.StatusSnapshot = request.Eligibility.Status
	alert.PrioritySnapshot = request.Eligibility.Priority
	alert.FreshnessSnapshot = request.Eligibility.Freshness
	alert.Message = request.Message
	alert.EligibilityPolicyVersion = EligibilityPolicyVersion
	alert.EligibilityReasons = append([]string(nil), decision.Reasons...)
	alert.AsOf = request.AsOf.UTC()
	alert.SupersedesAlertID = request.SupersedesAlertID
	alert.IdempotencyKey = request.IdempotencyKey

	payload, err := json.Marshal(struct {
		AlertID            string         `json:"alert_id"`
		IncidentID         string         `json:"incident_id"`
		AlertType          AlertType      `json:"alert_type"`
		ConfidenceSnapshot string         `json:"confidence_snapshot"`
		SeveritySnapshot   string         `json:"severity_snapshot"`
		PrioritySnapshot   priority.Level `json:"priority_snapshot"`
		CreatedAt          time.Time      `json:"created_at"`
	}{alert.ID, alert.IncidentID, alert.AlertType, alert.ConfidenceSnapshot, alert.SeveritySnapshot, alert.PrioritySnapshot, alert.CreatedAt.UTC()})
	if err != nil {
		return CreateResult{}, fmt.Errorf("marshal alert event: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO outbox_events (event_type, aggregate_type, aggregate_id, payload)
		VALUES ('alert.created', 'alert', $1, $2::jsonb)
	`, alert.ID, payload); err != nil {
		return CreateResult{}, fmt.Errorf("insert alert outbox event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return CreateResult{}, fmt.Errorf("commit alert transaction: %w", err)
	}
	return CreateResult{Alert: alert, Decision: decision}, nil
}

func loadByIdempotencyKey(ctx context.Context, tx pgx.Tx, key string) (*Alert, error) {
	var alert Alert
	var reasons []byte
	var supersedes *string
	err := tx.QueryRow(ctx, `
		SELECT id::text, incident_id::text, alert_type, confidence_snapshot,
		       severity_snapshot, status_snapshot, priority_snapshot,
		       freshness_snapshot, message, eligibility_policy_version,
		       eligibility_reasons, as_of, supersedes_alert_id::text,
		       idempotency_key, created_at
		FROM alerts WHERE idempotency_key = $1
	`, key).Scan(&alert.ID, &alert.IncidentID, &alert.AlertType, &alert.ConfidenceSnapshot,
		&alert.SeveritySnapshot, &alert.StatusSnapshot, &alert.PrioritySnapshot,
		&alert.FreshnessSnapshot, &alert.Message, &alert.EligibilityPolicyVersion,
		&reasons, &alert.AsOf, &supersedes, &alert.IdempotencyKey, &alert.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load idempotent alert: %w", err)
	}
	if err := json.Unmarshal(reasons, &alert.EligibilityReasons); err != nil {
		return nil, fmt.Errorf("decode alert eligibility reasons: %w", err)
	}
	alert.SupersedesAlertID = supersedes
	return &alert, nil
}

func validateSupersession(ctx context.Context, tx pgx.Tx, incidentID, supersedesID string) error {
	var supersedesIncidentID string
	err := tx.QueryRow(ctx, `SELECT incident_id::text FROM alerts WHERE id = $1 FOR SHARE`, supersedesID).Scan(&supersedesIncidentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrSupersedesNotFound
	}
	if err != nil {
		return fmt.Errorf("load superseded alert: %w", err)
	}
	if supersedesIncidentID != incidentID {
		return ErrSupersedesOtherIncident
	}
	return nil
}
