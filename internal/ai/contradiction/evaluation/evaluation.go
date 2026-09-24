// Package evaluation runs the deterministic contradiction regression suite.
package evaluation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/onerandomd3v/signa/internal/ai/contradiction"
	"github.com/onerandomd3v/signa/internal/ai/extraction"
	"github.com/onerandomd3v/signa/internal/ai/location"
)

type Suite struct {
	EvaluationVersion, ContractVersion string
	Cases                              []Case
}
type Case struct {
	ID          string
	Categories  []string
	Left, Right contradiction.Evidence
	Expected    Expected
}
type Expected struct {
	State   string            `json:"contradiction_state"`
	Recency string            `json:"recency_order"`
	Factors map[string]string `json:"factor_outcomes"`
}
type Failure struct{ CaseID, Category, Message string }
type Report struct {
	EvaluationVersion, ContractVersion string
	Total, Passed                      int
	Failures                           []Failure
}

func (r Report) Failed() bool { return len(r.Failures) > 0 }
func (r Report) String() string {
	lines := []string{fmt.Sprintf("%s: %d cases, %d passed", r.EvaluationVersion, r.Total, r.Passed)}
	for _, failure := range r.Failures {
		lines = append(lines, fmt.Sprintf("case %s [%s]: %s", failure.CaseID, failure.Category, failure.Message))
	}
	return strings.Join(lines, "\n")
}

type provider interface {
	Assess(context.Context, contradiction.Evidence, contradiction.Evidence) ([]byte, error)
}
type manifest struct {
	EvaluationVersion string `json:"evaluation_version"`
	ContractVersion   string `json:"contract_version"`
	Comparison        string `json:"comparison"`
	Cases             []struct {
		ID         string   `json:"case_id"`
		Fixture    string   `json:"fixture"`
		Categories []string `json:"categories"`
	} `json:"cases"`
}
type compactEvidence struct {
	ID            string              `json:"id"`
	EventType     string              `json:"event_type"`
	LocationState string              `json:"location_state"`
	Location      string              `json:"location"`
	Claim         contradiction.Claim `json:"claim"`
	ObservedAt    *string             `json:"observed_at"`
}
type fixture struct {
	FixtureID string          `json:"fixture_id"`
	Left      compactEvidence `json:"left"`
	Right     compactEvidence `json:"right"`
	Expected  Expected        `json:"expected"`
}

func LoadSuite(manifestPath string, validator *contradiction.Validator) (Suite, error) {
	if validator == nil {
		return Suite{}, errors.New("contradiction evaluation validator is required")
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return Suite{}, fmt.Errorf("read contradiction manifest: %w", err)
	}
	var definition manifest
	if err := json.Unmarshal(data, &definition); err != nil {
		return Suite{}, fmt.Errorf("decode contradiction manifest: %w", err)
	}
	if definition.EvaluationVersion == "" || definition.ContractVersion != contradiction.ContractVersion || definition.Comparison != "exact_state_recency_and_factor_outcomes" || len(definition.Cases) == 0 {
		return Suite{}, errors.New("contradiction manifest must declare compatible versions and cases")
	}
	suite := Suite{EvaluationVersion: definition.EvaluationVersion, ContractVersion: definition.ContractVersion, Cases: make([]Case, 0, len(definition.Cases))}
	for _, entry := range definition.Cases {
		if entry.ID == "" || entry.Fixture == "" {
			return Suite{}, errors.New("case requires case_id and fixture")
		}
		path := filepath.Join(filepath.Dir(manifestPath), filepath.Clean(entry.Fixture))
		data, err := os.ReadFile(path)
		if err != nil {
			return Suite{}, fmt.Errorf("read fixture %s: %w", entry.Fixture, err)
		}
		var item fixture
		if err := json.Unmarshal(data, &item); err != nil {
			return Suite{}, fmt.Errorf("decode fixture %s: %w", entry.Fixture, err)
		}
		if item.FixtureID != entry.ID {
			return Suite{}, fmt.Errorf("fixture %s identity mismatch", entry.Fixture)
		}
		left, err := expand(item.Left)
		if err != nil {
			return Suite{}, fmt.Errorf("fixture %s left evidence: %w", entry.Fixture, err)
		}
		right, err := expand(item.Right)
		if err != nil {
			return Suite{}, fmt.Errorf("fixture %s right evidence: %w", entry.Fixture, err)
		}
		if err := contradiction.ValidateInput(left); err != nil {
			return Suite{}, fmt.Errorf("fixture %s left evidence invalid: %w", entry.Fixture, err)
		}
		if err := contradiction.ValidateInput(right); err != nil {
			return Suite{}, fmt.Errorf("fixture %s right evidence invalid: %w", entry.Fixture, err)
		}
		if item.Expected.State == "" || item.Expected.Recency == "" || len(item.Expected.Factors) != 3 {
			return Suite{}, fmt.Errorf("fixture %s expected output incomplete", entry.Fixture)
		}
		suite.Cases = append(suite.Cases, Case{ID: entry.ID, Categories: entry.Categories, Left: left, Right: right, Expected: item.Expected})
	}
	return suite, nil
}

func expand(input compactEvidence) (contradiction.Evidence, error) {
	event := extractionField(input.EventType)
	placeField := extractionField(input.Location)
	locationState := input.LocationState
	locationValue := location.Normalization{ContractVersion: location.ContractVersion, InputContractVersion: location.InputContractVersion, LocationState: locationState, SourceLocationReference: placeField, Candidates: []location.Candidate{}}
	if locationState == "identified" {
		locationValue.Candidates = []location.Candidate{{ReportedText: input.Location, NormalizedText: input.Location, ReferenceKind: "place", Qualifier: nil, EvidenceQuotes: []string{input.Location}}}
	} else if locationState == "ambiguous" {
		locationValue.SourceLocationReference = extraction.Field{Status: "ambiguous", Value: nil, Candidates: []string{input.Location, input.Location + " (alternate)"}, EvidenceQuotes: []string{input.Location}}
		locationValue.Candidates = []location.Candidate{{ReportedText: input.Location, NormalizedText: input.Location, ReferenceKind: "place", Qualifier: nil, EvidenceQuotes: []string{input.Location}}, {ReportedText: input.Location + " (alternate)", NormalizedText: input.Location + " (alternate)", ReferenceKind: "place", Qualifier: nil, EvidenceQuotes: []string{input.Location}}}
	}
	value := contradiction.Evidence{ID: input.ID, EventType: event, Location: locationValue, Claim: input.Claim}
	if input.ObservedAt != nil {
		parsed, err := time.Parse(time.RFC3339, *input.ObservedAt)
		if err != nil {
			return contradiction.Evidence{}, err
		}
		value.ObservedAt = &parsed
	}
	return value, nil
}

func extractionField(value string) extraction.Field {
	if value == "" {
		return extraction.Field{Status: "unknown", Value: nil, Candidates: []string{}, EvidenceQuotes: []string{}}
	}
	return extraction.Field{Status: "identified", Value: &value, Candidates: []string{}, EvidenceQuotes: []string{value}}
}

func Evaluate(ctx context.Context, suite Suite, evaluator provider, validator *contradiction.Validator) (Report, error) {
	if evaluator == nil {
		return Report{}, errors.New("contradiction evaluation provider is required")
	}
	if validator == nil {
		return Report{}, errors.New("contradiction evaluation validator is required")
	}
	report := Report{EvaluationVersion: suite.EvaluationVersion, ContractVersion: suite.ContractVersion}
	for _, testCase := range suite.Cases {
		report.Total++
		data, err := evaluator.Assess(ctx, testCase.Left, testCase.Right)
		if err != nil {
			report.fail(testCase, "provider error: "+err.Error())
			continue
		}
		actual, err := validator.Validate(data)
		if err != nil {
			report.fail(testCase, "output schema validation failed: "+err.Error())
			continue
		}
		var failures []string
		if actual.ContradictionState != testCase.Expected.State {
			failures = append(failures, fmt.Sprintf("contradiction_state mismatch: got %q, want %q", actual.ContradictionState, testCase.Expected.State))
		}
		if actual.RecencyOrder != testCase.Expected.Recency {
			failures = append(failures, fmt.Sprintf("recency_order mismatch: got %q, want %q", actual.RecencyOrder, testCase.Expected.Recency))
		}
		outcomes := map[string]string{}
		for _, factor := range actual.Factors {
			outcomes[factor.Name] = factor.Outcome
		}
		for name, expected := range testCase.Expected.Factors {
			if outcomes[name] != expected {
				failures = append(failures, fmt.Sprintf("factor %s outcome mismatch: got %q, want %q", name, outcomes[name], expected))
			}
		}
		if len(failures) == 0 {
			report.Passed++
		} else {
			for _, message := range failures {
				report.fail(testCase, message)
			}
		}
	}
	return report, nil
}

func (r *Report) fail(testCase Case, message string) {
	r.Failures = append(r.Failures, Failure{CaseID: testCase.ID, Category: strings.Join(testCase.Categories, ","), Message: message})
}
