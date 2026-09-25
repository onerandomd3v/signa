package alerts

import (
	"errors"
	"fmt"

	"github.com/onerandomd3v/signa/internal/priority"
)

const EligibilityPolicyVersion = "signa.alert-eligibility.v1"

type AlertType string

const (
	AlertTypeImmediate AlertType = "IMMEDIATE"
	AlertTypeNearby    AlertType = "NEARBY"
)

type FreshnessState string

const (
	FreshnessFresh   FreshnessState = "FRESH"
	FreshnessStale   FreshnessState = "STALE"
	FreshnessUnknown FreshnessState = "UNKNOWN"
)

type EligibilityInput struct {
	Status     string
	Freshness  FreshnessState
	Confidence string
	Severity   string
	Priority   priority.Level
}

type Decision struct {
	Eligible      bool
	AlertType     AlertType
	Reasons       []string
	PolicyVersion string
}

var ErrInvalidEligibilityInput = errors.New("alert eligibility input is invalid")

func EvaluateEligibility(input EligibilityInput) (Decision, error) {
	if err := validateEligibilityInput(input); err != nil {
		return Decision{}, err
	}
	if input.Status == priority.StatusResolved || input.Status == priority.StatusExpired {
		return ineligible("incident_terminal"), nil
	}
	if input.Freshness != FreshnessFresh {
		return ineligible("incident_not_fresh"), nil
	}
	reasons := []string{"incident_active", "incident_fresh"}
	if input.Priority == priority.P1 {
		if input.Severity != priority.SeverityHigh && input.Severity != priority.SeverityCritical {
			return ineligible("severity_not_urgent"), nil
		}
		if !eligibleConfidence(input.Confidence) {
			return ineligible("confidence_not_actionable"), nil
		}
		return eligible(AlertTypeImmediate, append(reasons, "priority_p1", severityReason(input.Severity), confidenceReason(input.Confidence))), nil
	}
	if input.Priority == priority.P2 {
		if input.Severity != priority.SeverityModerate && input.Severity != priority.SeverityHigh && input.Severity != priority.SeverityCritical {
			return ineligible("severity_not_actionable"), nil
		}
		if !eligibleConfidence(input.Confidence) {
			return ineligible("confidence_not_actionable"), nil
		}
		return eligible(AlertTypeNearby, append(reasons, "priority_p2", severityReason(input.Severity), confidenceReason(input.Confidence))), nil
	}
	return ineligible("priority_not_realtime"), nil
}

func validateEligibilityInput(input EligibilityInput) error {
	if input.Status != priority.StatusOpen && input.Status != priority.StatusResolving && input.Status != priority.StatusResolved && input.Status != priority.StatusExpired {
		return fmt.Errorf("%w: unsupported incident status %q", ErrInvalidEligibilityInput, input.Status)
	}
	if input.Freshness != FreshnessFresh && input.Freshness != FreshnessStale && input.Freshness != FreshnessUnknown {
		return fmt.Errorf("%w: unsupported freshness %q", ErrInvalidEligibilityInput, input.Freshness)
	}
	if input.Confidence != priority.ConfidenceUnverified && input.Confidence != priority.ConfidenceEmerging && input.Confidence != priority.ConfidenceCorroborated && input.Confidence != priority.ConfidenceHigh && input.Confidence != priority.ConfidenceDisputed {
		return fmt.Errorf("%w: unsupported confidence %q", ErrInvalidEligibilityInput, input.Confidence)
	}
	if input.Severity != priority.SeverityLow && input.Severity != priority.SeverityModerate && input.Severity != priority.SeverityHigh && input.Severity != priority.SeverityCritical {
		return fmt.Errorf("%w: unsupported severity %q", ErrInvalidEligibilityInput, input.Severity)
	}
	if input.Priority != priority.P1 && input.Priority != priority.P2 && input.Priority != priority.P3 && input.Priority != priority.None {
		return fmt.Errorf("%w: unsupported priority %q", ErrInvalidEligibilityInput, input.Priority)
	}
	return nil
}

func eligible(alertType AlertType, reasons []string) Decision {
	return Decision{Eligible: true, AlertType: alertType, Reasons: reasons, PolicyVersion: EligibilityPolicyVersion}
}

func ineligible(reason string) Decision {
	return Decision{Eligible: false, Reasons: []string{reason}, PolicyVersion: EligibilityPolicyVersion}
}

func eligibleConfidence(value string) bool {
	return value == priority.ConfidenceEmerging || value == priority.ConfidenceCorroborated || value == priority.ConfidenceHigh
}

func severityReason(value string) string { return "severity_" + lower(value) }

func confidenceReason(value string) string { return "confidence_" + lower(value) }

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
