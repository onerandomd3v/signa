package alerts

import (
	"errors"
	"testing"

	"github.com/onerandomd3v/signa/internal/priority"
)

func TestEvaluateEligibilityMatrix(t *testing.T) {
	statuses := []string{priority.StatusOpen, priority.StatusResolving, priority.StatusResolved, priority.StatusExpired}
	freshnesses := []FreshnessState{FreshnessFresh, FreshnessStale, FreshnessUnknown}
	priorities := []priority.Level{priority.P1, priority.P2, priority.P3, priority.None}
	for _, status := range statuses {
		for _, freshness := range freshnesses {
			for _, level := range priorities {
				decision, err := EvaluateEligibility(EligibilityInput{Status: status, Freshness: freshness, Confidence: priority.ConfidenceEmerging, Severity: priority.SeverityCritical, Priority: level})
				if err != nil {
					t.Fatal(err)
				}
				want := status != priority.StatusResolved && status != priority.StatusExpired && freshness == FreshnessFresh && (level == priority.P1 || level == priority.P2)
				if decision.Eligible != want {
					t.Errorf("status=%s freshness=%s priority=%s eligible=%v, want %v", status, freshness, level, decision.Eligible, want)
				}
			}
		}
	}
}

func TestEvaluateEligibilityFullInputMatrix(t *testing.T) {
	statuses := []string{priority.StatusOpen, priority.StatusResolving, priority.StatusResolved, priority.StatusExpired}
	freshnesses := []FreshnessState{FreshnessFresh, FreshnessStale, FreshnessUnknown}
	confidences := []string{priority.ConfidenceUnverified, priority.ConfidenceEmerging, priority.ConfidenceCorroborated, priority.ConfidenceHigh, priority.ConfidenceDisputed}
	severities := []string{priority.SeverityLow, priority.SeverityModerate, priority.SeverityHigh, priority.SeverityCritical}
	levels := []priority.Level{priority.P1, priority.P2, priority.P3, priority.None}
	for _, status := range statuses {
		for _, freshness := range freshnesses {
			for _, confidence := range confidences {
				for _, severity := range severities {
					for _, level := range levels {
						decision, err := EvaluateEligibility(EligibilityInput{Status: status, Freshness: freshness, Confidence: confidence, Severity: severity, Priority: level})
						if err != nil {
							t.Fatal(err)
						}
						active := status == priority.StatusOpen || status == priority.StatusResolving
						actionableConfidence := confidence == priority.ConfidenceEmerging || confidence == priority.ConfidenceCorroborated || confidence == priority.ConfidenceHigh
						p1Rule := level == priority.P1 && (severity == priority.SeverityHigh || severity == priority.SeverityCritical)
						p2Rule := level == priority.P2 && (severity == priority.SeverityModerate || severity == priority.SeverityHigh || severity == priority.SeverityCritical)
						want := active && freshness == FreshnessFresh && actionableConfidence && (p1Rule || p2Rule)
						if decision.Eligible != want {
							t.Errorf("status=%s freshness=%s confidence=%s severity=%s priority=%s eligible=%v, want %v", status, freshness, confidence, severity, level, decision.Eligible, want)
						}
					}
				}
			}
		}
	}
}

func TestEvaluateEligibilityApprovedRegressions(t *testing.T) {
	tests := []struct {
		name  string
		input EligibilityInput
		want  bool
		type_ AlertType
	}{
		{"critical emerging p1 fresh", EligibilityInput{priority.StatusOpen, FreshnessFresh, priority.ConfidenceEmerging, priority.SeverityCritical, priority.P1}, true, AlertTypeImmediate},
		{"high corroborated p1", EligibilityInput{priority.StatusOpen, FreshnessFresh, priority.ConfidenceCorroborated, priority.SeverityHigh, priority.P1}, true, AlertTypeImmediate},
		{"moderate emerging p2", EligibilityInput{priority.StatusOpen, FreshnessFresh, priority.ConfidenceEmerging, priority.SeverityModerate, priority.P2}, true, AlertTypeNearby},
		{"p3", EligibilityInput{priority.StatusOpen, FreshnessFresh, priority.ConfidenceHigh, priority.SeverityCritical, priority.P3}, false, ""},
		{"none", EligibilityInput{priority.StatusOpen, FreshnessFresh, priority.ConfidenceHigh, priority.SeverityCritical, priority.None}, false, ""},
		{"disputed", EligibilityInput{priority.StatusOpen, FreshnessFresh, priority.ConfidenceDisputed, priority.SeverityCritical, priority.P1}, false, ""},
		{"unverified", EligibilityInput{priority.StatusOpen, FreshnessFresh, priority.ConfidenceUnverified, priority.SeverityCritical, priority.P1}, false, ""},
		{"resolved", EligibilityInput{priority.StatusResolved, FreshnessFresh, priority.ConfidenceHigh, priority.SeverityCritical, priority.P1}, false, ""},
		{"expired", EligibilityInput{priority.StatusExpired, FreshnessFresh, priority.ConfidenceHigh, priority.SeverityCritical, priority.P1}, false, ""},
		{"stale", EligibilityInput{priority.StatusOpen, FreshnessStale, priority.ConfidenceHigh, priority.SeverityCritical, priority.P1}, false, ""},
		{"unknown freshness", EligibilityInput{priority.StatusOpen, FreshnessUnknown, priority.ConfidenceHigh, priority.SeverityCritical, priority.P1}, false, ""},
		{"low severity", EligibilityInput{priority.StatusOpen, FreshnessFresh, priority.ConfidenceHigh, priority.SeverityLow, priority.P1}, false, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := EvaluateEligibility(test.input)
			if err != nil {
				t.Fatal(err)
			}
			if got.Eligible != test.want || got.AlertType != test.type_ || got.PolicyVersion != EligibilityPolicyVersion || len(got.Reasons) == 0 {
				t.Fatalf("decision = %+v", got)
			}
		})
	}
}

func TestEvaluateEligibilityRejectsUnknownValues(t *testing.T) {
	_, err := EvaluateEligibility(EligibilityInput{Status: "UNKNOWN", Freshness: FreshnessFresh, Confidence: priority.ConfidenceHigh, Severity: priority.SeverityHigh, Priority: priority.P1})
	if !errors.Is(err, ErrInvalidEligibilityInput) {
		t.Fatalf("error = %v", err)
	}
}
