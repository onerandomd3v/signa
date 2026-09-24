package confidence

import "testing"

func TestEvaluateConfidenceThresholds(t *testing.T) {
	policy := Policy{EmergingMin: 1, CorroboratedMin: 2, HighMin: 3}
	cases := []struct {
		name  string
		items []Evidence
		want  ConfidenceState
	}{
		{"unverified", nil, Unverified},
		{"emerging", []Evidence{{ID: "a"}}, Emerging},
		{"repetition_does_not_corrobate", []Evidence{{ID: "a"}, {ID: "b", IndependenceState: "repetition_risk"}}, Emerging},
		{"corroborated", []Evidence{{ID: "a"}, {ID: "b", IndependenceState: "independence_supported"}}, Corroborated},
		{"high_confidence", []Evidence{{ID: "a"}, {ID: "b", IndependenceState: "independence_supported"}, {ID: "c", IndependenceState: "independence_supported"}}, HighConfidence},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Evaluate(policy, tt.items)
			if err != nil {
				t.Fatal(err)
			}
			if got.ConfidenceState != tt.want {
				t.Fatalf("confidence = %q, want %q", got.ConfidenceState, tt.want)
			}
		})
	}
}

func TestEvaluateSignalsPreserveUncertainty(t *testing.T) {
	policy := Policy{EmergingMin: 1, CorroboratedMin: 2, HighMin: 3}
	got, err := Evaluate(policy, []Evidence{
		{ID: "a", SeverityStatus: "identified", SeverityCandidate: "CRITICAL"},
		{ID: "b", IndependenceState: "mixed_signals", CoordinationState: "possible_coordination", SeverityStatus: "ambiguous", SeverityCandidate: "HIGH"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.ConfidenceState != Emerging || got.Severity == nil || *got.Severity != Critical {
		t.Fatalf("result = %+v", got)
	}
	if got.CoordinationState != "possible_coordination" || got.RepeatedEvidenceCount != 1 {
		t.Fatalf("signals = %+v", got)
	}
}

func TestEvaluateContradictionDisputesWithoutChangingSeverity(t *testing.T) {
	policy := Policy{EmergingMin: 1, CorroboratedMin: 2, HighMin: 3}
	got, err := Evaluate(policy, []Evidence{
		{ID: "a", SeverityStatus: "identified", SeverityCandidate: "CRITICAL"},
		{ID: "b", IndependenceState: "independence_supported", ContradictionState: "contradiction_signal"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.ConfidenceState != Disputed || got.Severity == nil || *got.Severity != Critical {
		t.Fatalf("result = %+v", got)
	}
}

func TestPolicyRejectsInvalidThresholds(t *testing.T) {
	if _, err := Evaluate(Policy{EmergingMin: 2, CorroboratedMin: 2, HighMin: 3}, nil); err == nil {
		t.Fatal("expected threshold validation error")
	}
}
