package alertsummary_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

func TestDeterministicEvaluationCoversSafetyCategories(t *testing.T) {
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

func TestAuthoritativeFieldsAndUnsafeWordingAreRejected(t *testing.T) {
	state := alertsummary.State{EventLabel: "Possible incident", ConfidenceState: alertsummary.Unverified, Severity: stringPointer("CRITICAL"), Freshness: alertsummary.Current, LifecycleStatus: "OPEN", Location: alertsummary.Location{Status: "APPROXIMATE", Label: stringPointer("near a junction")}}
	validator := testValidator(t)
	bad := alertsummary.RenderDeterministic(state)
	bad.Message = "Confirmed incident happening now at 12.345, 3.456."
	bad.UncertaintyQualifier = "confirmed"
	if err := alertsummary.ValidateBound(state, bad); err == nil {
		t.Fatal("ValidateBound accepted false certainty and exact coordinates")
	}
	bad = alertsummary.RenderDeterministic(state)
	bad.ConfidenceState = alertsummary.HighConfidence
	if err := alertsummary.ValidateBound(state, bad); err == nil {
		t.Fatal("ValidateBound accepted provider confidence override")
	}
	if _, err := alertsummary.Summarize(context.Background(), state, alertsummary.ProviderFunc(func(context.Context, alertsummary.State) ([]byte, error) { b, _ := json.Marshal(bad); return b, nil }), validator); err == nil {
		t.Fatal("Summarize accepted unsafe provider output")
	}
}

func TestDisputedAndStaleWordingGuards(t *testing.T) {
	disputed := alertsummary.State{EventLabel: "Reported gunfire", ConfidenceState: alertsummary.Disputed, Severity: stringPointer("HIGH"), Freshness: alertsummary.Current, LifecycleStatus: "OPEN", Location: alertsummary.Location{Status: "UNKNOWN"}}
	s := alertsummary.RenderDeterministic(disputed)
	if !strings.Contains(strings.ToLower(s.Message), "uncertain") {
		t.Fatalf("disputed message = %q", s.Message)
	}
	stale := alertsummary.State{EventLabel: "Smoke", ConfidenceState: alertsummary.Corroborated, Freshness: alertsummary.Stale, LifecycleStatus: "OPEN", Location: alertsummary.Location{Status: "UNKNOWN"}}
	s = alertsummary.RenderDeterministic(stale)
	if strings.Contains(strings.ToLower(s.Message), "ongoing") {
		t.Fatalf("stale message implies ongoing: %q", s.Message)
	}
}

func stringPointer(s string) *string { return &s }
