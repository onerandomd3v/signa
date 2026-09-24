package alertsummary

import "fmt"

// RenderDeterministic is the provider-free fallback. It only uses the supplied
// authoritative snapshot and never decides eligibility or delivery.
func RenderDeterministic(state State) Summary {
	title := state.EventType
	if title == "" {
		title = "Incident"
	}
	location := ""
	if state.PublicLocation.Label != nil && *state.PublicLocation.Label != "" {
		location = " near " + *state.PublicLocation.Label
	}
	qualifier := map[ConfidenceState]string{Unverified: "Unverified report", Emerging: "Early reports; details may change", Corroborated: "Corroborated reports", HighConfidence: "High confidence based on available reports", Disputed: "Reports conflict; details are uncertain"}[state.ConfidenceState]
	if qualifier == "" {
		qualifier = "Confidence unknown"
	}
	message := fmt.Sprintf("%s%s. %s.", title, location, qualifier)
	if state.Freshness.State == Stale {
		message += " This information may be out of date."
	}
	if state.Freshness.State == Aging {
		message += " Check the latest information before acting."
	}
	if state.LifecycleStatus == "RESOLVING" || state.LifecycleStatus == "RESOLVED" || state.LifecycleStatus == "EXPIRED" {
		message += " The incident is not described as current."
	}
	return Summary{ContractVersion: ContractVersion, SnapshotVersion: state.SnapshotVersion, IncidentID: state.IncidentID, EventType: state.EventType, Title: title, Message: message, UncertaintyQualifier: qualifier, ConfidenceState: state.ConfidenceState, Severity: state.Severity, LifecycleStatus: state.LifecycleStatus, PublicLocation: state.PublicLocation, Freshness: state.Freshness, AsOf: state.AsOf, PolicyVersions: state.PolicyVersions}
}
