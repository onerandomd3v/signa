package alertsummary_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
		{"freshness mismatch", func(s *alertsummary.Summary) { s.Freshness.Status = alertsummary.UnknownFreshness }},
		{"policy version mismatch", func(s *alertsummary.Summary) { s.PolicyVersions.Confidence = "other-policy" }},
		{"confirmed claim", func(s *alertsummary.Summary) { s.Message = "This incident is confirmed." }},
		{"verified claim", func(s *alertsummary.Summary) { s.Message = "This incident is verified." }},
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

func TestValidateBoundRejectsUnsupportedAssurancesIndividually(t *testing.T) {
	state := validState()
	for _, phrase := range []string{"The road is safe now.", "There is no danger.", "There is no immediate danger.", "There is no risk.", "There is nothing to worry about."} {
		t.Run(phrase, func(t *testing.T) {
			candidate := alertsummary.RenderDeterministic(state)
			candidate.Message = phrase
			if err := alertsummary.ValidateBound(state, candidate); err == nil {
				t.Fatalf("accepted unsupported assurance %q", phrase)
			}
		})
	}
}

func TestDisputedAsFactIsRejectedWithMatchingAuthoritativeConfidence(t *testing.T) {
	state := validState()
	state.ConfidenceState = alertsummary.Disputed
	candidate := alertsummary.RenderDeterministic(state)
	candidate.Message = "This incident is a fact."
	if err := alertsummary.ValidateBound(state, candidate); err == nil {
		t.Fatal("accepted disputed incident stated as fact")
	}
}

func TestValidateStateRejectsExactCoordinatesInPublicLocation(t *testing.T) {
	state := validState()
	state.PublicLocation.Label = stringPointer("6.524379, 3.379206")
	if err := alertsummary.ValidateState(state); err == nil {
		t.Fatal("accepted exact coordinates in public_location.label")
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
	state.Freshness.LastSignalAt = timePointer(state.AsOf.Add(-48 * time.Hour))
	state.Freshness.AgeSeconds = intPointer(48 * 60 * 60)
	candidate := alertsummary.RenderDeterministic(state)
	candidate.Message = "The incident is happening now."
	if err := alertsummary.ValidateBound(state, candidate); err == nil {
		t.Fatal("accepted stale current-activity claim")
	}
}

func TestValidateBoundRejectsInventedProseWithMatchingMetadata(t *testing.T) {
	tests := []struct {
		name    string
		state   alertsummary.State
		message string
	}{
		{"another event type", validState(), "A fire is reported near a junction."},
		{"another location", validState(), "Possible road blockage near Ikeja."},
		{"stronger confidence", validState(), "High confidence incident near a junction."},
		{"current activity for old signal", oldTimestampState(), "The incident is happening now near a junction."},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			candidate := alertsummary.RenderDeterministic(tc.state)
			candidate.Message = tc.message
			if err := alertsummary.ValidateBound(tc.state, candidate); err == nil {
				t.Fatalf("accepted invented public prose: %q", tc.message)
			}
		})
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

func TestValidateStateRequiresConsistentKnownFreshness(t *testing.T) {
	state := validState()
	state.Freshness.LastSignalAt = nil
	if err := alertsummary.ValidateState(state); err == nil {
		t.Fatal("accepted known freshness without last_signal_at")
	}
	state = validState()
	state.Freshness.AgeSeconds = nil
	if err := alertsummary.ValidateState(state); err == nil {
		t.Fatal("accepted known freshness without age_seconds")
	}
	state = validState()
	state.Freshness.Status = alertsummary.UnknownFreshness
	if err := alertsummary.ValidateState(state); err == nil {
		t.Fatal("accepted unknown freshness with known timestamps")
	}
}

func TestValidateStateAcceptsUnknownFreshnessWithoutTimestamps(t *testing.T) {
	state := validState()
	state.Freshness = alertsummary.Freshness{Status: alertsummary.UnknownFreshness}
	if err := alertsummary.ValidateState(state); err != nil {
		t.Fatalf("rejected unknown freshness: %v", err)
	}
}

func TestApprovedSnapshotContractStatusesAndVersion(t *testing.T) {
	if alertsummary.SnapshotVersion != "signa.alert-summary-snapshot.v1" {
		t.Fatalf("snapshot version = %q", alertsummary.SnapshotVersion)
	}
	state := validState()
	state.PublicLocation = alertsummary.PublicLocation{Status: "unknown"}
	if err := alertsummary.ValidateState(state); err != nil {
		t.Fatalf("rejected unknown location: %v", err)
	}
	state = validState()
	state.PublicLocation.Status = "APPROXIMATE"
	if err := alertsummary.ValidateState(state); err == nil {
		t.Fatal("accepted unapproved public location status")
	}
	state = validState()
	state.Freshness.Status = "STALE"
	if err := alertsummary.ValidateState(state); err == nil {
		t.Fatal("accepted derived freshness state")
	}
	state = validState()
	state.LifecycleStatus = "UNKNOWN"
	if err := alertsummary.ValidateState(state); err == nil {
		t.Fatal("accepted unapproved lifecycle status")
	}
}

func TestOutputSchemaRejectsPreviousSnapshotVersion(t *testing.T) {
	summary := alertsummary.RenderDeterministic(validState())
	summary.SnapshotVersion = "signa.incident-alert-snapshot.v1"
	data, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := testValidator(t).Validate(data); err == nil {
		t.Fatal("accepted previous snapshot version")
	}
}

func TestRendererUsesSuppliedFreshnessWithoutDerivingThresholds(t *testing.T) {
	state := oldTimestampState()
	summary := alertsummary.RenderDeterministic(state)
	if !strings.Contains(summary.Message, "Last signal recorded at 2026-09-23T12:00:00Z") {
		t.Fatalf("summary omitted supplied old timestamp: %q", summary.Message)
	}
	if strings.Contains(summary.Message, "ongoing") || strings.Contains(summary.Message, "happening now") {
		t.Fatalf("summary implied current activity: %q", summary.Message)
	}
	state.Freshness = alertsummary.Freshness{Status: alertsummary.UnknownFreshness}
	summary = alertsummary.RenderDeterministic(state)
	if !strings.Contains(summary.Message, "Freshness is unknown") {
		t.Fatalf("summary did not preserve unknown freshness: %q", summary.Message)
	}
}

func TestValidateStatePreservesCanonicalUnknownEventType(t *testing.T) {
	state := validState()
	state.EventType = alertsummary.EventTypeUnknown
	if err := alertsummary.ValidateState(state); err != nil {
		t.Fatalf("rejected canonical unknown event type: %v", err)
	}
	state.EventType = "unknown event"
	if err := alertsummary.ValidateState(state); err == nil {
		t.Fatal("accepted non-canonical event type")
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
	return alertsummary.State{SnapshotVersion: alertsummary.SnapshotVersion, IncidentID: "inc-valid", EventType: "possible_road_blockage", ConfidenceState: alertsummary.Unverified, Severity: stringPointer("HIGH"), LifecycleStatus: "OPEN", PublicLocation: alertsummary.PublicLocation{Status: "identified", Label: stringPointer("near a junction")}, Freshness: alertsummary.Freshness{Status: alertsummary.KnownFreshness, LastSignalAt: &last, AgeSeconds: &age}, AsOf: asOf, PolicyVersions: alertsummary.PolicyVersions{Confidence: "signa.incident-confidence-severity.v1", Lifecycle: "signa.incident-lifecycle.v1"}}
}

func oldTimestampState() alertsummary.State {
	state := validState()
	state.Freshness.LastSignalAt = timePointer(state.AsOf.Add(-48 * time.Hour))
	age := int64(48 * 60 * 60)
	state.Freshness.AgeSeconds = &age
	return state
}

func stringPointer(value string) *string     { return &value }
func timePointer(value time.Time) *time.Time { return &value }
func intPointer(value int64) *int64          { return &value }
