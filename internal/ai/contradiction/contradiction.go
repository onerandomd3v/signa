// Package contradiction emits auditable interpretation signals when explicit
// report claims conflict. Signals do not determine truth or incident state.
package contradiction

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/onerandomd3v/signa/internal/ai/extraction"
	"github.com/onerandomd3v/signa/internal/ai/location"
)

const (
	ContractVersion      = "signa.ai.contradiction-assessment.v1"
	InputContractVersion = "signa.ai.report-extraction.v0"
)

type Claim struct {
	Subject       string `json:"subject"`
	State         string `json:"state"`
	EvidenceQuote string `json:"evidence_quote"`
}

// Evidence contains explicit, already-extracted context and a narrow claim
// pair. ObservedAt is an observation timestamp supplied by the caller; it is
// used only to order evidence, never to infer freshness or truth.
type Evidence struct {
	ID            string                 `json:"id"`
	EventType     extraction.Field       `json:"event_type"`
	TimeReference extraction.Field       `json:"time_reference"`
	Location      location.Normalization `json:"location"`
	Claim         Claim                  `json:"claim"`
	ObservedAt    *time.Time             `json:"observed_at"`
}

type Assessment struct {
	ContractVersion      string   `json:"contract_version"`
	InputContractVersion string   `json:"input_contract_version"`
	LeftEvidenceID       string   `json:"left_evidence_id"`
	RightEvidenceID      string   `json:"right_evidence_id"`
	ContradictionState   string   `json:"contradiction_state"`
	RecencyOrder         string   `json:"recency_order"`
	Factors              []Factor `json:"factors"`
}

type Factor struct {
	Name    string `json:"name"`
	Outcome string `json:"outcome"`
	Reason  string `json:"reason"`
}

type Provider interface {
	Assess(context.Context, Evidence, Evidence) ([]byte, error)
}

type ProviderFunc func(context.Context, Evidence, Evidence) ([]byte, error)

func (f ProviderFunc) Assess(ctx context.Context, left, right Evidence) ([]byte, error) {
	if f == nil {
		return nil, errors.New("contradiction provider function is nil")
	}
	return f(ctx, left, right)
}

type Processor struct {
	Provider  Provider
	Validator *Validator
}

func (p Processor) Evaluate(ctx context.Context, left, right Evidence) (Assessment, error) {
	if p.Provider == nil {
		return Assessment{}, errors.New("contradiction provider is required")
	}
	if p.Validator == nil {
		return Assessment{}, errors.New("contradiction validator is required")
	}
	if err := ValidateInput(left); err != nil {
		return Assessment{}, fmt.Errorf("validate left evidence: %w", err)
	}
	if err := ValidateInput(right); err != nil {
		return Assessment{}, fmt.Errorf("validate right evidence: %w", err)
	}
	data, err := p.Provider.Assess(ctx, left, right)
	if err != nil {
		return Assessment{}, fmt.Errorf("assess contradiction: %w", err)
	}
	assessment, err := p.Validator.Validate(data)
	if err != nil {
		return Assessment{}, err
	}
	if assessment.LeftEvidenceID != left.ID || assessment.RightEvidenceID != right.ID {
		return Assessment{}, errors.New("contradiction assessment evidence IDs do not match request")
	}
	return assessment, nil
}

// ValidateInput permits only explicitly modeled claim subjects and states;
// arbitrary natural-language inference remains the responsibility of an
// injected provider. The deterministic evaluator stays intentionally narrow.
func ValidateInput(input Evidence) error {
	if strings.TrimSpace(input.ID) == "" {
		return errors.New("evidence id is required")
	}
	if input.EventType.Status != "identified" && input.EventType.Status != "ambiguous" && input.EventType.Status != "unknown" {
		return errors.New("event_type has unsupported status")
	}
	switch input.EventType.Status {
	case "identified":
		if input.EventType.Value == nil || strings.TrimSpace(*input.EventType.Value) == "" || len(input.EventType.Candidates) != 0 {
			return errors.New("identified event_type requires a value and no candidates")
		}
	case "ambiguous":
		if input.EventType.Value != nil || len(input.EventType.Candidates) < 2 {
			return errors.New("ambiguous event_type requires multiple candidates and a null value")
		}
	case "unknown":
		if input.EventType.Value != nil || len(input.EventType.Candidates) != 0 {
			return errors.New("unknown event_type cannot include a value or candidates")
		}
	}
	if input.Location.LocationState != "identified" && input.Location.LocationState != "ambiguous" && input.Location.LocationState != "unknown" {
		return errors.New("location has unsupported state")
	}
	if input.Location.LocationState != input.Location.SourceLocationReference.Status {
		return errors.New("location state does not match its source reference")
	}
	if err := location.ValidateLocationReference(input.Location.SourceLocationReference); err != nil {
		return fmt.Errorf("invalid source location reference: %w", err)
	}
	switch input.Location.LocationState {
	case "identified":
		if len(input.Location.Candidates) != 1 || strings.TrimSpace(input.Location.Candidates[0].NormalizedText) == "" {
			return errors.New("identified location requires one non-empty normalized candidate")
		}
	case "ambiguous":
		if len(input.Location.Candidates) < 2 {
			return errors.New("ambiguous location requires multiple candidates")
		}
	case "unknown":
		if len(input.Location.Candidates) != 0 {
			return errors.New("unknown location cannot include candidates")
		}
	}
	if input.Claim.Subject != "passage" && input.Claim.Subject != "activity" {
		return errors.New("claim subject must be passage or activity")
	}
	allowed := map[string]map[string]bool{"passage": {"blocked": true, "open": true}, "activity": {"ongoing": true, "stopped": true}}
	if !allowed[input.Claim.Subject][input.Claim.State] {
		return errors.New("claim state is unsupported for subject")
	}
	if strings.TrimSpace(input.Claim.EvidenceQuote) == "" {
		return errors.New("claim evidence_quote is required")
	}
	return nil
}

// RuleBasedEvaluator compares only exact categorical event/location context
// and the supported opposite claim states. Timestamp ordering has no cutoff.
type RuleBasedEvaluator struct{}

func (RuleBasedEvaluator) Assess(ctx context.Context, left, right Evidence) ([]byte, error) {
	return (RuleBasedEvaluator{}).Evaluate(ctx, left, right)
}

func (RuleBasedEvaluator) Evaluate(ctx context.Context, left, right Evidence) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := ValidateInput(left); err != nil {
		return nil, fmt.Errorf("validate left evidence: %w", err)
	}
	if err := ValidateInput(right); err != nil {
		return nil, fmt.Errorf("validate right evidence: %w", err)
	}
	event := compareEvent(left.EventType, right.EventType)
	place := compareLocation(left.Location, right.Location)
	claim := compareClaim(left.Claim, right.Claim)
	state := "indeterminate"
	if event.Outcome == "not_comparable" || place.Outcome == "not_comparable" {
		state = "not_comparable"
		claim = Factor{Name: "claim_state", Outcome: "not_comparable", Reason: "claims are not assessed across distinct event or identified location contexts"}
	} else if event.Outcome == "no_signal" && place.Outcome == "no_signal" {
		switch claim.Outcome {
		case "contradiction":
			state = "contradiction_signal"
		case "no_signal":
			state = "no_signal"
		}
	}
	assessment := Assessment{ContractVersion: ContractVersion, InputContractVersion: InputContractVersion, LeftEvidenceID: left.ID, RightEvidenceID: right.ID, ContradictionState: state, RecencyOrder: recency(left.ObservedAt, right.ObservedAt), Factors: []Factor{event, place, claim}}
	return json.Marshal(assessment)
}

func compareEvent(left, right extraction.Field) Factor {
	if left.Status != "identified" || right.Status != "identified" || left.Value == nil || right.Value == nil {
		return Factor{"event_type", "unknown", "one or both event types are unknown or ambiguous"}
	}
	if strings.EqualFold(strings.TrimSpace(*left.Value), strings.TrimSpace(*right.Value)) {
		return Factor{"event_type", "no_signal", "identified event types agree"}
	}
	return Factor{"event_type", "not_comparable", "identified event types differ"}
}

func compareLocation(left, right location.Normalization) Factor {
	if left.LocationState != "identified" || right.LocationState != "identified" || len(left.Candidates) != 1 || len(right.Candidates) != 1 {
		return Factor{"location_reference", "unknown", "one or both locations are unknown or ambiguous"}
	}
	if strings.EqualFold(strings.TrimSpace(left.Candidates[0].NormalizedText), strings.TrimSpace(right.Candidates[0].NormalizedText)) {
		return Factor{"location_reference", "no_signal", "identified normalized location wording agrees"}
	}
	return Factor{"location_reference", "not_comparable", "identified location candidates differ"}
}

func compareClaim(left, right Claim) Factor {
	if left.Subject != right.Subject {
		return Factor{"claim_state", "unknown", "claim subjects differ and are not directly comparable"}
	}
	if left.State == right.State {
		return Factor{"claim_state", "no_signal", "explicit claim states agree"}
	}
	if (left.Subject == "passage" && opposite(left.State, right.State, "blocked", "open")) || (left.Subject == "activity" && opposite(left.State, right.State, "ongoing", "stopped")) {
		return Factor{"claim_state", "contradiction", "explicit claims state opposite conditions for the same subject"}
	}
	return Factor{"claim_state", "unknown", "claim states are not directly comparable"}
}

func opposite(left, right, a, b string) bool {
	return left == a && right == b || left == b && right == a
}

func recency(left, right *time.Time) string {
	if left == nil || right == nil {
		return "unknown"
	}
	if left.Equal(*right) {
		return "same_time"
	}
	if left.After(*right) {
		return "left_newer"
	}
	return "right_newer"
}
