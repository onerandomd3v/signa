package alertsummary

import (
	"fmt"
	"time"
)

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
	if state.Freshness.Status == KnownFreshness && state.Freshness.LastSignalAt != nil {
		message += " Last signal recorded at " + state.Freshness.LastSignalAt.UTC().Format(time.RFC3339Nano) + "."
	} else {
		message += " Freshness is unknown."
	}
	if state.LifecycleStatus != "OPEN" {
		message += " The incident is not described as current."
	}
	return Summary{ContractVersion: ContractVersion, SnapshotVersion: state.SnapshotVersion, IncidentID: state.IncidentID, EventType: state.EventType, Title: title, Message: message, UncertaintyQualifier: qualifier, ConfidenceState: state.ConfidenceState, Severity: state.Severity, LifecycleStatus: state.LifecycleStatus, PublicLocation: state.PublicLocation, Freshness: state.Freshness, AsOf: state.AsOf, PolicyVersions: state.PolicyVersions}
}
