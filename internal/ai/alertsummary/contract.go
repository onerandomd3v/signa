package alertsummary

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const ContractVersion = "signa.ai.alert-summarization.v1"

type ConfidenceState string

const (
	Unverified     ConfidenceState = "UNVERIFIED"
	Emerging       ConfidenceState = "EMERGING"
	Corroborated   ConfidenceState = "CORROBORATED"
	HighConfidence ConfidenceState = "HIGH_CONFIDENCE"
	Disputed       ConfidenceState = "DISPUTED"
)

type Freshness string

const (
	Current          Freshness = "CURRENT"
	Aging            Freshness = "AGING"
	Stale            Freshness = "STALE"
	UnknownFreshness Freshness = "UNKNOWN"
)

type Location struct {
	Status string  `json:"status"`
	Label  *string `json:"label"`
}

type State struct {
	EventLabel      string          `json:"event_label"`
	ConfidenceState ConfidenceState `json:"confidence_state"`
	Severity        *string         `json:"severity"`
	Freshness       Freshness       `json:"freshness"`
	LifecycleStatus string          `json:"lifecycle_status"`
	Location        Location        `json:"location"`
}

type Summary struct {
	ContractVersion      string          `json:"contract_version"`
	Title                string          `json:"title"`
	Message              string          `json:"message"`
	UncertaintyQualifier string          `json:"uncertainty_qualifier"`
	ConfidenceState      ConfidenceState `json:"confidence_state"`
	Severity             *string         `json:"severity"`
	Freshness            Freshness       `json:"freshness"`
	LifecycleStatus      string          `json:"lifecycle_status"`
	Location             Location        `json:"location"`
}

type Provider interface {
	Summarize(context.Context, State) ([]byte, error)
}
type ProviderFunc func(context.Context, State) ([]byte, error)

func (f ProviderFunc) Summarize(ctx context.Context, state State) ([]byte, error) {
	if f == nil {
		return nil, fmt.Errorf("alert summary provider function is nil")
	}
	return f(ctx, state)
}

type Validator struct{ schema *jsonschema.Schema }

func NewValidator(schemaBytes []byte) (*Validator, error) {
	var document any
	if err := json.Unmarshal(schemaBytes, &document); err != nil {
		return nil, fmt.Errorf("decode alert summarization schema: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("https://signa.local/contracts/ai/alert-summarization/v1/schema.json", document); err != nil {
		return nil, fmt.Errorf("register alert summarization schema: %w", err)
	}
	schema, err := compiler.Compile("https://signa.local/contracts/ai/alert-summarization/v1/schema.json")
	if err != nil {
		return nil, fmt.Errorf("compile alert summarization schema: %w", err)
	}
	return &Validator{schema: schema}, nil
}

func (v *Validator) Validate(data []byte) (Summary, error) {
	if v == nil || v.schema == nil {
		return Summary{}, fmt.Errorf("alert summarization validator is not initialized")
	}
	var document any
	if err := json.Unmarshal(data, &document); err != nil {
		return Summary{}, fmt.Errorf("decode alert summary: %w", err)
	}
	if err := v.schema.Validate(document); err != nil {
		return Summary{}, fmt.Errorf("validate alert summary: %w", err)
	}
	var summary Summary
	if err := json.Unmarshal(data, &summary); err != nil {
		return Summary{}, fmt.Errorf("decode validated alert summary: %w", err)
	}
	return summary, nil
}

func ValidateBound(state State, summary Summary) error {
	if summary.ContractVersion != ContractVersion {
		return fmt.Errorf("contract_version must be %q", ContractVersion)
	}
	if summary.ConfidenceState != state.ConfidenceState {
		return fmt.Errorf("provider changed confidence_state from %q to %q", state.ConfidenceState, summary.ConfidenceState)
	}
	if !sameStringPointer(summary.Severity, state.Severity) {
		return fmt.Errorf("provider changed severity")
	}
	if summary.Freshness != state.Freshness || summary.LifecycleStatus != state.LifecycleStatus || !sameLocation(summary.Location, state.Location) {
		return fmt.Errorf("provider changed authoritative freshness, lifecycle, or location")
	}
	if strings.TrimSpace(summary.UncertaintyQualifier) == "" {
		return fmt.Errorf("uncertainty_qualifier is required")
	}
	text := strings.ToLower(summary.Title + " " + summary.Message + " " + summary.UncertaintyQualifier)
	if state.ConfidenceState == Unverified || state.ConfidenceState == Emerging || state.ConfidenceState == Disputed {
		for _, word := range []string{"confirmed", "verified", "definitely", "certainly", "fact"} {
			if strings.Contains(text, word) && !(word == "verified" && strings.Contains(text, "unverified")) {
				return fmt.Errorf("%s confidence cannot use certainty word %q", state.ConfidenceState, word)
			}
		}
	}
	if state.ConfidenceState == Disputed && !strings.Contains(text, "disput") && !strings.Contains(text, "conflict") && !strings.Contains(text, "uncertain") {
		return fmt.Errorf("disputed summary must mention conflicting or uncertain information")
	}
	if state.Freshness != Current || (state.LifecycleStatus != "OPEN" && state.LifecycleStatus != "UNKNOWN") {
		for _, word := range []string{"ongoing", "happening now", "currently", "right now"} {
			if strings.Contains(text, word) {
				return fmt.Errorf("stale/resolving summary cannot imply current activity")
			}
		}
	}
	return nil
}

func sameStringPointer(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func sameLocation(a, b Location) bool {
	return a.Status == b.Status && sameStringPointer(a.Label, b.Label)
}

func Summarize(ctx context.Context, state State, provider Provider, validator *Validator) (Summary, error) {
	if provider == nil {
		return Summary{}, fmt.Errorf("alert summary provider is required")
	}
	if validator == nil {
		return Summary{}, fmt.Errorf("alert summary validator is required")
	}
	data, err := provider.Summarize(ctx, state)
	if err != nil {
		return Summary{}, fmt.Errorf("summarize alert: %w", err)
	}
	summary, err := validator.Validate(data)
	if err != nil {
		return Summary{}, err
	}
	if err := ValidateBound(state, summary); err != nil {
		return Summary{}, err
	}
	return summary, nil
}
