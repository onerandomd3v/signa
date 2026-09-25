package priority

import "fmt"

// RoutePolicyVersion identifies the route-aware extension of the v1 policy.
// Evaluate remains the stable signa.priority.v1 evaluator.
const RoutePolicyVersion = "signa.priority.v2"

type RouteRelevance string

const (
	RouteUnknown     RouteRelevance = "UNKNOWN"
	RouteNotRelevant RouteRelevance = "NOT_RELEVANT"
	RouteRelevant    RouteRelevance = "RELEVANT"
)

// EvaluateRoute applies route relevance after the v1 decision. It receives
// only the spatial layer's categorical result; route coordinates never cross
// into the pure priority policy.
func EvaluateRoute(policy Policy, input Input, relevance RouteRelevance) (Decision, error) {
	if relevance != RouteUnknown && relevance != RouteNotRelevant && relevance != RouteRelevant {
		return Decision{}, fmt.Errorf("unsupported route relevance %q", relevance)
	}
	base, err := Evaluate(policy, input)
	if err != nil {
		return Decision{}, err
	}

	switch relevance {
	case RouteUnknown:
		base.Reasons = append(base.Reasons, "route_relevance_unknown")
		return base, nil
	case RouteNotRelevant:
		base.Reasons = append(base.Reasons, "route_not_relevant")
		return base, nil
	case RouteRelevant:
		base.Reasons = append(base.Reasons, "route_relevant")
	}

	if base.Level == P1 || base.Level == P2 {
		return base, nil
	}
	if !routeEligible(policy, input) {
		return base, nil
	}

	promotable := input.Incident.Confidence != ConfidenceDisputed && nearbySeverity(input.Incident.Severity) && nearbyConfidence(input.Incident.Confidence)
	if promotable {
		base.Level = P2
		base.Reasons = append(base.Reasons, "route_promoted_to_p2")
		return base, nil
	}
	if base.Level == None {
		base.Level = P3
		base.Reasons = append(base.Reasons, "route_relevance_p3")
		if input.Incident.Confidence == ConfidenceDisputed {
			base.Reasons = append(base.Reasons, "confidence_disputed")
		}
	}
	return base, nil
}

func routeEligible(policy Policy, input Input) bool {
	now := input.Now.UTC()
	incidentAge := now.Sub(input.Incident.ActivityAt.UTC())
	if incidentAge < 0 || incidentAge > policy.IncidentMaxAge {
		return false
	}
	locationAge := now.Sub(input.Proximity.ObservedAt.UTC())
	if locationAge < 0 || locationAge > policy.LocationMaxAge {
		return false
	}
	if input.Incident.Status != StatusOpen && input.Incident.Status != StatusResolving {
		return false
	}
	return knownSeverity(input.Incident.Severity) && knownConfidence(input.Incident.Confidence)
}
