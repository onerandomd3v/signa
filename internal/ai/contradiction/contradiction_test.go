package contradiction

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/onerandomd3v/signa/internal/ai/extraction"
	"github.com/onerandomd3v/signa/internal/ai/location"
)

func TestRuleBasedEvaluatorContradictionAndContext(t *testing.T) {
	older := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	newer := older.Add(20 * time.Minute)
	left := evidence("report-a", "road_closure", "Third Mainland Bridge", "passage", "blocked", older)
	right := evidence("report-b", "road_closure", "Third Mainland Bridge", "passage", "open", newer)

	data, err := (RuleBasedEvaluator{}).Evaluate(context.Background(), left, right)
	if err != nil {
		t.Fatal(err)
	}
	assessment, err := testValidator(t).Validate(data)
	if err != nil {
		t.Fatal(err)
	}
	if assessment.ContradictionState != "contradiction_signal" || assessment.RecencyOrder != "right_newer" {
		t.Fatalf("got state=%q recency=%q", assessment.ContradictionState, assessment.RecencyOrder)
	}
}

func TestRuleBasedEvaluatorPreservesUnknownAndNonComparableContext(t *testing.T) {
	tests := []struct {
		name  string
		right Evidence
		want  string
	}{
		{"unknown location", evidence("b", "road_closure", "", "passage", "open", time.Time{}), "indeterminate"},
		{"different place", evidence("b", "road_closure", "Ojuelegba", "passage", "open", time.Time{}), "not_comparable"},
		{"different event", evidence("b", "fire", "Third Mainland Bridge", "passage", "open", time.Time{}), "not_comparable"},
		{"same state", evidence("b", "road_closure", "Third Mainland Bridge", "passage", "blocked", time.Time{}), "no_signal"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			left := evidence("a", "road_closure", "Third Mainland Bridge", "passage", "blocked", time.Time{})
			data, err := (RuleBasedEvaluator{}).Evaluate(context.Background(), left, test.right)
			if err != nil {
				t.Fatal(err)
			}
			got, err := testValidator(t).Validate(data)
			if err != nil {
				t.Fatal(err)
			}
			if got.ContradictionState != test.want {
				t.Fatalf("state=%q, want %q", got.ContradictionState, test.want)
			}
		})
	}
}

func TestProcessorRejectsInvalidProviderOutput(t *testing.T) {
	input := evidence("a", "road_closure", "Third Mainland Bridge", "passage", "blocked", time.Time{})
	bad := []byte(`{"not":"a contradiction assessment"}`)
	processor := Processor{Provider: ProviderFunc(func(context.Context, Evidence, Evidence) ([]byte, error) { return bad, nil }), Validator: testValidator(t)}
	if _, err := processor.Evaluate(context.Background(), input, input); err == nil {
		t.Fatal("invalid provider output was accepted")
	}
}

func TestValidatorRejectsContradictionOutsideClaimFactor(t *testing.T) {
	data := []byte(`{"contract_version":"signa.ai.contradiction-assessment.v1","input_contract_version":"signa.ai.report-extraction.v0","left_evidence_id":"a","right_evidence_id":"b","contradiction_state":"contradiction_signal","recency_order":"unknown","factors":[{"name":"event_type","outcome":"contradiction","reason":"invalid factor outcome"},{"name":"location_reference","outcome":"no_signal","reason":"same place"},{"name":"claim_state","outcome":"contradiction","reason":"opposite claims"}]}`)
	if _, err := testValidator(t).Validate(data); err == nil {
		t.Fatal("schema/validator accepted contradiction outcome for event_type")
	}
}

func TestValidatorRejectsSignalWithIncomparableContext(t *testing.T) {
	data := []byte(`{"contract_version":"signa.ai.contradiction-assessment.v1","input_contract_version":"signa.ai.report-extraction.v0","left_evidence_id":"a","right_evidence_id":"b","contradiction_state":"contradiction_signal","recency_order":"unknown","factors":[{"name":"event_type","outcome":"no_signal","reason":"same type"},{"name":"location_reference","outcome":"not_comparable","reason":"different locations"},{"name":"claim_state","outcome":"contradiction","reason":"opposite claims"}]}`)
	if _, err := testValidator(t).Validate(data); err == nil {
		t.Fatal("validator accepted contradiction signal across incomparable locations")
	}
}

func testValidator(t *testing.T) *Validator {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	schema, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "contracts", "ai", "contradiction", "v1", "schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	validator, err := NewValidator(schema)
	if err != nil {
		t.Fatal(err)
	}
	return validator
}

func evidence(id, eventType, place, subject, state string, observedAt time.Time) Evidence {
	event := extraction.Field{Status: "identified", Value: stringPointer(eventType), Candidates: []string{}, EvidenceQuotes: []string{"reported event"}}
	locationField := extraction.Field{Status: "unknown", Candidates: []string{}, EvidenceQuotes: []string{}}
	locationState := "unknown"
	var candidates []location.Candidate
	if place != "" {
		locationField = extraction.Field{Status: "identified", Value: stringPointer(place), Candidates: []string{}, EvidenceQuotes: []string{place}}
		locationState = "identified"
		candidates = []location.Candidate{{ReportedText: place, NormalizedText: place, ReferenceKind: "landmark", Qualifier: nil, EvidenceQuotes: []string{place}}}
	}
	locationValue := location.Normalization{ContractVersion: location.ContractVersion, InputContractVersion: location.InputContractVersion, LocationState: locationState, SourceLocationReference: locationField, Candidates: candidates}
	claim := Claim{Subject: subject, State: state, EvidenceQuote: state}
	var timestamp *time.Time
	if !observedAt.IsZero() {
		copy := observedAt
		timestamp = &copy
	}
	return Evidence{ID: id, EventType: event, Location: locationValue, Claim: claim, ObservedAt: timestamp}
}

func stringPointer(value string) *string { return &value }

func TestContractTypesAreJSONStable(t *testing.T) {
	data, err := json.Marshal(evidence("a", "road_closure", "Third Mainland Bridge", "passage", "blocked", time.Time{}))
	if err != nil || len(data) == 0 {
		t.Fatalf("marshal evidence: %v", err)
	}
}
