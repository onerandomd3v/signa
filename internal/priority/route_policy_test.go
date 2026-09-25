package priority

import (
	"testing"
	"time"
)

func TestEvaluateRouteKeepsV1BehaviorWhenRouteIsNotRelevant(t *testing.T) {
	input := testInput()
	for _, test := range []struct {
		name       string
		relevance  RouteRelevance
		wantReason string
	}{
		{name: "not relevant", relevance: RouteNotRelevant, wantReason: "route_not_relevant"},
		{name: "unknown", relevance: RouteUnknown, wantReason: "route_relevance_unknown"},
	} {
		t.Run(test.name, func(t *testing.T) {
			decision, err := EvaluateRoute(testPolicy(), input, test.relevance)
			if err != nil {
				t.Fatal(err)
			}
			if decision.Level != P1 || !contains(decision.Reasons, test.wantReason) {
				t.Fatalf("decision = %+v, want v1 P1 with %q", decision, test.wantReason)
			}
		})
	}
}

func TestEvaluateRoutePromotesFartherActionableIncidentToP2(t *testing.T) {
	input := testInput()
	input.Proximity.DistanceMeters = 1000
	input.Proximity.AccuracyMeters = floatPtr(0)

	decision, err := EvaluateRoute(testPolicy(), input, RouteRelevant)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Level != P2 || !contains(decision.Reasons, "route_relevant") || !contains(decision.Reasons, "route_promoted_to_p2") {
		t.Fatalf("decision = %+v, want route-promoted P2", decision)
	}
}

func TestEvaluateRouteLeavesFartherIrrelevantIncidentAtV1Result(t *testing.T) {
	input := testInput()
	input.Proximity.DistanceMeters = 1000
	input.Proximity.AccuracyMeters = floatPtr(0)

	decision, err := EvaluateRoute(testPolicy(), input, RouteNotRelevant)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Level != None || !contains(decision.Reasons, "outside_p2_radius") {
		t.Fatalf("decision = %+v, want v1 NONE", decision)
	}
}

func TestEvaluateRouteRelevantDisputedIsAtMostP3(t *testing.T) {
	input := testInput()
	input.Incident.Confidence = ConfidenceDisputed
	input.Proximity.DistanceMeters = 1000
	input.Proximity.AccuracyMeters = floatPtr(0)

	decision, err := EvaluateRoute(testPolicy(), input, RouteRelevant)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Level != P3 || contains(decision.Reasons, "route_promoted_to_p2") || !contains(decision.Reasons, "confidence_disputed") {
		t.Fatalf("decision = %+v, want route-relevant disputed P3 at most", decision)
	}
}

func TestEvaluateRouteRelevantTerminalAndStaleRemainNone(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*Input)
	}{
		{name: "terminal", mutate: func(input *Input) { input.Incident.Status = StatusResolved }},
		{name: "stale", mutate: func(input *Input) { input.Incident.ActivityAt = input.Now.Add(-31 * time.Minute) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := testInput()
			input.Proximity.DistanceMeters = 1000
			input.Proximity.AccuracyMeters = floatPtr(0)
			test.mutate(&input)
			decision, err := EvaluateRoute(testPolicy(), input, RouteRelevant)
			if err != nil {
				t.Fatal(err)
			}
			if decision.Level != None {
				t.Fatalf("decision = %+v, want NONE", decision)
			}
		})
	}
}

func TestEvaluateRouteRankingFartherRelevantIncidentOutranksNearerIrrelevantIncident(t *testing.T) {
	nearIrrelevant := testInput()
	nearIrrelevant.Incident.Severity = SeverityLow
	nearIrrelevant.Proximity.DistanceMeters = 50
	nearIrrelevant.Proximity.AccuracyMeters = floatPtr(0)
	nearDecision, err := EvaluateRoute(testPolicy(), nearIrrelevant, RouteNotRelevant)
	if err != nil {
		t.Fatal(err)
	}

	farRelevant := testInput()
	farRelevant.Proximity.DistanceMeters = 1000
	farRelevant.Proximity.AccuracyMeters = floatPtr(0)
	farDecision, err := EvaluateRoute(testPolicy(), farRelevant, RouteRelevant)
	if err != nil {
		t.Fatal(err)
	}
	if nearDecision.Level != P3 || farDecision.Level != P2 {
		t.Fatalf("near decision = %+v, far decision = %+v, want P3 then P2", nearDecision, farDecision)
	}
}
