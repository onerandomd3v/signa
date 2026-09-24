// Package evaluation executes the checked-in, offline source-coordination suite.
package evaluation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/onerandomd3v/signa/internal/ai/coordination"
)

type Suite struct {
	EvaluationVersion string
	ContractVersion   string
	ToleranceVersion  string
	WeightTolerance   float64
	Cases             []Case
}
type Case struct {
	ID         string
	Categories []string
	Input      coordination.Input
	Config     coordination.Config
	Expected   Expected
}
type Expected struct {
	CoordinationState string                    `json:"coordination_state"`
	Factors           map[string]ExpectedFactor `json:"factors"`
}
type ExpectedFactor struct {
	Outcome             string   `json:"outcome"`
	Weight              *float64 `json:"weight"`
	PairCount           int      `json:"pair_count"`
	KnownPairCount      int      `json:"known_pair_count"`
	SupportingPairCount int      `json:"supporting_pair_count"`
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
	for _, f := range r.Failures {
		category := f.Category
		if category == "" {
			category = "uncategorized"
		}
		lines = append(lines, fmt.Sprintf("case %s [%s]: %s", f.CaseID, category, f.Message))
	}
	return strings.Join(lines, "\n")
}
func (s Suite) HasCategory(category string) bool {
	for _, item := range s.Cases {
		for _, current := range item.Categories {
			if current == category {
				return true
			}
		}
	}
	return false
}

type manifest struct {
	EvaluationVersion string              `json:"evaluation_version"`
	ContractVersion   string              `json:"contract_version"`
	ToleranceVersion  string              `json:"tolerance_version"`
	Comparison        string              `json:"comparison"`
	WeightTolerance   float64             `json:"weight_tolerance"`
	Config            coordination.Config `json:"config"`
	Cases             []struct {
		ID         string   `json:"case_id"`
		Fixture    string   `json:"fixture"`
		Categories []string `json:"categories"`
	} `json:"cases"`
}
type fixture struct {
	FixtureID string             `json:"fixture_id"`
	Input     coordination.Input `json:"input"`
	Expected  Expected           `json:"expected"`
}

func LoadSuite(path string, validator *coordination.Validator) (Suite, error) {
	if validator == nil {
		return Suite{}, errors.New("coordination evaluation validator is required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Suite{}, fmt.Errorf("read coordination manifest: %w", err)
	}
	var definition manifest
	if err := json.Unmarshal(data, &definition); err != nil {
		return Suite{}, fmt.Errorf("decode coordination manifest: %w", err)
	}
	if definition.EvaluationVersion == "" || definition.ContractVersion != coordination.ContractVersion || definition.ToleranceVersion == "" || definition.Comparison != "exact_states_outcomes_counts_and_tolerant_weights" || definition.WeightTolerance < 0 || math.IsNaN(definition.WeightTolerance) || math.IsInf(definition.WeightTolerance, 0) || len(definition.Cases) == 0 {
		return Suite{}, errors.New("coordination manifest must declare supported versions, comparison, tolerances, and cases")
	}
	if err := coordination.ValidateConfig(definition.Config); err != nil {
		return Suite{}, fmt.Errorf("invalid coordination manifest config: %w", err)
	}
	suite := Suite{EvaluationVersion: definition.EvaluationVersion, ContractVersion: definition.ContractVersion, ToleranceVersion: definition.ToleranceVersion, WeightTolerance: definition.WeightTolerance, Cases: make([]Case, 0, len(definition.Cases))}
	for _, entry := range definition.Cases {
		if entry.ID == "" || entry.Fixture == "" || len(entry.Categories) == 0 {
			return Suite{}, errors.New("coordination case requires id, fixture, and categories")
		}
		fixturePath := filepath.Join(filepath.Dir(path), filepath.Clean(entry.Fixture))
		content, err := os.ReadFile(fixturePath)
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
		if err := coordination.ValidateInput(item.Input, definition.Config); err != nil {
			return Suite{}, fmt.Errorf("fixture %s input invalid: %w", entry.Fixture, err)
		}
		if err := validateExpected(item.Expected); err != nil {
			return Suite{}, fmt.Errorf("fixture %s expectation invalid: %w", entry.Fixture, err)
		}
		suite.Cases = append(suite.Cases, Case{ID: entry.ID, Categories: append([]string(nil), entry.Categories...), Input: item.Input, Config: definition.Config, Expected: item.Expected})
	}
	return suite, nil
}

func Evaluate(ctx context.Context, suite Suite, provider coordination.Provider, validator *coordination.Validator) (Report, error) {
	if provider == nil {
		return Report{}, errors.New("coordination evaluation provider is required")
	}
	if validator == nil {
		return Report{}, errors.New("coordination evaluation validator is required")
	}
	report := Report{EvaluationVersion: suite.EvaluationVersion, ContractVersion: suite.ContractVersion}
	for _, item := range suite.Cases {
		report.Total++
		data, err := provider.Assess(ctx, item.Input, item.Config)
		if err != nil {
			report.add(item, fmt.Sprintf("provider error: %v", err))
			continue
		}
		actual, err := validator.ValidateForInput(data, item.Input, item.Config)
		if err != nil {
			report.add(item, fmt.Sprintf("schema validation failed: %v", err))
			continue
		}
		failures := compare(item.Expected, actual, suite.WeightTolerance)
		if len(failures) == 0 {
			report.Passed++
			continue
		}
		for _, failure := range failures {
			report.add(item, failure)
		}
	}
	return report, nil
}
func (r *Report) add(item Case, message string) {
	r.Failures = append(r.Failures, Failure{CaseID: item.ID, Category: strings.Join(item.Categories, ","), Message: message})
}

func compare(expected Expected, actual coordination.Assessment, tolerance float64) []string {
	var failures []string
	if actual.CoordinationState != expected.CoordinationState {
		failures = append(failures, fmt.Sprintf("coordination_state mismatch: got %q, want %q", actual.CoordinationState, expected.CoordinationState))
	}
	actualFactors := make(map[string]coordination.Factor, len(actual.Factors))
	for _, factor := range actual.Factors {
		actualFactors[factor.Name] = factor
	}
	for _, name := range []string{"text_similarity", "media_fingerprint", "source_origin", "source_claim", "submission_timing"} {
		want, ok := expected.Factors[name]
		if !ok {
			failures = append(failures, "expected factor missing: "+name)
			continue
		}
		got, ok := actualFactors[name]
		if !ok {
			failures = append(failures, "actual factor missing: "+name)
			continue
		}
		if got.Outcome != want.Outcome {
			failures = append(failures, fmt.Sprintf("factor %s outcome mismatch: got %q, want %q", name, got.Outcome, want.Outcome))
		}
		if !sameWeight(got.Weight, want.Weight, tolerance) {
			failures = append(failures, fmt.Sprintf("factor %s weight mismatch: got %s, want %s (tolerance %g)", name, formatWeight(got.Weight), formatWeight(want.Weight), tolerance))
		}
		if got.PairCount != want.PairCount {
			failures = append(failures, fmt.Sprintf("factor %s pair_count mismatch: got %d, want %d", name, got.PairCount, want.PairCount))
		}
		if got.KnownPairCount != want.KnownPairCount {
			failures = append(failures, fmt.Sprintf("factor %s known_pair_count mismatch: got %d, want %d", name, got.KnownPairCount, want.KnownPairCount))
		}
		if got.SupportingPairCount != want.SupportingPairCount {
			failures = append(failures, fmt.Sprintf("factor %s supporting_pair_count mismatch: got %d, want %d", name, got.SupportingPairCount, want.SupportingPairCount))
		}
	}
	return failures
}
func sameWeight(left, right *float64, tolerance float64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return math.Abs(*left-*right) <= tolerance
}
func formatWeight(value *float64) string {
	if value == nil {
		return "null"
	}
	return fmt.Sprintf("%g", *value)
}
func validateExpected(e Expected) error {
	if e.CoordinationState != "possible_coordination" && e.CoordinationState != "no_signal" && e.CoordinationState != "indeterminate" {
		return fmt.Errorf("invalid coordination state %q", e.CoordinationState)
	}
	if len(e.Factors) != 5 {
		return errors.New("all five factor expectations are required")
	}
	for _, name := range []string{"text_similarity", "media_fingerprint", "source_origin", "source_claim", "submission_timing"} {
		factor, ok := e.Factors[name]
		if !ok {
			return fmt.Errorf("missing factor %s", name)
		}
		if factor.PairCount < 0 || factor.KnownPairCount < 0 || factor.SupportingPairCount < 0 {
			return fmt.Errorf("factor %s has negative counts", name)
		}
	}
	return nil
}
