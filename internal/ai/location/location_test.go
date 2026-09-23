package location

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/onerandomd3v/signa/internal/ai/extraction"
)

func TestRuleBasedNormalizerPreservesIdentifiedLocationTraceability(t *testing.T) {
	input := extraction.Field{
		Status:         "identified",
		Value:          stringPointer("near the old market"),
		Candidates:     []string{},
		EvidenceQuotes: []string{"near the old market"},
	}

	output, err := (RuleBasedNormalizer{}).Normalize(context.Background(), input)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	normalized := validateTestOutput(t, output)
	if normalized.LocationState != "identified" {
		t.Fatalf("LocationState = %q, want identified", normalized.LocationState)
	}
	if normalized.SourceLocationReference.Value == nil || *normalized.SourceLocationReference.Value != "near the old market" {
		t.Fatalf("source location value = %v, want original wording", normalized.SourceLocationReference.Value)
	}
	if len(normalized.Candidates) != 1 {
		t.Fatalf("candidate count = %d, want 1", len(normalized.Candidates))
	}
	candidate := normalized.Candidates[0]
	if candidate.ReportedText != "near the old market" || candidate.NormalizedText != "old market" {
		t.Fatalf("candidate text = %+v, want reported and normalized wording preserved", candidate)
	}
	if candidate.Qualifier == nil || *candidate.Qualifier != "near" || candidate.ReferenceKind != "market" {
		t.Fatalf("candidate classification = %+v, want near/market", candidate)
	}
	if len(candidate.EvidenceQuotes) != 1 || candidate.EvidenceQuotes[0] != "near the old market" {
		t.Fatalf("candidate evidence = %v, want source quote", candidate.EvidenceQuotes)
	}
}

func TestRuleBasedNormalizerPreservesAmbiguousCandidatesWithoutCoordinates(t *testing.T) {
	input := extraction.Field{
		Status:         "ambiguous",
		Value:          nil,
		Candidates:     []string{"near Ikeja", "near Maryland"},
		EvidenceQuotes: []string{"near Ikeja or near Maryland"},
	}

	output, err := (RuleBasedNormalizer{}).Normalize(context.Background(), input)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if strings.Contains(string(output), "latitude") || strings.Contains(string(output), "longitude") || strings.Contains(string(output), "coordinates") {
		t.Fatalf("normalization output contains unsupported coordinate data: %s", output)
	}
	normalized := validateTestOutput(t, output)
	if normalized.LocationState != "ambiguous" || len(normalized.Candidates) != 2 {
		t.Fatalf("ambiguous output = %+v, want two textual candidates", normalized)
	}
	if normalized.Candidates[0].EvidenceQuotes[0] != "near Ikeja or near Maryland" {
		t.Fatalf("candidate evidence = %v, want original ambiguous quote", normalized.Candidates[0].EvidenceQuotes)
	}
}

func TestRuleBasedNormalizerPreservesUnknownLocation(t *testing.T) {
	input := extraction.Field{
		Status:         "unknown",
		Value:          nil,
		Candidates:     []string{},
		EvidenceQuotes: []string{},
	}

	output, err := (RuleBasedNormalizer{}).Normalize(context.Background(), input)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	normalized := validateTestOutput(t, output)
	if normalized.LocationState != "unknown" || len(normalized.Candidates) != 0 {
		t.Fatalf("unknown output = %+v, want unknown with no candidates", normalized)
	}
	if normalized.SourceLocationReference.Status != "unknown" || normalized.SourceLocationReference.EvidenceQuotes == nil {
		t.Fatalf("unknown source reference = %+v, want explicit unknown state", normalized.SourceLocationReference)
	}
}

func TestProcessorValidatesInjectedLocationOutput(t *testing.T) {
	processor := Processor{
		Provider: ProviderFunc(func(context.Context, extraction.Field) ([]byte, error) {
			return []byte(`{"contract_version":"signa.location-normalization.v0","input_contract_version":"signa.ai.report-extraction.v0","location_state":"identified","source_location_reference":{"status":"identified","value":"Ikeja","candidates":[],"evidence_quotes":["Ikeja"]},"candidates":[{"reported_text":"Ikeja","normalized_text":"Ikeja","reference_kind":"named_place","qualifier":null,"evidence_quotes":["Ikeja"]}]}`), nil
		}),
		Validator: testValidator(t),
	}
	result, err := processor.Process(context.Background(), extraction.Field{
		Status:         "identified",
		Value:          stringPointer("Ikeja"),
		Candidates:     []string{},
		EvidenceQuotes: []string{"Ikeja"},
	})
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if result.Candidates[0].NormalizedText != "Ikeja" {
		t.Fatalf("injected result = %+v, want validated candidate", result.Candidates[0])
	}
}

func TestValidatorRejectsStateReferenceMismatch(t *testing.T) {
	data := []byte(`{"contract_version":"signa.location-normalization.v0","input_contract_version":"signa.ai.report-extraction.v0","location_state":"identified","source_location_reference":{"status":"unknown","value":null,"candidates":[],"evidence_quotes":[]},"candidates":[{"reported_text":"Ikeja","normalized_text":"Ikeja","reference_kind":"named_place","qualifier":null,"evidence_quotes":["Ikeja"]}]}`)
	if _, err := testValidator(t).Validate(data); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("Validate() error = %v, want state/reference mismatch", err)
	}
}

func validateTestOutput(t *testing.T, data []byte) Normalization {
	t.Helper()
	normalized, err := testValidator(t).Validate(data)
	if err != nil {
		t.Fatalf("Validate() error = %v; output = %s", err, data)
	}
	return normalized
}

func testValidator(t *testing.T) *Validator {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	schemaPath := filepath.Join(filepath.Dir(file), "..", "..", "..", "contracts", "ai", "location-normalization", "v0", "schema.json")
	schema, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	validator, err := NewValidator(schema)
	if err != nil {
		t.Fatalf("NewValidator() error = %v", err)
	}
	return validator
}

func stringPointer(value string) *string {
	return &value
}

func TestNormalizationTypesMarshalAsContract(t *testing.T) {
	value := Normalization{
		ContractVersion:      ContractVersion,
		InputContractVersion: "signa.ai.report-extraction.v0",
		LocationState:        "unknown",
		SourceLocationReference: extraction.Field{
			Status:         "unknown",
			Value:          nil,
			Candidates:     []string{},
			EvidenceQuotes: []string{},
		},
		Candidates: []Candidate{},
	}
	if _, err := json.Marshal(value); err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
}
