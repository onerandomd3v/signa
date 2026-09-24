package incidents

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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
	pool   *pgxpool.Pool
	policy confidence.Policy
}

func NewEvidencePolicyProcessor(pool *pgxpool.Pool, policy confidence.Policy) (*EvidencePolicyProcessor, error) {
	if pool == nil {
		return nil, fmt.Errorf("confidence policy database is required")
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	return &EvidencePolicyProcessor{pool: pool, policy: policy}, nil
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
	evidence, err := loadPolicyEvidence(ctx, tx, incidentID)
	if err != nil {
		return err
	}
	result, err := confidence.Evaluate(p.policy, evidence)
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
	ObservedAt         *time.Time
	SubmittedAt        time.Time
	Extraction         extraction.Extraction
	IndependenceState  *string
	CoordinationState  *string
	ContradictionState *string
}

func loadPolicyEvidence(ctx context.Context, tx pgx.Tx, incidentID string) ([]confidence.Evidence, error) {
	rows, err := tx.Query(ctx, `
		SELECT r.id::text, r.raw_text, r.observed_at, r.submitted_at,
		       e.result, ir.independence_state, ir.coordination_state, ir.contradiction_state
		FROM incident_reports AS ir
		JOIN reports AS r ON r.id = ir.report_id
		LEFT JOIN report_ai_extractions AS e
		  ON e.report_id = r.id AND e.contract_version = $2
		WHERE ir.incident_id = $1
		ORDER BY ir.attached_at, ir.report_id`, incidentID, independence.InputContractVersion)
	if err != nil {
		return nil, fmt.Errorf("load incident evidence: %w", err)
	}
	defer rows.Close()
	items := make([]policyEvidenceRow, 0)
	for rows.Next() {
		var item policyEvidenceRow
		var encoded []byte
		if err := rows.Scan(&item.ID, &item.RawText, &item.ObservedAt, &item.SubmittedAt, &encoded, &item.IndependenceState, &item.CoordinationState, &item.ContradictionState); err != nil {
			return nil, fmt.Errorf("scan incident evidence: %w", err)
		}
		if len(encoded) > 0 {
			if err := json.Unmarshal(encoded, &item.Extraction); err != nil {
				return nil, fmt.Errorf("decode incident evidence extraction: %w", err)
			}
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate incident evidence: %w", err)
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
		}
		coordinationState := "indeterminate"
		if item.CoordinationState != nil {
			coordinationState = *item.CoordinationState
		}
		contradictionState := "indeterminate"
		if item.ContradictionState != nil {
			contradictionState = *item.ContradictionState
		}
		result = append(result, confidence.Evidence{
			ID: item.ID, IndependenceState: independenceState, CoordinationState: coordinationState,
			ContradictionState: contradictionState, SeverityStatus: item.Extraction.SeverityCandidate.Status,
			SeverityCandidate: valueOf(item.Extraction.SeverityCandidate),
		})
		observations = append(observations, independence.Observation{EvidenceID: item.ID, Text: item.RawText, SourceClaim: item.Extraction.SourceClaim})
	}
	return result, nil
}

func compareToPriorEvidence(ctx context.Context, item policyEvidenceRow, prior []independence.Observation) string {
	state := "indeterminate"
	current := independence.Observation{EvidenceID: item.ID, Text: item.RawText, SourceClaim: item.Extraction.SourceClaim}
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
