package alertsummary_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onerandomd3v/signa/internal/ai/alertsummary"
	"github.com/onerandomd3v/signa/internal/ai/alertsummary/evaluation"
)

func testValidator(t *testing.T) *alertsummary.Validator {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "ai", "alert-summarization", "v1", "schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	v, err := alertsummary.NewValidator(data)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestDeterministicEvaluationCoversAllScenarios(t *testing.T) {
	manifest := filepath.Join("..", "..", "..", "contracts", "ai", "alert-summarization", "v1", "evaluation.json")
	suite, err := evaluation.LoadSuite(manifest)
	if err != nil {
		t.Fatal(err)
	}
	report := evaluation.Evaluate(context.Background(), suite, evaluation.ProviderFunc(func(_ context.Context, state alertsummary.State) ([]byte, error) {
		return json.Marshal(alertsummary.RenderDeterministic(state))
	}), testValidator(t))
	if report.Failed() || report.Total != 11 || report.Passed != 11 {
		t.Fatalf("evaluation = %s; failures=%v", report, report.Failures)
	}
}

func TestValidateBoundRejectsEachUnsafeViolationIndividually(t *testing.T) {
	state := validState()
	base := alertsummary.RenderDeterministic(state)
	tests := []struct {
		name   string
		mutate func(*alertsummary.Summary)
	}{
		{"incident id mismatch", func(s *alertsummary.Summary) { s.IncidentID = "other" }},
		{"event type mismatch", func(s *alertsummary.Summary) { s.EventType = "other_event" }},
		{"confidence mismatch", func(s *alertsummary.Summary) { s.ConfidenceState = alertsummary.HighConfidence }},
		{"severity mismatch", func(s *alertsummary.Summary) { s.Severity = stringPointer("CRITICAL") }},
		{"location mismatch", func(s *alertsummary.Summary) { s.PublicLocation.Label = stringPointer("exact private address") }},
		{"freshness mismatch", func(s *alertsummary.Summary) { s.Freshness.State = alertsummary.Stale }},
		{"policy version mismatch", func(s *alertsummary.Summary) { s.PolicyVersions.Confidence = "other-policy" }},
		{"confirmed claim", func(s *alertsummary.Summary) { s.Message = "This incident is confirmed." }},
		{"verified claim", func(s *alertsummary.Summary) { s.Message = "This incident is verified." }},
		{"disputed as fact", func(s *alertsummary.Summary) {
			s.ConfidenceState = alertsummary.Disputed
			s.Message = "This is a fact."
		}},
		{"exact coordinates", func(s *alertsummary.Summary) { s.Message = "Reported at 6.524379, 3.379206." }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			candidate := base
			tc.mutate(&candidate)
			if err := alertsummary.ValidateBound(state, candidate); err == nil {
				t.Fatalf("ValidateBound accepted %s", tc.name)
			}
		})
	}
}

func TestValidateBoundRejectsUnsupportedOngoingClaims(t *testing.T) {
	for _, lifecycle := range []string{"RESOLVING", "RESOLVED", "EXPIRED"} {
		t.Run(lifecycle, func(t *testing.T) {
			state := validState()
			state.LifecycleStatus = lifecycle
			candidate := alertsummary.RenderDeterministic(state)
			candidate.Message = "The incident is ongoing."
			if err := alertsummary.ValidateBound(state, candidate); err == nil {
				t.Fatal("accepted unsupported ongoing claim")
			}
		})
	}
	state := validState()
	state.Freshness.State = alertsummary.Stale
	candidate := alertsummary.RenderDeterministic(state)
	candidate.Message = "The incident is happening now."
	if err := alertsummary.ValidateBound(state, candidate); err == nil {
		t.Fatal("accepted stale current-activity claim")
	}
}

func TestValidateBoundAcceptsValidSummary(t *testing.T) {
	state := validState()
	if err := alertsummary.ValidateState(state); err != nil {
		t.Fatal(err)
	}
	if err := alertsummary.ValidateBound(state, alertsummary.RenderDeterministic(state)); err != nil {
		t.Fatal(err)
	}
}

func TestValidateStateRejectsTimestampAgeMismatch(t *testing.T) {
	state := validState()
	age := int64(601)
	state.Freshness.AgeSeconds = &age
	if err := alertsummary.ValidateState(state); err == nil {
		t.Fatal("accepted inconsistent freshness age")
	}
}

func TestProviderEvaluationRejectsUnsafeAndMismatchedOutputs(t *testing.T) {
	validator := testValidator(t)
	state := validState()
	cases := []struct {
		name   string
		output func() alertsummary.Summary
	}{
		{"mismatched snapshot", func() alertsummary.Summary {
			s := alertsummary.RenderDeterministic(state)
			s.IncidentID = "wrong"
			return s
		}},
		{"unsupported confirmation", func() alertsummary.Summary {
			s := alertsummary.RenderDeterministic(state)
			s.Message = "Confirmed incident."
			return s
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := alertsummary.Summarize(context.Background(), state, alertsummary.ProviderFunc(func(context.Context, alertsummary.State) ([]byte, error) { return json.Marshal(tc.output()) }), validator)
			if err == nil {
				t.Fatal("accepted unsafe provider output")
			}
		})
	}
}

func validState() alertsummary.State {
	asOf := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	last := asOf.Add(-5 * time.Minute)
	age := int64(300)
	return alertsummary.State{SnapshotVersion: alertsummary.SnapshotVersion, IncidentID: "inc-valid", EventType: "possible_road_blockage", ConfidenceState: alertsummary.Unverified, Severity: stringPointer("HIGH"), LifecycleStatus: "OPEN", PublicLocation: alertsummary.PublicLocation{Status: "APPROXIMATE", Label: stringPointer("near a junction")}, Freshness: alertsummary.Freshness{State: alertsummary.Current, LastSignalAt: &last, AgeSeconds: &age}, AsOf: asOf, PolicyVersions: alertsummary.PolicyVersions{Confidence: "signa.incident-confidence-severity.v1", Lifecycle: "signa.incident-lifecycle.v1"}}
}

func stringPointer(value string) *string { return &value }
