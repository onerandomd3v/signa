package contradiction

import (
	"context"
	"encoding/json"
	"errors"
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

func TestProcessorBindsProviderRecencyToInputTimestamps(t *testing.T) {
	base := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	earlier := base.Add(-time.Hour)
	later := base.Add(time.Hour)
	tests := []struct {
		name     string
		leftAt   *time.Time
		rightAt  *time.Time
		provider string
	}{
		{"rejects left_newer when right is newer", &earlier, &later, "left_newer"},
		{"rejects right_newer when left is newer", &later, &earlier, "right_newer"},
		{"missing timestamp requires unknown", &later, nil, "left_newer"},
		{"rejects same_time when timestamps differ", &earlier, &later, "same_time"},
		{"rejects wrong same_time result", &base, &base, "right_newer"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			left := evidence("left", "road_closure", "Third Mainland Bridge", "passage", "blocked", time.Time{})
			right := evidence("right", "road_closure", "Third Mainland Bridge", "passage", "open", time.Time{})
			left.ObservedAt, right.ObservedAt = test.leftAt, test.rightAt
			processor := Processor{Provider: providerWithRecency(t, test.provider), Validator: testValidator(t)}
			if _, err := processor.Evaluate(context.Background(), left, right); err == nil {
				t.Fatal("processor accepted provider recency inconsistent with input timestamps")
			}
		})
	}
}

func TestProcessorAcceptsCorrectProviderRecency(t *testing.T) {
	leftAt := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	rightAt := leftAt.Add(time.Minute)
	left := evidence("left", "road_closure", "Third Mainland Bridge", "passage", "blocked", time.Time{})
	right := evidence("right", "road_closure", "Third Mainland Bridge", "passage", "open", time.Time{})
	left.ObservedAt, right.ObservedAt = &leftAt, &rightAt
	processor := Processor{Provider: providerWithRecency(t, "right_newer"), Validator: testValidator(t)}
	assessment, err := processor.Evaluate(context.Background(), left, right)
	if err != nil {
		t.Fatalf("processor rejected matching provider recency: %v", err)
	}
	if assessment.RecencyOrder != "right_newer" {
		t.Fatalf("recency_order = %q, want right_newer", assessment.RecencyOrder)
	}
}

func TestProcessorRejectsSameEvidenceIdentity(t *testing.T) {
	item := evidence("same-report", "road_closure", "Third Mainland Bridge", "passage", "blocked", time.Time{})
	providerCalled := false
	processor := Processor{
		Provider: ProviderFunc(func(context.Context, Evidence, Evidence) ([]byte, error) {
			providerCalled = true
			return nil, errors.New("provider should not be called")
		}),
		Validator: testValidator(t),
	}
	if _, err := processor.Evaluate(context.Background(), item, item); err == nil {
		t.Fatal("processor accepted evidence compared with itself")
	}
	if providerCalled {
		t.Fatal("provider was called for identical evidence IDs")
	}
	if _, err := (RuleBasedEvaluator{}).Evaluate(context.Background(), item, item); err == nil {
		t.Fatal("rule-based evaluator accepted evidence compared with itself")
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

func providerWithRecency(t *testing.T, order string) Provider {
	t.Helper()
	return ProviderFunc(func(ctx context.Context, left, right Evidence) ([]byte, error) {
		data, err := (RuleBasedEvaluator{}).Evaluate(ctx, left, right)
		if err != nil {
			return nil, err
		}
		var result Assessment
		if err := json.Unmarshal(data, &result); err != nil {
			return nil, err
		}
		result.RecencyOrder = order
		return json.Marshal(result)
	})
}

func TestContractTypesAreJSONStable(t *testing.T) {
	data, err := json.Marshal(evidence("a", "road_closure", "Third Mainland Bridge", "passage", "blocked", time.Time{}))
	if err != nil || len(data) == 0 {
		t.Fatalf("marshal evidence: %v", err)
	}
}
