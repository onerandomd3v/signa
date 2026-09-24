// Package confidence contains the deterministic confidence and severity rules
// for incident evidence. It never treats an AI score as incident confidence.
package confidence

import (
	"fmt"
	"strings"
)

const PolicyVersion = "signa.incident-confidence-severity.v1"

type ConfidenceState string

const (
	Unverified     ConfidenceState = "UNVERIFIED"
	Emerging       ConfidenceState = "EMERGING"
	Corroborated   ConfidenceState = "CORROBORATED"
	HighConfidence ConfidenceState = "HIGH_CONFIDENCE"
	Disputed       ConfidenceState = "DISPUTED"
)

type Severity string

const (
	Low      Severity = "LOW"
	Moderate Severity = "MODERATE"
	High     Severity = "HIGH"
	Critical Severity = "CRITICAL"
)

type Policy struct {
	EmergingMin     int
	CorroboratedMin int
	HighMin         int
}

type Evidence struct {
	ID                 string
	IndependenceState  string
	ContradictionState string
	SeverityStatus     string
	SeverityCandidate  string
}

type Result struct {
	ConfidenceState           ConfidenceState
	Severity                  *Severity
	AttachedReportCount       int
	EffectiveIndependentCount int
	RepeatedEvidenceCount     int
	ContradictionState        string
	CoordinationState         string
}

func (p Policy) Validate() error {
	if p.EmergingMin <= 0 || p.CorroboratedMin <= 0 || p.HighMin <= 0 {
		return fmt.Errorf("confidence evidence thresholds must be positive")
	}
	if p.EmergingMin >= p.CorroboratedMin || p.CorroboratedMin >= p.HighMin {
		return fmt.Errorf("confidence evidence thresholds must be strictly increasing")
	}
	return nil
}

func Evaluate(policy Policy, evidence []Evidence) (Result, error) {
	return EvaluateWithCoordination(policy, evidence, "indeterminate")
}

func EvaluateWithCoordination(policy Policy, evidence []Evidence, coordinationState string) (Result, error) {
	if err := policy.Validate(); err != nil {
		return Result{}, err
	}
	result := Result{AttachedReportCount: len(evidence)}
	seen := make(map[string]struct{}, len(evidence))
	severityRank := 0
	for _, item := range evidence {
		if item.ID == "" {
			return Result{}, fmt.Errorf("evidence id is required")
		}
		if _, duplicate := seen[item.ID]; duplicate {
			continue
		}
		seen[item.ID] = struct{}{}

		if len(seen) == 1 {
			result.EffectiveIndependentCount++
		} else {
			switch item.IndependenceState {
			case "independence_supported":
				result.EffectiveIndependentCount++
			case "repetition_risk", "mixed_signals", "indeterminate", "", "no_signal":
				result.RepeatedEvidenceCount++
			}
		}

		if item.ContradictionState == "contradiction_signal" {
			result.ContradictionState = "contradiction_signal"
		}
		if item.SeverityStatus == "identified" {
			if candidate, ok := parseSeverity(item.SeverityCandidate); ok && severityRank < candidate.rank() {
				severityRank = candidate.rank()
				value := candidate
				result.Severity = &value
			}
		}
	}
	result.CoordinationState = coordinationState

	switch {
	case result.ContradictionState == "contradiction_signal":
		result.ConfidenceState = Disputed
	case result.EffectiveIndependentCount >= policy.HighMin:
		result.ConfidenceState = HighConfidence
	case result.EffectiveIndependentCount >= policy.CorroboratedMin:
		result.ConfidenceState = Corroborated
	case result.EffectiveIndependentCount >= policy.EmergingMin:
		result.ConfidenceState = Emerging
	default:
		result.ConfidenceState = Unverified
	}
	return result, nil
}

func parseSeverity(value string) (Severity, bool) {
	switch Severity(strings.ToUpper(strings.TrimSpace(value))) {
	case Low:
		return Low, true
	case Moderate:
		return Moderate, true
	case High:
		return High, true
	case Critical:
		return Critical, true
	default:
		return "", false
	}
}

func (s Severity) rank() int {
	switch s {
	case Low:
		return 1
	case Moderate:
		return 2
	case High:
		return 3
	case Critical:
		return 4
	default:
		return 0
	}
}
