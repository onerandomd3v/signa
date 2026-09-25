package alerts

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onerandomd3v/signa/internal/priority"
)

var ErrAlertNotVisible = errors.New("alert is not visible to this user")

// AlertRead is the immutable, browser-safe alert snapshot. It deliberately
// excludes delivery/provider state, raw evidence, reporter identity, and
// private location data.
type AlertRead struct {
	ID                 uuid.UUID      `json:"alert_id"`
	IncidentID         uuid.UUID      `json:"incident_id"`
	AlertType          AlertType      `json:"alert_type"`
	ConfidenceSnapshot string         `json:"confidence_snapshot"`
	SeveritySnapshot   string         `json:"severity_snapshot"`
	StatusSnapshot     string         `json:"status_snapshot"`
	PrioritySnapshot   priority.Level `json:"priority_snapshot"`
	FreshnessSnapshot  FreshnessState `json:"freshness_snapshot"`
	Message            string         `json:"message"`
	AsOf               time.Time      `json:"as_of"`
	CreatedAt          time.Time      `json:"created_at"`
	SupersedesAlertID  *uuid.UUID     `json:"supersedes_alert_id"`
}

type alertReadQueryRower interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// ReadAuthorized returns the durable alert snapshot only when the principal
// has a non-terminal delivery relationship for that alert. Delivery/provider
// internals are intentionally not part of this read model.
func (s *Store) ReadAuthorized(ctx context.Context, userID, alertID uuid.UUID) (AlertRead, error) {
	if s == nil || s.pool == nil {
		return AlertRead{}, errors.New("alerts database is required")
	}
	return readAuthorized(ctx, s.pool, userID, alertID)
}

func readAuthorized(ctx context.Context, db alertReadQueryRower, userID, alertID uuid.UUID) (AlertRead, error) {
	if db == nil || userID == uuid.Nil || alertID == uuid.Nil {
		return AlertRead{}, ErrAlertNotVisible
	}
	var result AlertRead
	var supersedes *uuid.UUID
	err := db.QueryRow(ctx, `
		SELECT a.id, a.incident_id, a.alert_type, a.confidence_snapshot,
		       a.severity_snapshot, a.status_snapshot, a.priority_snapshot,
		       a.freshness_snapshot, a.message, a.as_of, a.created_at,
		       a.supersedes_alert_id
		FROM alerts AS a
		JOIN deliveries AS d ON d.alert_id = a.id
		WHERE a.id = $1
		  AND d.user_id = $2
		  AND d.state NOT IN ('SKIPPED', 'QUARANTINED')
		LIMIT 1
	`, alertID, userID).Scan(
		&result.ID, &result.IncidentID, &result.AlertType,
		&result.ConfidenceSnapshot, &result.SeveritySnapshot,
		&result.StatusSnapshot, &result.PrioritySnapshot,
		&result.FreshnessSnapshot, &result.Message, &result.AsOf,
		&result.CreatedAt, &supersedes,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return AlertRead{}, ErrAlertNotVisible
	}
	if err != nil {
		return AlertRead{}, fmt.Errorf("read authorized alert: %w", err)
	}
	result.SupersedesAlertID = supersedes
	return result, nil
}
