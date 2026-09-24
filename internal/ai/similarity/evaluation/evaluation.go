// Package evaluation runs the deterministic, versioned same-event similarity suite.
package evaluation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/onerandomd3v/signa/internal/ai/similarity"
)

type Suite struct {
	EvaluationVersion string
	ContractVersion   string
	ToleranceVersion  string
	Cases             []Case
}

type Case struct {
	ID string
	Categories []string
	Report similarity.ReportEvidence
	Candidates []CandidateCase
}

type CandidateCase struct {
	Input similarity.CandidateIncident
	Expected ExpectedAssessment
}

type ExpectedAssessment struct {
	SimilarityState string `json:"similarity_state"`
	SimilarityScore *float64 `json:"similarity_score"`
	Factors map[string]ExpectedFactor `json:"factors"`
}

type ExpectedFactor struct {
	Status string `json:"status"`
	Score *float64 `json:"score"`
}

type Failure struct {
	CaseID   string
	Category string
	Message  string
}

type Report struct {
	EvaluationVersion string
	ContractVersion   string
	Total             int
	Passed            int
	Failures          []Failure
}

func (r Report) Failed() bool { return len(r.Failures) != 0 }
func (r Report) String() string {
	lines := []string{fmt.Sprintf("%s: %d candidates, %d passed", r.EvaluationVersion, r.Total, r.Passed)}
	for _, failure := range r.Failures {
		category := failure.Category
		if category == "" {
			category = "uncategorized"
		}
		lines = append(lines, fmt.Sprintf("case %s [%s]: %s", failure.CaseID, category, failure.Message))
	}
	return strings.Join(lines, "\n")
}

type provider interface {
	Assess(context.Context, similarity.ReportEvidence, similarity.CandidateIncident) ([]byte, error)
}

type manifest struct {
	EvaluationVersion string `json:"evaluation_version"`
	ContractVersion   string `json:"contract_version"`
	ToleranceVersion  string `json:"tolerance_version"`
	Comparison        string `json:"comparison"`
	Cases             []struct {
		ID         string   `json:"case_id"`
		Fixture    string   `json:"fixture"`
		Categories []string `json:"categories"`
	} `json:"cases"`
}

type fixture struct {
	FixtureID  string                      `json:"fixture_id"`
	Report     similarity.ReportEvidence   `json:"report_evidence"`
	Candidates []struct {
		Input similarity.CandidateIncident `json:"candidate_incident"`
		Expected ExpectedAssessment `json:"expected_assessment"`
	} `json:"candidates"`
}

func LoadSuite(manifestPath string, validator *similarity.Validator) (Suite, error) {
	if validator == nil {
		return Suite{}, errors.New("similarity evaluation validator is required")
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return Suite{}, fmt.Errorf("read similarity evaluation manifest: %w", err)
	}
	var definition manifest
	if err := json.Unmarshal(data, &definition); err != nil {
		return Suite{}, fmt.Errorf("decode similarity evaluation manifest: %w", err)
	}
	if definition.EvaluationVersion == "" || definition.ContractVersion != similarity.ContractVersion || definition.ToleranceVersion == "" || definition.Comparison != "exact_scores_and_factor_statuses" || len(definition.Cases) == 0 {
		return Suite{}, errors.New("similarity evaluation manifest must declare versions and cases")
	}
	suite := Suite{
		EvaluationVersion: definition.EvaluationVersion,
		ContractVersion:   definition.ContractVersion,
		ToleranceVersion:  definition.ToleranceVersion,
		Cases:             make([]Case, 0, len(definition.Cases)),
	}
	for _, entry := range definition.Cases {
		if entry.ID == "" || entry.Fixture == "" {
			return Suite{}, errors.New("similarity evaluation case requires case_id and fixture")
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
		if item.FixtureID != entry.ID || len(item.Candidates) == 0 {
			return Suite{}, fmt.Errorf("fixture %s identity or candidates are invalid", entry.Fixture)
		}
		caseItem := Case{
			ID: entry.ID, Categories: append([]string(nil), entry.Categories...),
			Report: item.Report, Candidates: make([]CandidateCase, 0, len(item.Candidates)),
		}
		for _, candidate := range item.Candidates {
			if candidate.Input.ID == "" {
				return Suite{}, fmt.Errorf("fixture %s has candidate without id", entry.Fixture)
			}
			if err := validateExpected(candidate.Expected); err != nil {
				return Suite{}, fmt.Errorf("fixture %s expected output invalid: %w", entry.Fixture, err)
			}
			caseItem.Candidates = append(caseItem.Candidates, CandidateCase{Input: candidate.Input, Expected: candidate.Expected})
		}
		suite.Cases = append(suite.Cases, caseItem)
	}
	return suite, nil
}

func Evaluate(ctx context.Context, suite Suite, scorer provider, validator *similarity.Validator) (Report, error) {
	if scorer == nil {
		return Report{}, errors.New("similarity evaluation provider is required")
	}
	if validator == nil {
		return Report{}, errors.New("similarity evaluation validator is required")
	}
	report := Report{EvaluationVersion: suite.EvaluationVersion, ContractVersion: suite.ContractVersion}
	for _, testCase := range suite.Cases {
		for _, candidate := range testCase.Candidates {
			report.Total++
			data, err := scorer.Assess(ctx, testCase.Report, candidate.Input)
			if err != nil {
				report.add(testCase, fmt.Sprintf("provider error: %v", err))
				continue
			}
			actual, err := validator.Validate(data)
			if err != nil {
				report.add(testCase, fmt.Sprintf("output schema validation failed: %v", err))
				continue
			}
			failures := compare(candidate.Input.ID, candidate.Expected, actual)
			if len(failures) == 0 {
				report.Passed++
				continue
			}
			for _, message := range failures { report.add(testCase, message) }
		}
	}
	return report, nil
}

func (r *Report) add(testCase Case, message string) {
	r.Failures = append(r.Failures, Failure{CaseID: testCase.ID, Category: strings.Join(testCase.Categories, ","), Message: message})
}

func compare(candidateID string, expected ExpectedAssessment, actual similarity.Assessment) []string {
	var failures []string
	if actual.CandidateIncidentID != candidateID {
		failures = append(failures, fmt.Sprintf("candidate_incident_id mismatch: got %q, want %q", actual.CandidateIncidentID, candidateID))
	}
	if actual.SimilarityState != expected.SimilarityState {
		failures = append(failures, fmt.Sprintf("similarity_state mismatch: got %q, want %q", actual.SimilarityState, expected.SimilarityState))
	}
	if !sameFloat(expected.SimilarityScore, actual.SimilarityScore) {
		failures = append(failures, fmt.Sprintf("similarity_score mismatch: got %s, want %s", floatString(actual.SimilarityScore), floatString(expected.SimilarityScore)))
	}
	actualFactors := make(map[string]similarity.Factor, len(actual.Factors))
	for _, factor := range actual.Factors {
		actualFactors[factor.Name] = factor
	}
	if len(actualFactors) != len(expected.Factors) {
		failures = append(failures, fmt.Sprintf("factors count mismatch: got %d, want %d", len(actualFactors), len(expected.Factors)))
	}
	wanted := make([]string, 0, len(expected.Factors))
	for name := range expected.Factors {
		wanted = append(wanted, name)
	}
	sort.Strings(wanted)
	for _, name := range wanted {
		want := expected.Factors[name]
		got, ok := actualFactors[name]
		if !ok {
			failures = append(failures, fmt.Sprintf("factor %s missing", name))
			continue
		}
		if got.Status != want.Status {
			failures = append(failures, fmt.Sprintf("factor %s status mismatch: got %q, want %q", name, got.Status, want.Status))
		}
		if !sameFloat(want.Score, got.Score) {
			failures = append(failures, fmt.Sprintf("factor %s score mismatch: got %s, want %s", name, floatString(got.Score), floatString(want.Score)))
		}
	}
	return failures
}

func sameFloat(left, right *float64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func floatString(value *float64) string {
	if value == nil {
		return "null"
	}
	return fmt.Sprintf("%g", *value)
}

func validateExpected(expected ExpectedAssessment) error {
	if expected.SimilarityState != "assessed" && expected.SimilarityState != "insufficient_evidence" {
		return errors.New("unsupported similarity_state")
	}
	if expected.SimilarityState == "assessed" && expected.SimilarityScore == nil {
		return errors.New("assessed expected output requires score")
	}
	if expected.SimilarityState == "insufficient_evidence" && expected.SimilarityScore != nil {
		return errors.New("insufficient evidence expected output requires null score")
	}
	if expected.SimilarityScore != nil && (*expected.SimilarityScore < 0 || *expected.SimilarityScore > 1) {
		return errors.New("expected similarity score outside [0,1]")
	}
	if len(expected.Factors) != 5 {
		return errors.New("expected output requires all five factors")
	}
	for name, factor := range expected.Factors {
		if factor.Status != "informative" && factor.Status != "indeterminate" {
			return fmt.Errorf("factor %s has unsupported status", name)
		}
		if factor.Status == "informative" && factor.Score == nil {
			return fmt.Errorf("factor %s informative score required", name)
		}
		if factor.Status == "indeterminate" && factor.Score != nil {
			return fmt.Errorf("factor %s indeterminate score must be null", name)
		}
		if factor.Score != nil && (*factor.Score < 0 || *factor.Score > 1) {
			return fmt.Errorf("factor %s score outside [0,1]", name)
		}
	}
	return nil
}
