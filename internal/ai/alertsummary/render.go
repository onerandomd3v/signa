package alertsummary

import "fmt"

// RenderDeterministic is the provider-free fallback. It only uses supplied state.
func RenderDeterministic(state State) Summary {
	label := state.EventLabel
	if label == "" {
		label = "Incident"
	}
	location := ""
	if state.Location.Label != nil && *state.Location.Label != "" {
		location = " near " + *state.Location.Label
	}
	qualifier := map[ConfidenceState]string{Unverified: "Unverified report", Emerging: "Early reports; details may change", Corroborated: "Corroborated reports", HighConfidence: "High confidence based on available reports", Disputed: "Reports conflict; details are uncertain"}[state.ConfidenceState]
	if qualifier == "" {
		qualifier = "Confidence unknown"
	}
	message := fmt.Sprintf("%s%s. %s.", label, location, qualifier)
	if state.Freshness == Stale {
		message += " This information may be out of date."
	}
	if state.Freshness == Aging {
		message += " Check the latest information before acting."
	}
	if state.LifecycleStatus == "RESOLVING" || state.LifecycleStatus == "RESOLVED" || state.LifecycleStatus == "EXPIRED" {
		message += " The incident is not described as current."
	}
	return Summary{ContractVersion: ContractVersion, Title: label, Message: message, UncertaintyQualifier: qualifier, ConfidenceState: state.ConfidenceState, Severity: state.Severity, Freshness: state.Freshness, LifecycleStatus: state.LifecycleStatus, Location: state.Location}
}
