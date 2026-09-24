// Package similarity produces explainable same-event interpretation signals.
// A similarity assessment is not incident truth, confidence, or an attachment decision.
package similarity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode"

	"github.com/onerandomd3v/signa/internal/ai/extraction"
	"github.com/onerandomd3v/signa/internal/ai/location"
)

const (
	ContractVersion      = "signa.ai.same-event-similarity.v1"
	InputContractVersion = "signa.ai.report-extraction.v0"
	ScoringMethod        = "arithmetic_mean_of_informative_factors.v1"
)

type ReportEvidence struct {
	EventType     extraction.Field      `json:"event_type"`
	TimeReference extraction.Field      `json:"time_reference"`
	Location      location.Normalization `json:"location"`
	Text          string                `json:"text"`
}

type CandidateIncident struct {
	ID                 string         `json:"id"`
	Evidence           ReportEvidence `json:"evidence"`
	DistanceMeters     *float64       `json:"distance_meters"`
	SearchRadiusMeters *float64       `json:"search_radius_meters"`
}

type Assessment struct {
	ContractVersion      string   `json:"contract_version"`
	InputContractVersion string   `json:"input_contract_version"`
	CandidateIncidentID  string   `json:"candidate_incident_id"`
	SimilarityState      string   `json:"similarity_state"`
	SimilarityScore      *float64 `json:"similarity_score"`
	ScoringMethod        string   `json:"scoring_method"`
	Factors              []Factor `json:"factors"`
	SupportingReasons    []string `json:"supporting_reasons"`
	LimitingReasons      []string `json:"limiting_reasons"`
}

type Factor struct {
	Name   string   `json:"name"`
	Status string   `json:"status"`
	Score  *float64 `json:"score"`
	Reason string   `json:"reason"`
}

type Provider interface {
	Assess(context.Context, ReportEvidence, CandidateIncident) ([]byte, error)
}

type ProviderFunc func(context.Context, ReportEvidence, CandidateIncident) ([]byte, error)

func (f ProviderFunc) Assess(ctx context.Context, report ReportEvidence, candidate CandidateIncident) ([]byte, error) {
	if f == nil {
		return nil, errors.New("similarity provider function is nil")
	}
	return f(ctx, report, candidate)
}

type Processor struct {
	Provider  Provider
	Validator *Validator
}

func (p Processor) Assess(ctx context.Context, report ReportEvidence, candidate CandidateIncident) ([]byte, error) {
	if p.Provider == nil {
		return nil, errors.New("similarity provider is required")
	}
	if p.Validator == nil {
		return nil, errors.New("similarity validator is required")
	}
	data, err := p.Provider.Assess(ctx, report, candidate)
	if err != nil {
		return nil, fmt.Errorf("assess candidate similarity: %w", err)
	}
	if _, err := p.Validator.Validate(data); err != nil {
		return nil, err
	}
	return data, nil
}

// RuleBasedScorer compares only supplied evidence. Exact categorical agreement
// and lexical overlap are transparent features; unknown or non-identical time
// and location wording is indeterminate, never a negative match. Distance is
// consumed only when supplied by the approved candidate lookup.
type RuleBasedScorer struct{}

func (RuleBasedScorer) Assess(ctx context.Context, report ReportEvidence, candidate CandidateIncident) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(candidate.ID) == "" {
		return nil, errors.New("candidate incident id is required")
	}
	factors := []Factor{
		compareEventType(report.EventType, candidate.Evidence.EventType),
		compareTime(report.TimeReference, candidate.Evidence.TimeReference),
		compareLocation(report.Location, candidate.Evidence.Location),
		compareText(report.Text, candidate.Evidence.Text),
		compareDistance(candidate.DistanceMeters, candidate.SearchRadiusMeters),
	}
	scoreTotal, scored := 0.0, 0
	supporting := make([]string, 0)
	limiting := make([]string, 0)
	for _, factor := range factors {
		if factor.Score != nil {
			scoreTotal += *factor.Score
			scored++
		}
		if factor.Status == "informative" && factor.Score != nil && *factor.Score > 0 {
			supporting = append(supporting, factor.Name+": "+factor.Reason)
		}
		if factor.Status == "indeterminate" {
			limiting = append(limiting, factor.Name+": "+factor.Reason)
		}
	}
	state := "assessed"
	var score *float64
	if scored == 0 {
		state = "insufficient_evidence"
		limiting = append(limiting, "no comparable evidence was available")
	} else {
		value := rounded(scoreTotal / float64(scored))
		score = &value
	}
	assessment := Assessment{
		ContractVersion: ContractVersion, InputContractVersion: InputContractVersion,
		CandidateIncidentID: candidate.ID, SimilarityState: state, SimilarityScore: score,
		ScoringMethod: ScoringMethod, Factors: factors, SupportingReasons: supporting, LimitingReasons: limiting,
	}
	return json.Marshal(assessment)
}

func compareEventType(left, right extraction.Field) Factor {
	if identified(left) && identified(right) {
		if *left.Value == *right.Value {
			return informative("event_type", 1, "identified event types agree")
		}
		return informative("event_type", 0, "identified event types differ")
	}
	return indeterminate("event_type", "one or both event types are unknown or ambiguous")
}

func compareTime(left, right extraction.Field) Factor {
	if identified(left) && identified(right) && strings.EqualFold(strings.TrimSpace(*left.Value), strings.TrimSpace(*right.Value)) {
		return informative("time_reference", 1, "reported time-reference wording agrees exactly")
	}
	return indeterminate("time_reference", "time wording is missing, ambiguous, or not directly comparable without temporal interpretation")
}

func compareLocation(left, right location.Normalization) Factor {
	if left.LocationState == "identified" && right.LocationState == "identified" &&
		len(left.Candidates) == 1 && len(right.Candidates) == 1 &&
		strings.EqualFold(strings.TrimSpace(left.Candidates[0].NormalizedText), strings.TrimSpace(right.Candidates[0].NormalizedText)) {
		return informative("location_reference", 1, "identified normalized place wording agrees exactly")
	}
	return indeterminate("location_reference", "location is missing, ambiguous, or distinct text alone does not establish incompatibility")
}

func compareText(left, right string) Factor {
	a, b := wordSet(left), wordSet(right)
	if len(a) == 0 || len(b) == 0 {
		return indeterminate("text_similarity", "report or candidate evidence text is missing")
	}
	intersection := 0
	for word := range a {
		if _, ok := b[word]; ok {
			intersection++
		}
	}
	union := len(a) + len(b) - intersection
	return informative("text_similarity", rounded(float64(intersection)/float64(union)), "word-set Jaccard overlap computed from supplied evidence text")
}

func compareDistance(distance, radius *float64) Factor {
	if distance == nil || radius == nil {
		return indeterminate("spatial_proximity", "candidate lookup distance or search radius was not supplied")
	}
	if !finite(*distance) || *distance < 0 || !finite(*radius) || *radius <= 0 {
		return indeterminate("spatial_proximity", "candidate lookup distance or search radius is invalid")
	}
	value := 1 - *distance / *radius
	if value < 0 {
		value = 0
	}
	return informative("spatial_proximity", rounded(value), "continuous distance-to-search-radius ratio from candidate lookup; no attachment threshold is applied")
}

func informative(name string, score float64, reason string) Factor {
	return Factor{Name: name, Status: "informative", Score: &score, Reason: reason}
}

func indeterminate(name, reason string) Factor {
	return Factor{Name: name, Status: "indeterminate", Score: nil, Reason: reason}
}

func identified(field extraction.Field) bool {
	return field.Status == "identified" && field.Value != nil
}

func wordSet(value string) map[string]struct{} {
	words := make(map[string]struct{})
	var word strings.Builder
	flush := func() {
		if word.Len() > 0 {
			words[strings.ToLower(word.String())] = struct{}{}
			word.Reset()
		}
	}
	for _, char := range value {
		if unicode.IsLetter(char) || unicode.IsNumber(char) {
			word.WriteRune(char)
		} else {
			flush()
		}
	}
	flush()
	return words
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func rounded(value float64) float64 {
	return math.Round(value*1e12) / 1e12
}
