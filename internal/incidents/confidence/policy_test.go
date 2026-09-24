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

func TestEvaluateCoordinationSuppressesAdditionalEvidence(t *testing.T) {
	policy := Policy{EmergingMin: 1, CorroboratedMin: 2, HighMin: 3}
	evidence := []Evidence{
		{ID: "a"},
		{ID: "b", IndependenceState: "independence_supported"},
		{ID: "c", IndependenceState: "independence_supported"},
	}

	noSignal, err := EvaluateWithCoordination(policy, evidence[:2], "no_signal")
	if err != nil {
		t.Fatal(err)
	}
	if noSignal.ConfidenceState != Corroborated || noSignal.EffectiveIndependentCount != 2 {
		t.Fatalf("no-signal result = %+v", noSignal)
	}

	noSignal, err = EvaluateWithCoordination(policy, evidence, "no_signal")
	if err != nil {
		t.Fatal(err)
	}
	if noSignal.ConfidenceState != HighConfidence || noSignal.EffectiveIndependentCount != 3 {
		t.Fatalf("no-signal high result = %+v", noSignal)
	}

	coordinated, err := EvaluateWithCoordination(policy, evidence, "possible_coordination")
	if err != nil {
		t.Fatal(err)
	}
	if coordinated.ConfidenceState != Emerging || coordinated.EffectiveIndependentCount != 1 || coordinated.RepeatedEvidenceCount != 2 {
		t.Fatalf("coordinated result = %+v", coordinated)
	}
	if coordinated.CoordinationState != "possible_coordination" {
		t.Fatalf("coordination state = %q", coordinated.CoordinationState)
	}
}

func TestEvaluateCoordinationDoesNotDisputeOrChangeSeverity(t *testing.T) {
	policy := Policy{EmergingMin: 1, CorroboratedMin: 2, HighMin: 3}
	got, err := EvaluateWithCoordination(policy, []Evidence{
		{ID: "a", SeverityStatus: "identified", SeverityCandidate: "CRITICAL"},
		{ID: "b", IndependenceState: "independence_supported", SeverityStatus: "identified", SeverityCandidate: "LOW"},
	}, "possible_coordination")
	if err != nil {
		t.Fatal(err)
	}
	if got.ConfidenceState != Emerging || got.ConfidenceState == Disputed || got.Severity == nil || *got.Severity != Critical {
		t.Fatalf("result = %+v", got)
	}
}

func TestEvaluateIndeterminateCoordinationDoesNotFabricateIndependence(t *testing.T) {
	policy := Policy{EmergingMin: 1, CorroboratedMin: 2, HighMin: 3}
	got, err := EvaluateWithCoordination(policy, []Evidence{
		{ID: "a"},
		{ID: "b", IndependenceState: "indeterminate"},
		{ID: "c", IndependenceState: "no_signal"},
	}, "indeterminate")
	if err != nil {
		t.Fatal(err)
	}
	if got.ConfidenceState != Emerging || got.EffectiveIndependentCount != 1 {
		t.Fatalf("result = %+v", got)
	}
}

func TestEvaluateSignalsPreserveUncertainty(t *testing.T) {
	policy := Policy{EmergingMin: 1, CorroboratedMin: 2, HighMin: 3}
	got, err := Evaluate(policy, []Evidence{
		{ID: "a", SeverityStatus: "identified", SeverityCandidate: "CRITICAL"},
		{ID: "b", IndependenceState: "mixed_signals", SeverityStatus: "ambiguous", SeverityCandidate: "HIGH"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.ConfidenceState != Emerging || got.Severity == nil || *got.Severity != Critical {
		t.Fatalf("result = %+v", got)
	}
	if got.RepeatedEvidenceCount != 1 {
		t.Fatalf("signals = %+v", got)
	}
	coordinated, err := EvaluateWithCoordination(policy, []Evidence{{ID: "a"}, {ID: "b", IndependenceState: "repetition_risk"}}, "possible_coordination")
	if err != nil || coordinated.ConfidenceState != Emerging || coordinated.CoordinationState != "possible_coordination" {
		t.Fatalf("coordination result = %+v, err = %v", coordinated, err)
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
