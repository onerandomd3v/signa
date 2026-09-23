// Package evaluation runs the deterministic, versioned location-normalization
// evaluation suite against injected providers.
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

	"github.com/onerandomd3v/signa/internal/ai/extraction"
	"github.com/onerandomd3v/signa/internal/ai/location"
)

const (
	comparisonExact     = "exact"
	comparisonUnordered = "unordered"
)

// Suite is a versioned set of location-normalization cases.
type Suite struct {
	EvaluationVersion string
	ContractVersion   string
	ToleranceVersion  string
	Cases             []Case
}

// Case contains one extracted location reference and its expected structured
// output.
type Case struct {
	ID         string
	Categories []string
	Input      extraction.Field
	Expected   location.Normalization
	Tolerances Tolerances
}

// Tolerances records versioned comparison behavior for arrays.
type Tolerances struct {
	Candidates     string `json:"candidates"`
	EvidenceQuotes string `json:"evidence_quotes"`
}

// Failure is a case-level field diagnostic.
type Failure struct {
	CaseID   string
	Category string
	Message  string
}

// Report summarizes one evaluation run.
type Report struct {
	EvaluationVersion string
	ContractVersion   string
	Total             int
	Passed            int
	Failures          []Failure
}

// Failed reports whether any case failed.
func (r Report) Failed() bool {
	return len(r.Failures) > 0
}

// String renders a CI-friendly field-level report.
func (r Report) String() string {
	lines := []string{fmt.Sprintf("%s: %d cases, %d passed", r.EvaluationVersion, r.Total, r.Passed)}
	for _, failure := range r.Failures {
		category := failure.Category
		if category == "" {
			category = "uncategorized"
		}
		lines = append(lines, fmt.Sprintf("case %s [%s]: %s", failure.CaseID, category, failure.Message))
	}
	return strings.Join(lines, "\n")
}

type manifest struct {
	EvaluationVersion string         `json:"evaluation_version"`
	ContractVersion   string         `json:"contract_version"`
	ToleranceVersion  string         `json:"tolerance_version"`
	DefaultTolerances Tolerances     `json:"default_tolerances"`
	Cases             []manifestCase `json:"cases"`
}

type manifestCase struct {
	ID         string      `json:"case_id"`
	Fixture    string      `json:"fixture"`
	Categories []string    `json:"categories"`
	Tolerances *Tolerances `json:"tolerances,omitempty"`
}

type fixture struct {
	FixtureID               string            `json:"fixture_id"`
	SourceExtractionFixture string            `json:"source_extraction_fixture"`
	InputLocationReference  *extraction.Field `json:"input_location_reference"`
	ExpectedNormalization   json.RawMessage   `json:"expected_normalization"`
}

type extractionFixture struct {
	ExpectedExtraction json.RawMessage `json:"expected_extraction"`
}

// LoadSuite loads the manifest and validates expected outputs against the
// location contract. Existing extraction fixtures are reused when referenced.
func LoadSuite(manifestPath string, validator *location.Validator, extractionValidator *extraction.Validator) (Suite, error) {
	if validator == nil {
		return Suite{}, errors.New("location evaluation validator is required")
	}
	contents, err := os.ReadFile(manifestPath)
	if err != nil {
		return Suite{}, fmt.Errorf("read location evaluation manifest: %w", err)
	}
	var definition manifest
	if err := json.Unmarshal(contents, &definition); err != nil {
		return Suite{}, fmt.Errorf("decode location evaluation manifest: %w", err)
	}
	if definition.EvaluationVersion == "" || definition.ContractVersion == "" || definition.ToleranceVersion == "" {
		return Suite{}, errors.New("location evaluation manifest must declare evaluation, contract, and tolerance versions")
	}
	if err := validateTolerances(definition.DefaultTolerances); err != nil {
		return Suite{}, fmt.Errorf("validate default tolerances: %w", err)
	}
	if len(definition.Cases) == 0 {
		return Suite{}, errors.New("location evaluation manifest must contain at least one case")
	}

	suite := Suite{
		EvaluationVersion: definition.EvaluationVersion,
		ContractVersion:   definition.ContractVersion,
		ToleranceVersion:  definition.ToleranceVersion,
		Cases:             make([]Case, 0, len(definition.Cases)),
	}
	for index, definitionCase := range definition.Cases {
		if definitionCase.ID == "" || definitionCase.Fixture == "" {
			return Suite{}, fmt.Errorf("manifest case %d must declare case_id and fixture", index)
		}
		tolerances := definition.DefaultTolerances
		if definitionCase.Tolerances != nil {
			tolerances = *definitionCase.Tolerances
		}
		if err := validateTolerances(tolerances); err != nil {
			return Suite{}, fmt.Errorf("case %s tolerances: %w", definitionCase.ID, err)
		}

		fixturePath := filepath.Join(filepath.Dir(manifestPath), filepath.Clean(definitionCase.Fixture))
		fixtureContents, err := os.ReadFile(fixturePath)
		if err != nil {
			return Suite{}, fmt.Errorf("read location fixture %s: %w", definitionCase.Fixture, err)
		}
		var fixtureDefinition fixture
		if err := json.Unmarshal(fixtureContents, &fixtureDefinition); err != nil {
			return Suite{}, fmt.Errorf("decode location fixture %s: %w", definitionCase.Fixture, err)
		}
		if fixtureDefinition.FixtureID != definitionCase.ID {
			return Suite{}, fmt.Errorf("case %s fixture_id = %q", definitionCase.ID, fixtureDefinition.FixtureID)
		}
		input, err := loadInputReference(filepath.Dir(manifestPath), fixtureDefinition, extractionValidator)
		if err != nil {
			return Suite{}, fmt.Errorf("load input for case %s: %w", definitionCase.ID, err)
		}
		if err := location.ValidateLocationReference(input); err != nil {
			return Suite{}, fmt.Errorf("validate input for case %s: %w", definitionCase.ID, err)
		}
		expected, err := validator.Validate(fixtureDefinition.ExpectedNormalization)
		if err != nil {
			return Suite{}, fmt.Errorf("validate expected output for case %s: %w", definitionCase.ID, err)
		}
		if expected.ContractVersion != definition.ContractVersion {
			return Suite{}, fmt.Errorf("case %s contract_version = %q, want %q", definitionCase.ID, expected.ContractVersion, definition.ContractVersion)
		}
		if !sameField(input, expected.SourceLocationReference, Tolerances{Candidates: comparisonExact, EvidenceQuotes: comparisonExact}) {
			return Suite{}, fmt.Errorf("case %s expected output does not preserve source_location_reference", definitionCase.ID)
		}
		suite.Cases = append(suite.Cases, Case{
			ID:         definitionCase.ID,
			Categories: append([]string(nil), definitionCase.Categories...),
			Input:      input,
			Expected:   expected,
			Tolerances: tolerances,
		})
	}
	return suite, nil
}

func loadInputReference(manifestDir string, fixtureDefinition fixture, extractionValidator *extraction.Validator) (extraction.Field, error) {
	if fixtureDefinition.SourceExtractionFixture != "" {
		if extractionValidator == nil {
			return extraction.Field{}, errors.New("extraction validator is required for source_extraction_fixture")
		}
		path := filepath.Join(manifestDir, filepath.Clean(fixtureDefinition.SourceExtractionFixture))
		contents, err := os.ReadFile(path)
		if err != nil {
			return extraction.Field{}, fmt.Errorf("read source extraction fixture: %w", err)
		}
		var source extractionFixture
		if err := json.Unmarshal(contents, &source); err != nil {
			return extraction.Field{}, fmt.Errorf("decode source extraction fixture: %w", err)
		}
		parsed, err := extractionValidator.Validate(source.ExpectedExtraction)
		if err != nil {
			return extraction.Field{}, fmt.Errorf("validate source extraction fixture: %w", err)
		}
		return parsed.LocationReference, nil
	}
	if fixtureDefinition.InputLocationReference == nil {
		return extraction.Field{}, errors.New("fixture must declare source_extraction_fixture or input_location_reference")
	}
	return *fixtureDefinition.InputLocationReference, nil
}

// Evaluate runs provider output through contract validation, then compares
// source traceability, state, and every candidate field.
func Evaluate(ctx context.Context, suite Suite, provider location.Provider, validator *location.Validator) (Report, error) {
	if provider == nil {
		return Report{}, errors.New("location evaluation provider is required")
	}
	if validator == nil {
		return Report{}, errors.New("location evaluation validator is required")
	}
	report := Report{EvaluationVersion: suite.EvaluationVersion, ContractVersion: suite.ContractVersion, Total: len(suite.Cases)}
	for _, testCase := range suite.Cases {
		output, err := provider.Normalize(ctx, testCase.Input)
		if err != nil {
			report.addFailure(testCase, fmt.Sprintf("provider error: %v", err))
			continue
		}
		actual, err := validator.Validate(output)
		if err != nil {
			report.addFailure(testCase, fmt.Sprintf("output schema validation failed: %v", err))
			continue
		}
		failures := compareNormalization(testCase.Expected, actual, testCase.Tolerances)
		if len(failures) == 0 {
			report.Passed++
			continue
		}
		for _, failure := range failures {
			report.addFailure(testCase, failure)
		}
	}
	return report, nil
}

func (r *Report) addFailure(testCase Case, message string) {
	r.Failures = append(r.Failures, Failure{CaseID: testCase.ID, Category: strings.Join(testCase.Categories, ","), Message: message})
}

func compareNormalization(expected, actual location.Normalization, tolerances Tolerances) []string {
	failures := make([]string, 0)
	if expected.ContractVersion != actual.ContractVersion {
		failures = append(failures, fmt.Sprintf("contract_version mismatch: got %q, want %q", actual.ContractVersion, expected.ContractVersion))
	}
	if expected.InputContractVersion != actual.InputContractVersion {
		failures = append(failures, fmt.Sprintf("input_contract_version mismatch: got %q, want %q", actual.InputContractVersion, expected.InputContractVersion))
	}
	if expected.LocationState != actual.LocationState {
		failures = append(failures, fmt.Sprintf("location_state mismatch: got %q, want %q", actual.LocationState, expected.LocationState))
	}
	failures = append(failures, compareField("source_location_reference", expected.SourceLocationReference, actual.SourceLocationReference, tolerances)...)
	failures = append(failures, compareCandidates(expected.Candidates, actual.Candidates, tolerances.Candidates)...)
	return failures
}

func compareField(name string, expected, actual extraction.Field, tolerances Tolerances) []string {
	failures := make([]string, 0)
	if expected.Status != actual.Status || !samePointerValue(expected.Value, actual.Value) {
		failures = append(failures, fmt.Sprintf("%s status/value mismatch: got status=%q value=%s, want status=%q value=%s", name, actual.Status, pointerValue(actual.Value), expected.Status, pointerValue(expected.Value)))
	}
	if !sameStrings(expected.Candidates, actual.Candidates, tolerances.Candidates) {
		failures = append(failures, fmt.Sprintf("%s candidates mismatch: got %v, want %v", name, actual.Candidates, expected.Candidates))
	}
	if !sameStrings(expected.EvidenceQuotes, actual.EvidenceQuotes, tolerances.EvidenceQuotes) {
		failures = append(failures, fmt.Sprintf("%s evidence_quotes mismatch: got %v, want %v", name, actual.EvidenceQuotes, expected.EvidenceQuotes))
	}
	return failures
}

func compareCandidates(expected, actual []location.Candidate, comparison string) []string {
	left := append([]location.Candidate(nil), expected...)
	right := append([]location.Candidate(nil), actual...)
	if comparison == comparisonUnordered {
		sort.Slice(left, func(i, j int) bool { return left[i].ReportedText < left[j].ReportedText })
		sort.Slice(right, func(i, j int) bool { return right[i].ReportedText < right[j].ReportedText })
	}
	failures := make([]string, 0)
	if len(left) != len(right) {
		return []string{fmt.Sprintf("candidates count mismatch: got %d, want %d", len(right), len(left))}
	}
	for index := range left {
		prefix := fmt.Sprintf("candidate[%d]", index)
		if left[index].ReportedText != right[index].ReportedText {
			failures = append(failures, fmt.Sprintf("%s.reported_text mismatch: got %q, want %q", prefix, right[index].ReportedText, left[index].ReportedText))
		}
		if left[index].NormalizedText != right[index].NormalizedText {
			failures = append(failures, fmt.Sprintf("%s.normalized_text mismatch: got %q, want %q", prefix, right[index].NormalizedText, left[index].NormalizedText))
		}
		if left[index].ReferenceKind != right[index].ReferenceKind {
			failures = append(failures, fmt.Sprintf("%s.reference_kind mismatch: got %q, want %q", prefix, right[index].ReferenceKind, left[index].ReferenceKind))
		}
		if !samePointerValue(left[index].Qualifier, right[index].Qualifier) {
			failures = append(failures, fmt.Sprintf("%s.qualifier mismatch: got %s, want %s", prefix, pointerValue(right[index].Qualifier), pointerValue(left[index].Qualifier)))
		}
		if !sameStrings(left[index].EvidenceQuotes, right[index].EvidenceQuotes, comparison) {
			failures = append(failures, fmt.Sprintf("%s.evidence_quotes mismatch: got %v, want %v", prefix, right[index].EvidenceQuotes, left[index].EvidenceQuotes))
		}
	}
	return failures
}

func validateTolerances(tolerances Tolerances) error {
	if !validComparison(tolerances.Candidates) || !validComparison(tolerances.EvidenceQuotes) {
		return fmt.Errorf("unsupported location comparison tolerance")
	}
	return nil
}

func validComparison(value string) bool {
	return value == comparisonExact || value == comparisonUnordered
}

func sameField(left, right extraction.Field, tolerances Tolerances) bool {
	return len(compareField("field", left, right, tolerances)) == 0
}

func samePointerValue(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func pointerValue(value *string) string {
	if value == nil {
		return "null"
	}
	return fmt.Sprintf("%q", *value)
}

func sameStrings(expected, actual []string, comparison string) bool {
	left := append([]string(nil), expected...)
	right := append([]string(nil), actual...)
	if comparison == comparisonUnordered {
		sort.Strings(left)
		sort.Strings(right)
	}
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
