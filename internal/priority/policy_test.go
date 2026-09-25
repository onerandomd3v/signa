package priority

import (
	"reflect"
	"testing"
	"time"
)

func testPolicy() Policy {
	return Policy{P1RadiusMeters: 100, P2RadiusMeters: 500, LocationMaxAge: 10 * time.Minute, IncidentMaxAge: 30 * time.Minute}
}

func testInput() Input {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	accuracy := 10.0
	return Input{
		Now:       now,
		Incident:  Incident{Status: StatusOpen, ActivityAt: now.Add(-time.Minute), Severity: SeverityHigh, Confidence: ConfidenceEmerging},
		Proximity: Proximity{DistanceMeters: 50, AccuracyMeters: &accuracy, ObservedAt: now.Add(-time.Minute)},
	}
}

func TestEvaluatePriorityRuleTable(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*Input)
		wantLevel  Level
		wantReason string
	}{
		{"critical emerging can be p1", func(input *Input) { input.Incident.Severity = SeverityCritical }, P1, "within_p1_radius"},
		{"high corroborated is p1", func(input *Input) { input.Incident.Confidence = ConfidenceCorroborated }, P1, "confidence_corroborated"},
		{"moderate emerging is p2", func(input *Input) { input.Incident.Severity = SeverityModerate }, P2, "within_p2_radius"},
		{"low high confidence is p3", func(input *Input) { input.Incident.Severity = SeverityLow }, P3, "severity_low"},
		{"unverified high confidence-independent is p3", func(input *Input) { input.Incident.Confidence = ConfidenceUnverified }, P3, "confidence_unverified"},
		{"disputed is explicitly p3", func(input *Input) { input.Incident.Confidence = ConfidenceDisputed }, P3, "confidence_disputed"},
		{"terminal resolved is none", func(input *Input) { input.Incident.Status = StatusResolved }, None, "incident_terminal"},
		{"terminal expired is none", func(input *Input) { input.Incident.Status = StatusExpired }, None, "incident_terminal"},
		{"resolving remains active", func(input *Input) { input.Incident.Status = StatusResolving }, P1, "incident_active"},
		{"stale incident is none", func(input *Input) { input.Incident.ActivityAt = input.Now.Add(-31 * time.Minute) }, None, "incident_stale"},
		{"future incident is none", func(input *Input) { input.Incident.ActivityAt = input.Now.Add(time.Minute) }, None, "incident_stale"},
		{"stale user location is none", func(input *Input) { input.Proximity.ObservedAt = input.Now.Add(-11 * time.Minute) }, None, "location_stale"},
		{"future user location is none", func(input *Input) { input.Proximity.ObservedAt = input.Now.Add(time.Minute) }, None, "location_stale"},
		{"outside p2 is none", func(input *Input) { input.Proximity.DistanceMeters = 501; input.Proximity.AccuracyMeters = floatPtr(0) }, None, "outside_p2_radius"},
		{"unknown severity is none", func(input *Input) { input.Incident.Severity = "" }, None, "severity_unknown"},
		{"unknown confidence is none", func(input *Input) { input.Incident.Confidence = "UNKNOWN" }, None, "confidence_unknown"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			input := testInput()
			tc.mutate(&input)
			got, err := Evaluate(testPolicy(), input)
			if err != nil {
				t.Fatal(err)
			}
			if got.Level != tc.wantLevel {
				t.Fatalf("level = %s, want %s; reasons = %#v", got.Level, tc.wantLevel, got.Reasons)
			}
			if !contains(got.Reasons, tc.wantReason) {
				t.Fatalf("reasons = %#v, want %q", got.Reasons, tc.wantReason)
			}
		})
	}
}

func TestEvaluateAccuracyAwareBoundaries(t *testing.T) {
	tests := []struct {
		name      string
		distance  float64
		accuracy  *float64
		wantLevel Level
	}{
		{"p1 exact boundary with accuracy", 90, floatPtr(10), P1},
		{"p1 just outside due to accuracy", 91, floatPtr(10), P2},
		{"p2 exact boundary with accuracy", 490, floatPtr(10), P2},
		{"p2 just outside due to accuracy", 491, floatPtr(10), P3},
		{"unknown accuracy never p1", 50, nil, P3},
		{"zero accuracy uses exact boundary", 100, floatPtr(0), P1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			input := testInput()
			input.Proximity.DistanceMeters = tc.distance
			input.Proximity.AccuracyMeters = tc.accuracy
			got, err := Evaluate(testPolicy(), input)
			if err != nil || got.Level != tc.wantLevel {
				t.Fatalf("decision = (%+v, %v), want level %s", got, err, tc.wantLevel)
			}
		})
	}
}

func TestEvaluateEverySeverityAndConfidenceIsExplicit(t *testing.T) {
	for _, severity := range []string{SeverityLow, SeverityModerate, SeverityHigh, SeverityCritical} {
		for _, confidence := range []string{ConfidenceUnverified, ConfidenceEmerging, ConfidenceCorroborated, ConfidenceHigh, ConfidenceDisputed} {
			input := testInput()
			input.Incident.Severity = severity
			input.Incident.Confidence = confidence
			got, err := Evaluate(testPolicy(), input)
			if err != nil || got.Level == None {
				t.Fatalf("severity=%s confidence=%s decision=(%+v,%v), want eligible priority", severity, confidence, got, err)
			}
		}
	}
}

func TestEvaluateDoesNotCollapseSeverityAndConfidence(t *testing.T) {
	lowHigh, err := Evaluate(testPolicy(), func() Input {
		input := testInput()
		input.Incident.Severity = SeverityLow
		input.Incident.Confidence = ConfidenceHigh
		return input
	}())
	if err != nil || lowHigh.Level != P3 || contains(lowHigh.Reasons, "severity_high") {
		t.Fatalf("low/high decision = (%+v, %v), want P3 without high severity", lowHigh, err)
	}

	criticalEmerging, err := Evaluate(testPolicy(), func() Input {
		input := testInput()
		input.Incident.Severity = SeverityCritical
		input.Incident.Confidence = ConfidenceEmerging
		return input
	}())
	if err != nil || criticalEmerging.Level != P1 || !contains(criticalEmerging.Reasons, "confidence_emerging") {
		t.Fatalf("critical/emerging decision = (%+v, %v), want P1", criticalEmerging, err)
	}
}

func TestEvaluateReasonsAreDeterministic(t *testing.T) {
	input := testInput()
	want := Decision{Level: P1, Reasons: []string{"incident_active", "incident_fresh", "location_fresh", "within_p1_radius", "severity_high", "confidence_emerging"}}
	for i := 0; i < 3; i++ {
		got, err := Evaluate(testPolicy(), input)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("decision = (%+v, %v), want (%+v, nil)", got, err, want)
		}
	}
}

func TestEvaluateValidatesPolicyAndInput(t *testing.T) {
	if _, err := Evaluate(Policy{P1RadiusMeters: 500, P2RadiusMeters: 100, LocationMaxAge: time.Minute, IncidentMaxAge: time.Minute}, testInput()); err == nil {
		t.Fatal("expected invalid radius order")
	}
	input := testInput()
	input.Proximity.AccuracyMeters = floatPtr(-1)
	if _, err := Evaluate(testPolicy(), input); err == nil {
		t.Fatal("expected invalid accuracy")
	}
}

func floatPtr(value float64) *float64 { return &value }

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
