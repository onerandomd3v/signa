package incidents

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onerandomd3v/signa/internal/ai/coordination"
	"github.com/onerandomd3v/signa/internal/ai/extraction"
	"github.com/onerandomd3v/signa/internal/ai/independence"
	"github.com/onerandomd3v/signa/internal/incidents/confidence"
)

const (
	IncidentCreatedV1           = "incident.created.v1"
	IncidentReportAttachedV1    = "incident.report_attached.v1"
	IncidentConfidenceChangedV1 = "incident.confidence_changed.v1"
	IncidentSeverityChangedV1   = "incident.severity_changed.v1"
	IncidentConfidenceChanged   = "incident.confidence_changed"
	IncidentSeverityChanged     = "incident.severity_changed"
)

type EvidencePolicyProcessor struct {
	pool                   *pgxpool.Pool
	policy                 confidence.Policy
	coordinationSyncWindow time.Duration
}

func NewEvidencePolicyProcessor(pool *pgxpool.Pool, policy confidence.Policy, coordinationSyncWindow time.Duration) (*EvidencePolicyProcessor, error) {
	if pool == nil {
		return nil, fmt.Errorf("confidence policy database is required")
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	if coordinationSyncWindow <= 0 {
		return nil, fmt.Errorf("coordination synchronization window must be greater than zero")
	}
	return &EvidencePolicyProcessor{pool: pool, policy: policy, coordinationSyncWindow: coordinationSyncWindow}, nil
}

func (p *EvidencePolicyProcessor) Process(ctx context.Context, message StreamMessage) error {
	incidentID, process, err := parseIncidentEvidenceTrigger(message.Values)
	if err != nil {
		return err
	}
	if !process {
		return nil
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin confidence evaluation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var previousConfidence string
	var previousSeverity *string
	if err := tx.QueryRow(ctx, `SELECT confidence_state, severity FROM incidents WHERE id = $1 FOR UPDATE`, incidentID).Scan(&previousConfidence, &previousSeverity); err != nil {
		return fmt.Errorf("lock incident %s for confidence evaluation: %w", incidentID, err)
	}
	evidence, observations, err := loadPolicyEvidence(ctx, tx, incidentID)
	if err != nil {
		return err
	}
	coordinationState := evaluateCoordination(ctx, observations, p.coordinationSyncWindow)
	result, err := confidence.EvaluateWithCoordination(p.policy, evidence, coordinationState)
	if err != nil {
		return fmt.Errorf("evaluate incident %s evidence: %w", incidentID, err)
	}
	metadata, err := json.Marshal(map[string]any{
		"attached_report_count":          result.AttachedReportCount,
		"effective_independent_evidence": result.EffectiveIndependentCount,
		"repeated_evidence_count":        result.RepeatedEvidenceCount,
		"contradiction_state":            result.ContradictionState,
		"coordination_state":             result.CoordinationState,
	})
	if err != nil {
		return err
	}

	if previousConfidence != string(result.ConfidenceState) {
		if _, err := tx.Exec(ctx, `UPDATE incidents SET confidence_state = $2, updated_at = now() WHERE id = $1`, incidentID, result.ConfidenceState); err != nil {
			return fmt.Errorf("update incident confidence: %w", err)
		}
		if err := insertPolicyTransition(ctx, tx, incidentID, "confidence_state", &previousConfidence, string(result.ConfidenceState), confidenceReason(result), metadata, IncidentConfidenceChanged); err != nil {
			return err
		}
	}

	if result.Severity != nil && !sameSeverity(previousSeverity, result.Severity) {
		var next any
		var nextValue string
		if result.Severity != nil {
			nextValue = string(*result.Severity)
			next = nextValue
		}
		if _, err := tx.Exec(ctx, `UPDATE incidents SET severity = $2, updated_at = now() WHERE id = $1`, incidentID, next); err != nil {
			return fmt.Errorf("update incident severity: %w", err)
		}
		var previousValue *string
		if previousSeverity != nil {
			previousValue = previousSeverity
		}
		if err := insertPolicyTransition(ctx, tx, incidentID, "severity", previousValue, nextValue, severityReason(result), metadata, IncidentSeverityChanged); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit confidence evaluation: %w", err)
	}
	return nil
}

func parseIncidentEvidenceTrigger(fields map[string]any) (string, bool, error) {
	name, ok := streamString(fields, "event_name")
	if !ok {
		return "", false, fmt.Errorf("confidence event missing event_name")
	}
	if name != IncidentCreatedV1 && name != IncidentReportAttachedV1 {
		if name == IncidentConfidenceChangedV1 || name == IncidentSeverityChangedV1 {
			return "", false, nil
		}
		return "", false, fmt.Errorf("unsupported confidence input event %q", name)
	}
	aggregate, ok := streamString(fields, "aggregate_type")
	if !ok || aggregate != "incident" {
		return "", false, fmt.Errorf("confidence input aggregate_type must be incident")
	}
	payload, ok := streamString(fields, "payload")
	if !ok {
		return "", false, fmt.Errorf("confidence event missing payload")
	}
	var body struct {
		IncidentID string `json:"incident_id"`
	}
	if err := json.Unmarshal([]byte(payload), &body); err != nil {
		return "", false, fmt.Errorf("decode confidence input payload: %w", err)
	}
	if strings.TrimSpace(body.IncidentID) == "" {
		return "", false, fmt.Errorf("confidence input payload missing incident_id")
	}
	return body.IncidentID, true, nil
}

type policyEvidenceRow struct {
	ID                 string
	RawText            string
	ReporterID         *string
	ObservedAt         *time.Time
	SubmittedAt        time.Time
	Extraction         extraction.Extraction
	IndependenceState  *string
	ContradictionState *string
}

func loadPolicyEvidence(ctx context.Context, tx pgx.Tx, incidentID string) ([]confidence.Evidence, []coordination.Observation, error) {
	rows, err := tx.Query(ctx, `
		SELECT r.id::text, r.raw_text, r.reporter_id::text, r.observed_at, r.submitted_at,
		       e.result, ir.independence_state, ir.contradiction_state
		FROM incident_reports AS ir
		JOIN reports AS r ON r.id = ir.report_id
		LEFT JOIN report_ai_extractions AS e
		  ON e.report_id = r.id AND e.contract_version = $2
		WHERE ir.incident_id = $1
		ORDER BY ir.attached_at, ir.report_id`, incidentID, independence.InputContractVersion)
	if err != nil {
		return nil, nil, fmt.Errorf("load incident evidence: %w", err)
	}
	defer rows.Close()
	items := make([]policyEvidenceRow, 0)
	for rows.Next() {
		var item policyEvidenceRow
		var encoded []byte
		if err := rows.Scan(&item.ID, &item.RawText, &item.ReporterID, &item.ObservedAt, &item.SubmittedAt, &encoded, &item.IndependenceState, &item.ContradictionState); err != nil {
			return nil, nil, fmt.Errorf("scan incident evidence: %w", err)
		}
		if len(encoded) > 0 {
			if err := json.Unmarshal(encoded, &item.Extraction); err != nil {
				return nil, nil, fmt.Errorf("decode incident evidence extraction: %w", err)
			}
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate incident evidence: %w", err)
	}

	result := make([]confidence.Evidence, 0, len(items))
	observations := make([]independence.Observation, 0, len(items))
	for index, item := range items {
		var independenceState string
		if item.IndependenceState != nil {
			independenceState = *item.IndependenceState
		} else if index == 0 {
			independenceState = "first_evidence"
		} else {
			independenceState = compareToPriorEvidence(ctx, item, observations)
			if _, err := tx.Exec(ctx, `UPDATE incident_reports SET independence_state = $3 WHERE incident_id = $1 AND report_id = $2 AND independence_state IS NULL`, incidentID, item.ID, independenceState); err != nil {
				return nil, nil, fmt.Errorf("persist independence state for report %s: %w", item.ID, err)
			}
		}
		contradictionState := "indeterminate"
		if item.ContradictionState != nil {
			contradictionState = *item.ContradictionState
		}
		result = append(result, confidence.Evidence{
			ID: item.ID, IndependenceState: independenceState,
			ContradictionState: contradictionState, SeverityStatus: item.Extraction.SeverityCandidate.Status,
			SeverityCandidate: valueOf(item.Extraction.SeverityCandidate),
		})
		reporterID := ""
		if item.ReporterID != nil {
			reporterID = *item.ReporterID
		}
		observation := independence.Observation{EvidenceID: item.ID, Text: item.RawText, SourceClaim: item.Extraction.SourceClaim}
		if reporterID != "" {
			observation.SourceOrigin = &reporterID
		}
		observations = append(observations, observation)
	}
	return result, buildCoordinationObservations(items, observations), nil
}

func buildCoordinationObservations(items []policyEvidenceRow, observations []independence.Observation) []coordination.Observation {
	result := make([]coordination.Observation, 0, len(observations))
	for index, observation := range observations {
		stamp := items[index].SubmittedAt.UTC()
		result = append(result, coordination.Observation{Evidence: observation, SubmittedAt: &stamp})
	}
	return result
}

func evaluateCoordination(ctx context.Context, observations []coordination.Observation, syncWindow time.Duration) string {
	if len(observations) < 2 || syncWindow < time.Second {
		return "indeterminate"
	}
	seconds := int64(syncWindow / time.Second)
	config := coordination.Config{ConfigVersion: coordination.ConfigVersion, SynchronizationWindowSeconds: seconds}
	assessment, err := (coordination.RuleBasedEvaluator{}).Assess(ctx, coordination.Input{Observations: observations}, config)
	if err != nil {
		return "indeterminate"
	}
	return assessment.CoordinationState
}

func compareToPriorEvidence(ctx context.Context, item policyEvidenceRow, prior []independence.Observation) string {
	state := "indeterminate"
	current := independence.Observation{EvidenceID: item.ID, Text: item.RawText, SourceClaim: item.Extraction.SourceClaim}
	if item.ReporterID != nil && *item.ReporterID != "" {
		reporterID := *item.ReporterID
		current.SourceOrigin = &reporterID
	}
	for _, previous := range prior {
		data, err := (independence.RuleBasedEvaluator{}).Assess(ctx, independence.Input{Target: current, Related: previous})
		if err != nil {
			continue
		}
		var assessment independence.Assessment
		if json.Unmarshal(data, &assessment) != nil {
			continue
		}
		switch assessment.IndependenceState {
		case "repetition_risk":
			return "repetition_risk"
		case "independence_supported":
			state = "independence_supported"
		case "mixed_signals":
			if state != "independence_supported" {
				state = "mixed_signals"
			}
		}
	}
	return state
}

func valueOf(field extraction.Field) string {
	if field.Value == nil {
		return ""
	}
	return *field.Value
}

func sameSeverity(previous *string, next *confidence.Severity) bool {
	if previous == nil && next == nil {
		return true
	}
	if previous == nil || next == nil {
		return false
	}
	return *previous == string(*next)
}

func confidenceReason(result confidence.Result) string {
	return fmt.Sprintf("effective independent evidence count %d of %d; repeated evidence %d; policy %s", result.EffectiveIndependentCount, result.AttachedReportCount, result.RepeatedEvidenceCount, confidence.PolicyVersion)
}

func severityReason(result confidence.Result) string {
	if result.Severity == nil {
		return fmt.Sprintf("no identified severity candidate; policy %s", confidence.PolicyVersion)
	}
	return fmt.Sprintf("highest identified severity candidate is %s; policy %s", *result.Severity, confidence.PolicyVersion)
}

func insertPolicyTransition(ctx context.Context, tx pgx.Tx, incidentID, field string, previous *string, next, reason string, metadata []byte, eventType string) error {
	if _, err := tx.Exec(ctx, `INSERT INTO incident_state_history (incident_id, changed_field, previous_value, new_value, reason, actor_type, rule_version, metadata) VALUES ($1, $2, $3, $4, $5, 'system', $6, $7::jsonb)`, incidentID, field, previous, next, reason, confidence.PolicyVersion, metadata); err != nil {
		return fmt.Errorf("write %s history: %w", field, err)
	}
	payload, err := json.Marshal(map[string]any{
		"incident_id": incidentID, "changed_field": field, "previous_value": previous,
		"new_value": next, "policy_version": confidence.PolicyVersion,
		"metadata": json.RawMessage(metadata),
	})
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO outbox_events (event_type, aggregate_type, aggregate_id, payload) VALUES ($1, 'incident', $2, $3::jsonb)`, eventType, incidentID, payload); err != nil {
		return fmt.Errorf("write %s outbox event: %w", field, err)
	}
	return nil
}
