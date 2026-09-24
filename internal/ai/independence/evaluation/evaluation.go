// Package evaluation runs the deterministic, versioned evidence independence suite.
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

	"github.com/onerandomd3v/signa/internal/ai/independence"
)

type Suite struct {
	EvaluationVersion string
	ContractVersion   string
	ToleranceVersion  string
	Cases             []Case
}
type Case struct {
	ID         string
	Categories []string
	Input      independence.Input
	Expected   ExpectedAssessment
}
type ExpectedAssessment struct {
	IndependenceState string                    `json:"independence_state"`
	Factors           map[string]ExpectedFactor `json:"factors"`
}
type ExpectedFactor struct {
	Outcome string   `json:"outcome"`
	Score   *float64 `json:"score"`
	Reason  string   `json:"reason,omitempty"`
}
type Failure struct{ CaseID, Category, Message string }
type Report struct {
	EvaluationVersion, ContractVersion string
	Total, Passed                      int
	Failures                           []Failure
}

func (r Report) Failed() bool { return len(r.Failures) != 0 }
func (r Report) String() string {
	lines := []string{fmt.Sprintf("%s: %d comparisons, %d passed", r.EvaluationVersion, r.Total, r.Passed)}
	for _, f := range r.Failures {
		category := f.Category
		if category == "" {
			category = "uncategorized"
		}
		lines = append(lines, fmt.Sprintf("case %s [%s]: %s", f.CaseID, category, f.Message))
	}
	return strings.Join(lines, "\n")
}

type provider interface {
	Assess(context.Context, independence.Input) ([]byte, error)
}
type manifest struct {
	EvaluationVersion   string  `json:"evaluation_version"`
	ContractVersion     string  `json:"contract_version"`
	ToleranceVersion    string  `json:"tolerance_version"`
	Comparison          string  `json:"comparison"`
	NearDuplicateCutoff float64 `json:"near_duplicate_cutoff"`
	Cases               []struct {
		ID         string   `json:"case_id"`
		Fixture    string   `json:"fixture"`
		Categories []string `json:"categories"`
	} `json:"cases"`
}
type fixture struct {
	FixtureID string             `json:"fixture_id"`
	Input     independence.Input `json:"input"`
	Expected  ExpectedAssessment `json:"expected"`
}

func LoadSuite(manifestPath string, validator *independence.Validator) (Suite, error) {
	if validator == nil {
		return Suite{}, errors.New("independence evaluation validator is required")
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return Suite{}, fmt.Errorf("read independence manifest: %w", err)
	}
	var definition manifest
	if err := json.Unmarshal(data, &definition); err != nil {
		return Suite{}, fmt.Errorf("decode independence manifest: %w", err)
	}
	if definition.EvaluationVersion == "" || definition.ContractVersion != independence.ContractVersion || definition.ToleranceVersion == "" || definition.Comparison != "exact_states_outcomes_scores_and_selected_reasons" || definition.NearDuplicateCutoff != independence.NearDuplicateCutoff || len(definition.Cases) == 0 {
		return Suite{}, errors.New("independence manifest must declare supported versions, cutoff, and cases")
	}
	suite := Suite{EvaluationVersion: definition.EvaluationVersion, ContractVersion: definition.ContractVersion, ToleranceVersion: definition.ToleranceVersion, Cases: make([]Case, 0, len(definition.Cases))}
	for _, entry := range definition.Cases {
		if entry.ID == "" || entry.Fixture == "" {
			return Suite{}, errors.New("independence evaluation case requires case_id and fixture")
		}
		path := filepath.Join(filepath.Dir(manifestPath), filepath.Clean(entry.Fixture))
		content, err := os.ReadFile(path)
		if err != nil {
			return Suite{}, fmt.Errorf("read fixture %s: %w", entry.Fixture, err)
		}
		var item fixture
		if err := json.Unmarshal(content, &item); err != nil {
			return Suite{}, fmt.Errorf("decode fixture %s: %w", entry.Fixture, err)
		}
		if item.FixtureID != entry.ID {
			return Suite{}, fmt.Errorf("fixture %s identity mismatch", entry.Fixture)
		}
		if err := independence.ValidateInput(item.Input); err != nil {
			return Suite{}, fmt.Errorf("fixture %s input invalid: %w", entry.Fixture, err)
		}
		if err := validateExpected(item.Expected); err != nil {
			return Suite{}, fmt.Errorf("fixture %s expected output invalid: %w", entry.Fixture, err)
		}
		suite.Cases = append(suite.Cases, Case{ID: entry.ID, Categories: append([]string(nil), entry.Categories...), Input: item.Input, Expected: item.Expected})
	}
	return suite, nil
}

func Evaluate(ctx context.Context, suite Suite, scorer provider, validator *independence.Validator) (Report, error) {
	if scorer == nil {
		return Report{}, errors.New("independence evaluation provider is required")
	}
	if validator == nil {
		return Report{}, errors.New("independence evaluation validator is required")
	}
	report := Report{EvaluationVersion: suite.EvaluationVersion, ContractVersion: suite.ContractVersion}
	for _, testCase := range suite.Cases {
		report.Total++
		data, err := scorer.Assess(ctx, testCase.Input)
		if err != nil {
			report.add(testCase, fmt.Sprintf("provider error: %v", err))
			continue
		}
		actual, err := validator.ValidateForInput(data, testCase.Input)
		if err != nil {
			report.add(testCase, fmt.Sprintf("output schema validation failed: %v", err))
			continue
		}
		failures := compare(testCase.Expected, actual)
		if len(failures) == 0 {
			report.Passed++
			continue
		}
		for _, message := range failures {
			report.add(testCase, message)
		}
	}
	return report, nil
}
func (r *Report) add(testCase Case, message string) {
	r.Failures = append(r.Failures, Failure{CaseID: testCase.ID, Category: strings.Join(testCase.Categories, ","), Message: message})
}

func compare(expected ExpectedAssessment, actual independence.Assessment) []string {
	var failures []string
	if actual.IndependenceState != expected.IndependenceState {
		failures = append(failures, fmt.Sprintf("independence_state mismatch: got %q, want %q", actual.IndependenceState, expected.IndependenceState))
	}
	actualFactors := make(map[string]independence.Factor, len(actual.Factors))
	for _, factor := range actual.Factors {
		actualFactors[factor.Name] = factor
	}
	if len(actualFactors) != len(expected.Factors) {
		failures = append(failures, fmt.Sprintf("factors count mismatch: got %d, want %d", len(actualFactors), len(expected.Factors)))
	}
	names := make([]string, 0, len(expected.Factors))
	for name := range expected.Factors {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		want := expected.Factors[name]
		got, ok := actualFactors[name]
		if !ok {
			failures = append(failures, fmt.Sprintf("factor %s missing", name))
			continue
		}
		if got.Outcome != want.Outcome {
			failures = append(failures, fmt.Sprintf("factor %s outcome mismatch: got %q, want %q", name, got.Outcome, want.Outcome))
		}
		if !sameFloat(got.Score, want.Score) {
			failures = append(failures, fmt.Sprintf("factor %s score mismatch: got %s, want %s", name, floatString(got.Score), floatString(want.Score)))
		}
		if want.Reason != "" && got.Reason != want.Reason {
			failures = append(failures, fmt.Sprintf("factor %s reason mismatch: got %q, want %q", name, got.Reason, want.Reason))
		}
	}
	return failures
}
func validateExpected(expected ExpectedAssessment) error {
	states := map[string]bool{"independence_supported": true, "repetition_risk": true, "mixed_signals": true, "indeterminate": true}
	if !states[expected.IndependenceState] {
		return errors.New("unsupported independence_state")
	}
	if len(expected.Factors) != 4 {
		return errors.New("expected output requires all four factors")
	}
	for name, factor := range expected.Factors {
		if name != "text_similarity" && name != "media_fingerprint" && name != "source_origin" && name != "source_claim" {
			return fmt.Errorf("unsupported factor %s", name)
		}
		if factor.Outcome != "repetition_risk" && factor.Outcome != "independence_support" && factor.Outcome != "no_signal" && factor.Outcome != "unknown" {
			return fmt.Errorf("factor %s has unsupported outcome", name)
		}
		if name == "text_similarity" && factor.Outcome != "unknown" && factor.Score == nil {
			return fmt.Errorf("factor %s requires score unless text is unknown", name)
		}
		if name == "text_similarity" && factor.Outcome == "unknown" && factor.Score != nil {
			return fmt.Errorf("factor %s unknown score must be null", name)
		}
		if name != "text_similarity" && factor.Score != nil {
			return fmt.Errorf("factor %s score must be null", name)
		}
		if factor.Score != nil && (*factor.Score < 0 || *factor.Score > 1) {
			return fmt.Errorf("factor %s score outside [0,1]", name)
		}
	}
	return nil
}
func sameFloat(a, b *float64) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }
func floatString(value *float64) string {
	if value == nil {
		return "null"
	}
	return fmt.Sprintf("%g", *value)
}
