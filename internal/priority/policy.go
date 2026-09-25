// Package priority contains the deterministic per-user incident priority policy.
package priority

import (
	"fmt"
	"math"
	"time"
)

const PolicyVersion = "signa.priority.v1"

type Level string

const (
	P1   Level = "P1"
	P2   Level = "P2"
	P3   Level = "P3"
	None Level = "NONE"
)

const (
	StatusOpen      = "OPEN"
	StatusResolving = "RESOLVING"
	StatusResolved  = "RESOLVED"
	StatusExpired   = "EXPIRED"
)

const (
	ConfidenceUnverified   = "UNVERIFIED"
	ConfidenceEmerging     = "EMERGING"
	ConfidenceCorroborated = "CORROBORATED"
	ConfidenceHigh         = "HIGH_CONFIDENCE"
	ConfidenceDisputed     = "DISPUTED"
	SeverityLow            = "LOW"
	SeverityModerate       = "MODERATE"
	SeverityHigh           = "HIGH"
	SeverityCritical       = "CRITICAL"
)

type Policy struct {
	P1RadiusMeters float64
	P2RadiusMeters float64
	LocationMaxAge time.Duration
	IncidentMaxAge time.Duration
}

type Incident struct {
	Status     string
	ActivityAt time.Time
	Severity   string
	Confidence string
}

type Proximity struct {
	DistanceMeters float64
	AccuracyMeters *float64
	ObservedAt     time.Time
}

type Input struct {
	Now       time.Time
	Incident  Incident
	Proximity Proximity
}

type Decision struct {
	Level   Level
	Reasons []string
}

func (p Policy) Validate() error {
	if !finitePositive(p.P1RadiusMeters) || !finitePositive(p.P2RadiusMeters) {
		return fmt.Errorf("priority radii must be finite and positive")
	}
	if p.P1RadiusMeters >= p.P2RadiusMeters {
		return fmt.Errorf("priority radii must satisfy P1 < P2")
	}
	if p.LocationMaxAge <= 0 || p.IncidentMaxAge <= 0 {
		return fmt.Errorf("priority freshness durations must be positive")
	}
	return nil
}

func Evaluate(policy Policy, input Input) (Decision, error) {
	if err := policy.Validate(); err != nil {
		return Decision{}, err
	}
	if input.Now.IsZero() || input.Incident.ActivityAt.IsZero() || input.Proximity.ObservedAt.IsZero() {
		return Decision{}, fmt.Errorf("priority evaluation times are required")
	}
	if !finiteNonNegative(input.Proximity.DistanceMeters) {
		return Decision{}, fmt.Errorf("proximity distance must be finite and non-negative")
	}
	if input.Proximity.AccuracyMeters != nil && !finiteNonNegative(*input.Proximity.AccuracyMeters) {
		return Decision{}, fmt.Errorf("proximity accuracy must be finite and non-negative")
	}

	now := input.Now.UTC()
	incidentAge := now.Sub(input.Incident.ActivityAt.UTC())
	if incidentAge < 0 || incidentAge > policy.IncidentMaxAge {
		return none("incident_stale"), nil
	}
	locationAge := now.Sub(input.Proximity.ObservedAt.UTC())
	if locationAge < 0 || locationAge > policy.LocationMaxAge {
		return none("location_stale"), nil
	}
	if input.Incident.Status == StatusResolved || input.Incident.Status == StatusExpired {
		return none("incident_terminal"), nil
	}
	if input.Incident.Status != StatusOpen && input.Incident.Status != StatusResolving {
		return Decision{}, fmt.Errorf("unsupported incident status %q", input.Incident.Status)
	}
	if !knownSeverity(input.Incident.Severity) {
		return none("severity_unknown"), nil
	}
	if !knownConfidence(input.Incident.Confidence) {
		return none("confidence_unknown"), nil
	}

	knownAccuracy := input.Proximity.AccuracyMeters != nil
	accuracy := 0.0
	if knownAccuracy {
		accuracy = *input.Proximity.AccuracyMeters
	}
	definitelyWithinP1 := knownAccuracy && input.Proximity.DistanceMeters+accuracy <= policy.P1RadiusMeters
	definitelyWithinP2 := knownAccuracy && input.Proximity.DistanceMeters+accuracy <= policy.P2RadiusMeters
	possiblyWithinP2 := input.Proximity.DistanceMeters-accuracy <= policy.P2RadiusMeters
	reasons := []string{"incident_active", "incident_fresh", "location_fresh"}
	if !knownAccuracy {
		reasons = append(reasons, "location_uncertain")
	}
	if definitelyWithinP1 && highImmediateSeverity(input.Incident.Severity) && immediateConfidence(input.Incident.Confidence) {
		return Decision{Level: P1, Reasons: append(reasons, "within_p1_radius", severityReason(input.Incident.Severity), confidenceReason(input.Incident.Confidence))}, nil
	}
	if definitelyWithinP2 && nearbySeverity(input.Incident.Severity) && nearbyConfidence(input.Incident.Confidence) {
		return Decision{Level: P2, Reasons: append(reasons, "within_p2_radius", severityReason(input.Incident.Severity), confidenceReason(input.Incident.Confidence))}, nil
	}
	if possiblyWithinP2 {
		if knownAccuracy && definitelyWithinP2 {
			reasons = append(reasons, "within_p2_radius")
		} else {
			if !knownAccuracy {
				// Unknown accuracy is conservatively treated as a raw-radius
				// candidate, but never as a definite spatial match.
				reasons = append(reasons, "possibly_within_p2_radius")
			} else {
				reasons = append(reasons, "location_uncertain", "possibly_within_p2_radius")
			}
		}
		if input.Incident.Confidence == ConfidenceDisputed {
			reasons = append(reasons, "confidence_disputed")
		} else {
			reasons = append(reasons, severityReason(input.Incident.Severity), confidenceReason(input.Incident.Confidence))
		}
		return Decision{Level: P3, Reasons: reasons}, nil
	}
	return none("outside_p2_radius"), nil
}

func none(reason string) Decision { return Decision{Level: None, Reasons: []string{reason}} }

func highImmediateSeverity(value string) bool {
	return value == SeverityHigh || value == SeverityCritical
}

func nearbySeverity(value string) bool {
	return value == SeverityModerate || highImmediateSeverity(value)
}

func immediateConfidence(value string) bool {
	return value == ConfidenceEmerging || value == ConfidenceCorroborated || value == ConfidenceHigh
}

func nearbyConfidence(value string) bool { return immediateConfidence(value) }

func knownSeverity(value string) bool {
	return value == SeverityLow || nearbySeverity(value)
}

func knownConfidence(value string) bool {
	return value == ConfidenceUnverified || immediateConfidence(value) || value == ConfidenceDisputed
}

func severityReason(value string) string {
	switch value {
	case SeverityCritical:
		return "severity_critical"
	case SeverityHigh:
		return "severity_high"
	default:
		return "severity_" + lower(value)
	}
}

func confidenceReason(value string) string {
	switch value {
	case ConfidenceEmerging:
		return "confidence_emerging"
	case ConfidenceCorroborated:
		return "confidence_corroborated"
	case ConfidenceHigh:
		return "confidence_high"
	default:
		return "confidence_" + lower(value)
	}
}

func lower(value string) string {
	result := make([]byte, len(value))
	for i := range value {
		if value[i] >= 'A' && value[i] <= 'Z' {
			result[i] = value[i] + ('a' - 'A')
		} else {
			result[i] = value[i]
		}
	}
	return string(result)
}

func finitePositive(value float64) bool { return finiteNonNegative(value) && value > 0 }

func finiteNonNegative(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}
