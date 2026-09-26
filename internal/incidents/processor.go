package incidents

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onerandomd3v/signa/internal/ai/extraction"
	"github.com/onerandomd3v/signa/internal/ai/location"
	"github.com/onerandomd3v/signa/internal/ai/similarity"
	"github.com/onerandomd3v/signa/internal/config"
	"github.com/onerandomd3v/signa/internal/observability"
)

const ReportAIProcessedV1 = "report.ai_processed.v1"

type StreamMessage = extraction.StreamMessage
type StreamClient = extraction.StreamClient

type SimilarityAssessor interface {
	Assess(context.Context, similarity.ReportEvidence, similarity.CandidateIncident) ([]byte, error)
}

type Processor struct {
	pool       *pgxpool.Pool
	lookup     *Store
	similarity SimilarityAssessor
	validator  *similarity.Validator
	policy     config.IncidentPolicy
	now        func() time.Time
}

func NewProcessor(pool *pgxpool.Pool, lookup *Store, assessor SimilarityAssessor, validator *similarity.Validator, policy config.IncidentPolicy) *Processor {
	return &Processor{pool: pool, lookup: lookup, similarity: assessor, validator: validator, policy: policy, now: time.Now}
}

func (p *Processor) Process(ctx context.Context, message StreamMessage) (err error) {
	ctx, finish := observability.StartStage(ctx, observability.StageIncidentProcessing)
	defer func() { finish(err) }()
	if p == nil || p.pool == nil || p.lookup == nil || p.similarity == nil {
		return fmt.Errorf("incident processor dependencies are required")
	}
	event, err := parseAIProcessed(message.Values)
	if err != nil {
		return err
	}
	if message.ID == "" {
		return fmt.Errorf("incident stream message id is required")
	}
	report, result, err := p.loadReportAndExtraction(ctx, event.ReportID, event.ExtractionID, event.ContractVersion)
	if err != nil {
		return err
	}
	if report.IncidentID != nil {
		return nil
	}
	if result.EventType.Status == "identified" && result.EventType.Value != nil {
		report.EventType = result.EventType.Value
	}
	decision, err := p.assess(ctx, report, result)
	if err != nil {
		return err
	}
	if err := p.apply(ctx, report, result, decision); err != nil {
		return err
	}
	return nil
}

type aiProcessedEvent struct {
	ReportID        string `json:"report_id"`
	ExtractionID    string `json:"extraction_id"`
	ContractVersion string `json:"contract_version"`
}

func parseAIProcessed(fields map[string]any) (aiProcessedEvent, error) {
	get := func(name string) (string, error) {
		value, ok := fields[name]
		if !ok {
			return "", fmt.Errorf("incident event missing %s", name)
		}
		switch typed := value.(type) {
		case string:
			if typed != "" {
				return typed, nil
			}
		case []byte:
			if len(typed) > 0 {
				return string(typed), nil
			}
		}
		return "", fmt.Errorf("incident event %s is invalid", name)
	}
	name, err := get("event_name")
	if err != nil {
		return aiProcessedEvent{}, err
	}
	if name != ReportAIProcessedV1 {
		return aiProcessedEvent{}, fmt.Errorf("unsupported incident input event %q", name)
	}
	aggregate, err := get("aggregate_type")
	if err != nil {
		return aiProcessedEvent{}, err
	}
	if aggregate != "report" {
		return aiProcessedEvent{}, fmt.Errorf("unexpected incident aggregate type %q", aggregate)
	}
	payload, err := get("payload")
	if err != nil {
		return aiProcessedEvent{}, err
	}
	var result aiProcessedEvent
	if err := json.Unmarshal([]byte(payload), &result); err != nil {
		return aiProcessedEvent{}, fmt.Errorf("decode report.ai_processed payload: %w", err)
	}
	if result.ReportID == "" || result.ExtractionID == "" || result.ContractVersion == "" {
		return aiProcessedEvent{}, fmt.Errorf("report.ai_processed payload is incomplete")
	}
	if _, err := uuid.Parse(result.ReportID); err != nil {
		return aiProcessedEvent{}, fmt.Errorf("invalid report_id: %w", err)
	}
	if _, err := uuid.Parse(result.ExtractionID); err != nil {
		return aiProcessedEvent{}, fmt.Errorf("invalid extraction_id: %w", err)
	}
	return result, nil
}

type reportEvidence struct {
	ID                  string
	IncidentID          *string
	RawText             string
	ObservedAt          *time.Time
	SubmittedAt         time.Time
	Latitude, Longitude *float64
	EventType           *string
}

func (p *Processor) loadReportAndExtraction(ctx context.Context, reportID, extractionID, contractVersion string) (reportEvidence, extraction.Extraction, error) {
	var report reportEvidence
	var lat, lon pgtype.Float8
	err := p.pool.QueryRow(ctx, `
		SELECT id::text, incident_id::text, raw_text, observed_at, submitted_at,
		       ST_Y(claimed_location::geometry), ST_X(claimed_location::geometry)
		FROM reports WHERE id = $1
	`, reportID).Scan(&report.ID, &report.IncidentID, &report.RawText, &report.ObservedAt, &report.SubmittedAt, &lat, &lon)
	if err != nil {
		return reportEvidence{}, extraction.Extraction{}, fmt.Errorf("load report %s: %w", reportID, err)
	}
	if lat.Valid && lon.Valid {
		report.Latitude, report.Longitude = &lat.Float64, &lon.Float64
	}
	var encoded []byte
	err = p.pool.QueryRow(ctx, `SELECT result FROM report_ai_extractions WHERE id = $1 AND report_id = $2 AND contract_version = $3`, extractionID, reportID, contractVersion).Scan(&encoded)
	if err != nil {
		return reportEvidence{}, extraction.Extraction{}, fmt.Errorf("load durable extraction for report %s: %w", reportID, err)
	}
	var result extraction.Extraction
	if err := json.Unmarshal(encoded, &result); err != nil {
		return reportEvidence{}, extraction.Extraction{}, fmt.Errorf("decode durable extraction for report %s: %w", reportID, err)
	}
	return report, result, nil
}

func (p *Processor) assess(ctx context.Context, report reportEvidence, result extraction.Extraction) (PolicyDecision, error) {
	return p.assessExcluding(ctx, report, result, nil)
}

func (p *Processor) assessExcluding(ctx context.Context, report reportEvidence, result extraction.Extraction, excluded map[string]struct{}) (PolicyDecision, error) {
	if result.EventType.Status != "identified" || result.EventType.Value == nil || report.Latitude == nil || report.Longitude == nil {
		return PolicyDecision{Decision: DecisionCreate, Reason: "candidate lookup requires identified event type and claimed coordinates"}, nil
	}
	now := firstTime(report.ObservedAt, report.SubmittedAt)
	candidates, err := p.lookup.LookupCandidates(ctx, CandidateLookup{EventType: *result.EventType.Value, Latitude: *report.Latitude, Longitude: *report.Longitude, RadiusMeters: p.policy.CandidateRadiusMeters, AsOf: now, TimeWindow: p.policy.CandidateTimeWindow, Limit: p.policy.CandidateLimit})
	if err != nil {
		return PolicyDecision{}, err
	}
	if len(candidates) == 0 {
		return PolicyDecision{Decision: DecisionCreate, Reason: "candidate lookup returned no candidates"}, nil
	}
	reportLocation := location.Normalization{}
	if result.LocationReference.Status != "unknown" {
		if encoded, normalizeErr := (location.RuleBasedNormalizer{}).Normalize(ctx, result.LocationReference); normalizeErr == nil {
			_ = json.Unmarshal(encoded, &reportLocation)
		}
	}
	evidence := similarity.ReportEvidence{EventType: result.EventType, TimeReference: result.TimeReference, Location: reportLocation, Text: report.RawText}
	assessed := make([]AssessedCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if _, skip := excluded[candidate.ID.String()]; skip {
			continue
		}
		distance, radius := candidate.DistanceMeters, p.policy.CandidateRadiusMeters
		candidateEvidence, err := p.loadCandidateEvidence(ctx, candidate.ID, candidate.EventType)
		if err != nil {
			return PolicyDecision{}, err
		}
		encoded, err := p.similarity.Assess(ctx, evidence, similarity.CandidateIncident{ID: candidate.ID.String(), Evidence: candidateEvidence, DistanceMeters: &distance, SearchRadiusMeters: &radius})
		if err != nil {
			return PolicyDecision{}, err
		}
		if p.validator == nil {
			return PolicyDecision{}, fmt.Errorf("similarity validator is required")
		}
		assessment, err := p.validator.Validate(encoded)
		if err != nil {
			return PolicyDecision{}, fmt.Errorf("validate similarity assessment: %w", err)
		}
		if assessment.CandidateIncidentID != candidate.ID.String() || assessment.SimilarityScore != nil && (*assessment.SimilarityScore < 0 || *assessment.SimilarityScore > 1) {
			return PolicyDecision{}, fmt.Errorf("invalid similarity assessment for candidate %s", candidate.ID)
		}
		assessed = append(assessed, AssessedCandidate{ID: candidate.ID.String(), Assessment: assessment})
	}
	return DecideAttachment(assessed, p.policy.SimilarityThreshold, p.policy.SimilarityWinnerMargin)
}

func (p *Processor) loadCandidateEvidence(ctx context.Context, incidentID uuid.UUID, fallbackEventType string) (similarity.ReportEvidence, error) {
	var rawText string
	var encoded []byte
	err := p.pool.QueryRow(ctx, `
		SELECT r.raw_text, e.result
		FROM incident_reports AS ir
		JOIN reports AS r ON r.id = ir.report_id
	LEFT JOIN report_ai_extractions AS e
		  ON e.report_id = r.id AND e.contract_version = $2
		WHERE ir.incident_id = $1
		ORDER BY ir.attached_at, ir.report_id
		LIMIT 1
	`, incidentID, similarity.InputContractVersion).Scan(&rawText, &encoded)
	if err == pgx.ErrNoRows {
		return similarity.ReportEvidence{
			EventType:     extraction.Field{Status: "identified", Value: &fallbackEventType, Candidates: []string{}, EvidenceQuotes: []string{}},
			TimeReference: extraction.Field{Status: "unknown", Candidates: []string{}, EvidenceQuotes: []string{}},
			Location:      location.Normalization{LocationState: "unknown", Candidates: []location.Candidate{}},
		}, nil
	}
	if err != nil {
		return similarity.ReportEvidence{}, fmt.Errorf("load candidate incident %s evidence: %w", incidentID, err)
	}
	result := extraction.Extraction{}
	if len(encoded) > 0 {
		if err := json.Unmarshal(encoded, &result); err != nil {
			return similarity.ReportEvidence{}, fmt.Errorf("decode candidate incident %s extraction: %w", incidentID, err)
		}
	} else {
		result.EventType = extraction.Field{Status: "identified", Value: &fallbackEventType, Candidates: []string{}, EvidenceQuotes: []string{}}
		result.TimeReference = extraction.Field{Status: "unknown", Candidates: []string{}, EvidenceQuotes: []string{}}
	}
	normalized := location.Normalization{LocationState: "unknown", Candidates: []location.Candidate{}}
	if result.LocationReference.Status != "unknown" {
		encodedLocation, normalizeErr := (location.RuleBasedNormalizer{}).Normalize(ctx, result.LocationReference)
		if normalizeErr == nil {
			if err := json.Unmarshal(encodedLocation, &normalized); err != nil {
				return similarity.ReportEvidence{}, err
			}
		}
	}
	return similarity.ReportEvidence{EventType: result.EventType, TimeReference: result.TimeReference, Location: normalized, Text: rawText}, nil
}

func (p *Processor) apply(ctx context.Context, report reportEvidence, result extraction.Extraction, decision PolicyDecision) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin incident decision: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var existing *string
	if err := tx.QueryRow(ctx, `SELECT incident_id::text FROM reports WHERE id = $1 FOR UPDATE`, report.ID).Scan(&existing); err != nil {
		return fmt.Errorf("lock report %s: %w", report.ID, err)
	}
	if existing != nil {
		return tx.Commit(ctx)
	}
	if decision.Decision == DecisionCreate || decision.Decision == DecisionAttach {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('signa.incident.create', 0))`); err != nil {
			return fmt.Errorf("serialize incident create decision: %w", err)
		}
	}
	if decision.Decision == DecisionCreate {
		decision, err = p.assess(ctx, report, result)
		if err != nil {
			return err
		}
	}
	metadata, _ := json.Marshal(struct {
		ReportID     string   `json:"report_id"`
		Decision     Decision `json:"decision"`
		Score        *float64 `json:"similarity_score,omitempty"`
		WinnerMargin *float64 `json:"winner_margin,omitempty"`
	}{report.ID, decision.Decision, decision.Score, decision.WinnerMargin})
	var incidentID string
	excluded := make(map[string]struct{})
	for decision.Decision == DecisionAttach {
		incidentID = decision.CandidateID
		var resolvedAt, expiresAt *time.Time
		if err := tx.QueryRow(ctx, `SELECT resolved_at, expires_at FROM incidents WHERE id = $1 FOR UPDATE`, incidentID).Scan(&resolvedAt, &expiresAt); err != nil {
			return fmt.Errorf("recheck incident candidate %s: %w", incidentID, err)
		}
		if resolvedAt == nil && (expiresAt == nil || expiresAt.After(p.now())) {
			break
		}
		excluded[incidentID] = struct{}{}
		decision, err = p.assessExcluding(ctx, report, result, excluded)
		if err != nil {
			return err
		}
		if decision.Decision == DecisionCreate {
			decision.Reason = "re-evaluated after candidate became unattachable: " + decision.Reason
		}
	}
	if decision.Decision == DecisionAttach {
		if _, err := tx.Exec(ctx, `UPDATE incidents SET last_signal_at = GREATEST(COALESCE(last_signal_at, '-infinity'::timestamptz), $2), updated_at = now() WHERE id = $1`, incidentID, firstTime(report.ObservedAt, report.SubmittedAt)); err != nil {
			return fmt.Errorf("update attached incident signal: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO incident_reports (incident_id, report_id, similarity) VALUES ($1, $2, $3) ON CONFLICT (incident_id, report_id) DO NOTHING`, incidentID, report.ID, decision.Score); err != nil {
			return fmt.Errorf("attach report: %w", err)
		}
	} else {
		if report.IncidentID != nil {
			return tx.Commit(ctx)
		}
		if result := report; result.Latitude != nil && result.Longitude != nil {
			err = tx.QueryRow(ctx, `INSERT INTO incidents (event_type, status, confidence_state, severity, center_point, started_at, last_signal_at) VALUES ($4, 'OPEN', 'UNVERIFIED', NULL, ST_SetSRID(ST_MakePoint($2, $1), 4326)::geography, $3, $3) RETURNING id::text`, *result.Latitude, *result.Longitude, firstTime(result.ObservedAt, result.SubmittedAt), result.EventType).Scan(&incidentID)
		} else {
			err = tx.QueryRow(ctx, `INSERT INTO incidents (event_type, status, confidence_state, severity, started_at, last_signal_at) VALUES ($2, 'OPEN', 'UNVERIFIED', NULL, $1, $1) RETURNING id::text`, firstTime(result.ObservedAt, result.SubmittedAt), result.EventType).Scan(&incidentID)
		}
		if err != nil {
			return fmt.Errorf("create incident: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO incident_reports (incident_id, report_id, similarity) VALUES ($1, $2, NULL)`, incidentID, report.ID); err != nil {
			return fmt.Errorf("link created incident: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE reports SET incident_id = $1 WHERE id = $2 AND incident_id IS NULL`, incidentID, report.ID); err != nil {
		return fmt.Errorf("set report incident: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO incident_state_history (incident_id, changed_field, new_value, reason, actor_type, rule_version, metadata) VALUES ($1, $2, $3, $4, 'system', $5, $6::jsonb)`, incidentID, map[Decision]string{DecisionCreate: "incident_created", DecisionAttach: "report_attached"}[decision.Decision], decision.Decision, decision.Reason, AttachmentPolicyVersion, metadata); err != nil {
		return fmt.Errorf("write incident history: %w", err)
	}
	eventTypeName := map[Decision]string{DecisionCreate: "incident.created", DecisionAttach: "incident.report_attached"}[decision.Decision]
	payload, _ := json.Marshal(struct {
		IncidentID      string   `json:"incident_id"`
		ReportID        string   `json:"report_id"`
		PolicyVersion   string   `json:"policy_version"`
		SimilarityScore *float64 `json:"similarity_score,omitempty"`
		WinnerMargin    *float64 `json:"winner_margin,omitempty"`
	}{incidentID, report.ID, AttachmentPolicyVersion, decision.Score, decision.WinnerMargin})
	if _, err := tx.Exec(ctx, `INSERT INTO outbox_events (event_type, aggregate_type, aggregate_id, payload) VALUES ($1, 'incident', $2, $3::jsonb)`, eventTypeName, incidentID, payload); err != nil {
		return fmt.Errorf("write incident outbox event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit incident decision: %w", err)
	}
	return nil
}

func firstTime(observed *time.Time, submitted time.Time) time.Time {
	if observed != nil {
		return observed.UTC()
	}
	return submitted.UTC()
}

type Consumer struct {
	client    StreamClient
	processor interface {
		Process(context.Context, StreamMessage) error
	}
	stream, group, consumer string
	poll                    time.Duration
}

func NewConsumer(client StreamClient, processor interface {
	Process(context.Context, StreamMessage) error
}, stream, group, consumer string, poll time.Duration) *Consumer {
	return &Consumer{client: client, processor: processor, stream: stream, group: group, consumer: consumer, poll: poll}
}
func (c *Consumer) Run(ctx context.Context) error {
	if c == nil || c.client == nil || c.processor == nil || c.stream == "" || c.group == "" || c.consumer == "" || c.poll <= 0 {
		return fmt.Errorf("incident consumer configuration is invalid")
	}
	if err := c.client.EnsureGroup(ctx, c.stream, c.group); err != nil {
		return err
	}
	for ctx.Err() == nil {
		messages, err := readPending(ctx, c.client, c.stream, c.group, c.consumer)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		for _, message := range messages {
			if name, ok := streamString(message.Values, "event_name"); ok && name != ReportAIProcessedV1 {
				if name == extraction.ReportCreatedV1 || name == "report.media_attached.v1" {
					if _, err := extraction.ParseReportEvent(message.Values); err != nil {
						continue
					}
					if err := c.client.Ack(ctx, c.stream, c.group, message.ID); err != nil {
						return err
					}
					continue
				}
				// Unknown event names remain pending for inspection rather than being discarded.
				continue
			}
			if err := c.processor.Process(ctx, message); err != nil {
				continue
			}
			if err := c.client.Ack(ctx, c.stream, c.group, message.ID); err != nil {
				return err
			}
		}
		// Always read new entries after pending work. A repeatedly failing
		// pending message must not starve newer incident events.
		messages, err = c.client.Read(ctx, c.stream, c.group, c.consumer, false, c.poll)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		for _, message := range messages {
			if name, ok := streamString(message.Values, "event_name"); ok && name != ReportAIProcessedV1 {
				if name == extraction.ReportCreatedV1 || name == "report.media_attached.v1" {
					if _, err := extraction.ParseReportEvent(message.Values); err != nil {
						continue
					}
					if err := c.client.Ack(ctx, c.stream, c.group, message.ID); err != nil {
						return err
					}
				}
				continue
			}
			if err := c.processor.Process(ctx, message); err != nil {
				continue
			}
			if err := c.client.Ack(ctx, c.stream, c.group, message.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

func readPending(ctx context.Context, client StreamClient, stream, group, consumer string) ([]StreamMessage, error) {
	messages, err := client.Read(ctx, stream, group, consumer, true, 0)
	if err != nil {
		return messages, err
	}
	if claimer, ok := client.(extraction.PendingClaimer); ok {
		claimed, claimErr := claimer.ClaimPending(ctx, stream, group, consumer)
		if claimErr != nil {
			return nil, claimErr
		}
		return extraction.MergeMessages(messages, claimed), nil
	}
	return messages, nil
}

func streamString(fields map[string]any, name string) (string, bool) {
	value, ok := fields[name]
	if !ok {
		return "", false
	}
	switch typed := value.(type) {
	case string:
		return typed, typed != ""
	case []byte:
		return string(typed), len(typed) > 0
	default:
		return "", false
	}
}
