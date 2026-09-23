package location

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/onerandomd3v/signa/internal/ai/extraction"
)

const (
	ContractVersion      = "signa.location-normalization.v0"
	InputContractVersion = "signa.ai.report-extraction.v0"
)

// Normalization is a text-only location interpretation. It is not a geocode,
// coordinate, incident-confidence signal, or truth decision.
type Normalization struct {
	ContractVersion         string           `json:"contract_version"`
	InputContractVersion    string           `json:"input_contract_version"`
	LocationState           string           `json:"location_state"`
	SourceLocationReference extraction.Field `json:"source_location_reference"`
	Candidates              []Candidate      `json:"candidates"`
}

// Candidate is a conservative textual location candidate. ReferenceKind is a
// lexical description only; it does not assert a map match or administrative
// boundary.
type Candidate struct {
	ReportedText   string   `json:"reported_text"`
	NormalizedText string   `json:"normalized_text"`
	ReferenceKind  string   `json:"reference_kind"`
	Qualifier      *string  `json:"qualifier"`
	EvidenceQuotes []string `json:"evidence_quotes"`
}

// Provider turns an extracted location field into contract JSON.
type Provider interface {
	Normalize(context.Context, extraction.Field) ([]byte, error)
}

// ProviderFunc adapts a function into a location Provider.
type ProviderFunc func(context.Context, extraction.Field) ([]byte, error)

// Normalize implements Provider.
func (f ProviderFunc) Normalize(ctx context.Context, field extraction.Field) ([]byte, error) {
	if f == nil {
		return nil, errors.New("location normalization provider function is nil")
	}
	return f(ctx, field)
}

// Processor validates injected provider output against the canonical contract.
type Processor struct {
	Provider  Provider
	Validator *Validator
}

// Process normalizes one extracted location reference and validates the
// structured result before returning it.
func (p Processor) Process(ctx context.Context, field extraction.Field) (Normalization, error) {
	if p.Provider == nil {
		return Normalization{}, errors.New("location normalization provider is required")
	}
	if p.Validator == nil {
		return Normalization{}, errors.New("location normalization validator is required")
	}
	output, err := p.Provider.Normalize(ctx, field)
	if err != nil {
		return Normalization{}, fmt.Errorf("normalize location: %w", err)
	}
	return p.Validator.Validate(output)
}

// ValidateLocationReference checks the v0 extraction field semantics before a
// provider is allowed to produce location candidates.
func ValidateLocationReference(field extraction.Field) error {
	switch field.Status {
	case "identified":
		if field.Value == nil || strings.TrimSpace(*field.Value) == "" {
			return errors.New("identified location reference requires a value")
		}
		if len(field.Candidates) != 0 {
			return errors.New("identified location reference cannot contain candidates")
		}
		if len(field.EvidenceQuotes) == 0 {
			return errors.New("identified location reference requires evidence_quotes")
		}
	case "ambiguous":
		if field.Value != nil {
			return errors.New("ambiguous location reference requires a null value")
		}
		if len(field.Candidates) < 2 {
			return errors.New("ambiguous location reference requires at least two candidates")
		}
		if len(field.EvidenceQuotes) == 0 {
			return errors.New("ambiguous location reference requires evidence_quotes")
		}
	case "unknown":
		if field.Value != nil || len(field.Candidates) != 0 || len(field.EvidenceQuotes) != 0 {
			return errors.New("unknown location reference cannot contain value, candidates, or evidence_quotes")
		}
	default:
		return fmt.Errorf("unsupported location reference status %q", field.Status)
	}
	return nil
}

func cloneField(field extraction.Field) extraction.Field {
	result := field
	if field.Candidates == nil {
		result.Candidates = []string{}
	} else {
		result.Candidates = append([]string{}, field.Candidates...)
	}
	if field.EvidenceQuotes == nil {
		result.EvidenceQuotes = []string{}
	} else {
		result.EvidenceQuotes = append([]string{}, field.EvidenceQuotes...)
	}
	if field.Value != nil {
		value := *field.Value
		result.Value = &value
	}
	return result
}

func marshalNormalization(value Normalization) ([]byte, error) {
	output, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode location normalization: %w", err)
	}
	return output, nil
}
