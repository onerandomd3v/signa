package alertsummary

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	ContractVersion  = "signa.ai.alert-summarization.v1"
	SnapshotVersion  = "signa.alert-summary-snapshot.v1"
	EventTypeUnknown = "unknown"
	EventTypeOther   = "other"
)

type ConfidenceState string

const (
	Unverified     ConfidenceState = "UNVERIFIED"
	Emerging       ConfidenceState = "EMERGING"
	Corroborated   ConfidenceState = "CORROBORATED"
	HighConfidence ConfidenceState = "HIGH_CONFIDENCE"
	Disputed       ConfidenceState = "DISPUTED"
)

type FreshnessStatus string

const (
	KnownFreshness   FreshnessStatus = "known"
	UnknownFreshness FreshnessStatus = "unknown"
)

type PublicLocation struct {
	Status string  `json:"status"`
	Label  *string `json:"label"`
}

type Freshness struct {
	Status       FreshnessStatus `json:"status"`
	LastSignalAt *time.Time      `json:"last_signal_at"`
	AgeSeconds   *int64          `json:"age_seconds"`
}

type PolicyVersions struct {
	Confidence string `json:"confidence"`
	Lifecycle  string `json:"lifecycle"`
}

// State is the authoritative, public-safe incident snapshot supplied to the
// summarizer. It contains no reporter identity, private coordinates, or truth
// score. The incident service owns every field in this snapshot.
type State struct {
	SnapshotVersion string          `json:"snapshot_version"`
	IncidentID      string          `json:"incident_id"`
	EventType       string          `json:"event_type"`
	ConfidenceState ConfidenceState `json:"confidence_state"`
	Severity        *string         `json:"severity"`
	LifecycleStatus string          `json:"lifecycle_status"`
	PublicLocation  PublicLocation  `json:"public_location"`
	Freshness       Freshness       `json:"freshness"`
	AsOf            time.Time       `json:"as_of"`
	PolicyVersions  PolicyVersions  `json:"policy_versions"`
}

type Summary struct {
	ContractVersion      string          `json:"contract_version"`
	SnapshotVersion      string          `json:"snapshot_version"`
	IncidentID           string          `json:"incident_id"`
	EventType            string          `json:"event_type"`
	Title                string          `json:"title"`
	Message              string          `json:"message"`
	UncertaintyQualifier string          `json:"uncertainty_qualifier"`
	ConfidenceState      ConfidenceState `json:"confidence_state"`
	Severity             *string         `json:"severity"`
	LifecycleStatus      string          `json:"lifecycle_status"`
	PublicLocation       PublicLocation  `json:"public_location"`
	Freshness            Freshness       `json:"freshness"`
	AsOf                 time.Time       `json:"as_of"`
	PolicyVersions       PolicyVersions  `json:"policy_versions"`
}

type Provider interface {
	Summarize(context.Context, State) ([]byte, error)
}
type ProviderFunc func(context.Context, State) ([]byte, error)

func (f ProviderFunc) Summarize(ctx context.Context, state State) ([]byte, error) {
	if f == nil {
		return nil, fmt.Errorf("alert summary provider function is nil")
	}
	return f(ctx, state)
}

type Validator struct{ schema *jsonschema.Schema }

func NewValidator(schemaBytes []byte) (*Validator, error) {
	var document any
	if err := json.Unmarshal(schemaBytes, &document); err != nil {
		return nil, fmt.Errorf("decode alert summarization schema: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	resource := "https://signa.local/contracts/ai/alert-summarization/v1/schema.json"
	if err := compiler.AddResource(resource, document); err != nil {
		return nil, fmt.Errorf("register alert summarization schema: %w", err)
	}
	schema, err := compiler.Compile(resource)
	if err != nil {
		return nil, fmt.Errorf("compile alert summarization schema: %w", err)
	}
	return &Validator{schema: schema}, nil
}

func (v *Validator) Validate(data []byte) (Summary, error) {
	if v == nil || v.schema == nil {
		return Summary{}, fmt.Errorf("alert summarization validator is not initialized")
	}
	var document any
	if err := json.Unmarshal(data, &document); err != nil {
		return Summary{}, fmt.Errorf("decode alert summary: %w", err)
	}
	if err := v.schema.Validate(document); err != nil {
		return Summary{}, fmt.Errorf("validate alert summary: %w", err)
	}
	var summary Summary
	if err := json.Unmarshal(data, &summary); err != nil {
		return Summary{}, fmt.Errorf("decode validated alert summary: %w", err)
	}
	return summary, nil
}

func ValidateState(state State) error {
	if state.SnapshotVersion != SnapshotVersion {
		return fmt.Errorf("snapshot_version must be %q", SnapshotVersion)
	}
	if strings.TrimSpace(state.IncidentID) == "" {
		return fmt.Errorf("incident_id is required")
	}
	if strings.TrimSpace(state.EventType) == "" {
		return fmt.Errorf("event_type is required")
	}
	if strings.ContainsAny(state.EventType, " \t\r\n") {
		return fmt.Errorf("event_type must be a canonical identifier")
	}
	if state.AsOf.IsZero() {
		return fmt.Errorf("as_of is required")
	}
	if state.PolicyVersions.Confidence == "" || state.PolicyVersions.Lifecycle == "" {
		return fmt.Errorf("applicable confidence and lifecycle policy versions are required")
	}
	if err := validateFreshness(state.AsOf, state.Freshness); err != nil {
		return err
	}
	if state.PublicLocation.Status != "identified" && state.PublicLocation.Status != "unknown" {
		return fmt.Errorf("unsupported public_location status %q", state.PublicLocation.Status)
	}
	if state.PublicLocation.Status == "unknown" && state.PublicLocation.Label != nil {
		return fmt.Errorf("unknown public_location cannot have a label")
	}
	if state.PublicLocation.Status == "identified" && (state.PublicLocation.Label == nil || strings.TrimSpace(*state.PublicLocation.Label) == "") {
		return fmt.Errorf("identified public_location requires a label")
	}
	if state.LifecycleStatus != "OPEN" && state.LifecycleStatus != "RESOLVING" && state.LifecycleStatus != "RESOLVED" && state.LifecycleStatus != "EXPIRED" {
		return fmt.Errorf("unsupported lifecycle_status %q", state.LifecycleStatus)
	}
	if state.PublicLocation.Label != nil && coordinatePattern.MatchString(strings.ToLower(*state.PublicLocation.Label)) {
		return fmt.Errorf("public_location.label cannot contain exact coordinates")
	}
	return nil
}

func validateFreshness(asOf time.Time, freshness Freshness) error {
	switch freshness.Status {
	case KnownFreshness, UnknownFreshness:
	default:
		return fmt.Errorf("unsupported freshness status %q", freshness.Status)
	}
	if freshness.LastSignalAt != nil && freshness.LastSignalAt.After(asOf) {
		return fmt.Errorf("last_signal_at cannot be after as_of")
	}
	if freshness.AgeSeconds != nil && *freshness.AgeSeconds < 0 {
		return fmt.Errorf("age_seconds cannot be negative")
	}
	if freshness.LastSignalAt != nil && freshness.AgeSeconds != nil {
		want := int64(asOf.Sub(freshness.LastSignalAt.UTC()).Seconds())
		if want != *freshness.AgeSeconds {
			return fmt.Errorf("age_seconds does not match as_of and last_signal_at")
		}
	}
	if freshness.Status == UnknownFreshness {
		if freshness.LastSignalAt != nil || freshness.AgeSeconds != nil {
			return fmt.Errorf("unknown freshness cannot include known timestamp or age")
		}
	} else if freshness.LastSignalAt == nil || freshness.AgeSeconds == nil {
		return fmt.Errorf("known freshness status requires last_signal_at and age_seconds")
	}
	return nil
}

func ValidateBound(state State, summary Summary) error {
	if err := ValidateState(state); err != nil {
		return fmt.Errorf("invalid authoritative snapshot: %w", err)
	}
	if summary.ContractVersion != ContractVersion {
		return fmt.Errorf("contract_version must be %q", ContractVersion)
	}
	if summary.SnapshotVersion != state.SnapshotVersion || summary.IncidentID != state.IncidentID || summary.EventType != state.EventType {
		return fmt.Errorf("provider changed authoritative snapshot identity")
	}
	if summary.ConfidenceState != state.ConfidenceState {
		return fmt.Errorf("provider changed confidence_state from %q to %q", state.ConfidenceState, summary.ConfidenceState)
	}
	if !sameStringPointer(summary.Severity, state.Severity) || summary.LifecycleStatus != state.LifecycleStatus || !sameLocation(summary.PublicLocation, state.PublicLocation) || !sameFreshness(summary.Freshness, state.Freshness) || !summary.AsOf.Equal(state.AsOf) || summary.PolicyVersions != state.PolicyVersions {
		return fmt.Errorf("provider changed authoritative incident state")
	}
	if strings.TrimSpace(summary.Title) == "" || strings.TrimSpace(summary.Message) == "" || strings.TrimSpace(summary.UncertaintyQualifier) == "" {
		return fmt.Errorf("title, message, and uncertainty_qualifier are required")
	}
	expected := RenderDeterministic(state)
	if summary.Title != expected.Title || summary.Message != expected.Message || summary.UncertaintyQualifier != expected.UncertaintyQualifier {
		return fmt.Errorf("provider public wording must use the caller-derived template")
	}
	text := strings.ToLower(summary.Title + " " + summary.Message + " " + summary.UncertaintyQualifier)
	for _, word := range []string{"confirmed", "verified", "definitely", "certainly", "fact"} {
		if containsWord(text, word) {
			return fmt.Errorf("summary contains unsupported certainty claim %q", word)
		}
	}
	for _, phrase := range []string{"safe now", "no danger", "no immediate danger", "no risk", "nothing to worry"} {
		if strings.Contains(text, phrase) {
			return fmt.Errorf("summary contains unsupported assurance %q", phrase)
		}
	}
	if state.ConfidenceState == Disputed && !containsWord(text, "disputed") && !strings.Contains(text, "conflict") && !strings.Contains(text, "uncertain") {
		return fmt.Errorf("disputed summary must mention conflicting or uncertain information")
	}
	if state.LifecycleStatus != "OPEN" || state.Freshness.Status != KnownFreshness || state.Freshness.AgeSeconds == nil || *state.Freshness.AgeSeconds != 0 {
		for _, phrase := range []string{"ongoing", "happening now", "currently", "right now"} {
			if strings.Contains(text, phrase) {
				return fmt.Errorf("summary cannot imply current activity for stale/resolving/resolved/expired state")
			}
		}
	}
	if coordinatePattern.MatchString(text) {
		return fmt.Errorf("summary contains exact coordinates")
	}
	return nil
}

var coordinatePattern = regexp.MustCompile(`(?i)([-+]?\d{1,3}\.\d{3,})\s*[,; ]\s*([-+]?\d{1,3}\.\d{3,})`)
var wordPattern = regexp.MustCompile(`\b[a-z]+\b`)

func containsWord(text, want string) bool {
	for _, word := range wordPattern.FindAllString(text, -1) {
		if word == want {
			return true
		}
	}
	return false
}
func sameStringPointer(a, b *string) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }
func sameLocation(a, b PublicLocation) bool {
	return a.Status == b.Status && sameStringPointer(a.Label, b.Label)
}
func sameFreshness(a, b Freshness) bool {
	return a.Status == b.Status && sameTimePointer(a.LastSignalAt, b.LastSignalAt) && (a.AgeSeconds == nil) == (b.AgeSeconds == nil) && (a.AgeSeconds == nil || *a.AgeSeconds == *b.AgeSeconds)
}
func sameTimePointer(a, b *time.Time) bool {
	return (a == nil) == (b == nil) && (a == nil || a.Equal(*b))
}

func Summarize(ctx context.Context, state State, provider Provider, validator *Validator) (Summary, error) {
	if provider == nil {
		return Summary{}, fmt.Errorf("alert summary provider is required")
	}
	if validator == nil {
		return Summary{}, fmt.Errorf("alert summary validator is required")
	}
	if err := ValidateState(state); err != nil {
		return Summary{}, err
	}
	data, err := provider.Summarize(ctx, state)
	if err != nil {
		return Summary{}, fmt.Errorf("summarize alert: %w", err)
	}
	summary, err := validator.Validate(data)
	if err != nil {
		return Summary{}, err
	}
	if err := ValidateBound(state, summary); err != nil {
		return Summary{}, err
	}
	return summary, nil
}
