// Package evaluation runs deterministic evaluations against the versioned AI
// extraction contract. Provider implementations are injected by callers so
// CI can replay fixtures without network or provider credentials.
package evaluation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/onerandomd3v/signa/internal/ai/extraction"
)

const (
	comparisonExact     = "exact"
	comparisonUnordered = "unordered"
)

// Suite is a versioned collection of extraction fixtures and expected outputs.
type Suite struct {
	EvaluationVersion string
	ContractVersion   string
	ToleranceVersion  string
	DefaultTolerances Tolerances
	Cases             []Case
}

// Case contains the raw report, expected canonical extraction, and the
// comparison tolerances declared for one fixture.
type Case struct {
	ID            string
	RawReportText string
	Categories    []string
	Expected      extraction.Extraction
	Tolerances    Tolerances
}

// Tolerances declares comparison behavior for fields whose array ordering is
// not semantically meaningful. The value is versioned by the containing suite.
type Tolerances struct {
	Candidates     string `json:"candidates"`
	EvidenceQuotes string `json:"evidence_quotes"`
}

// ProviderFunc adapts a function into an extraction.Provider.
type ProviderFunc func(context.Context, string) ([]byte, error)

// Extract implements extraction.Provider.
func (f ProviderFunc) Extract(ctx context.Context, reportText string) ([]byte, error) {
	if f == nil {
		return nil, errors.New("extraction evaluation provider function is nil")
	}
	return f(ctx, reportText)
}

// Failure is one case-level evaluation failure. Message contains the
// field-level diagnostic when a comparison fails.
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
	Acceptance        *AcceptanceResult
}

// Failed reports whether any case failed validation, provider execution, or
// field-by-field comparison.
func (r Report) Failed() bool {
	return len(r.Failures) > 0
}

// Accepted reports whether the evaluation passed its explicitly supplied
// acceptance configuration. Evaluate remains a comparison-only API for
// backwards compatibility; callers that need a gate must use
// EvaluateWithThresholds with owner-approved values.
func (r Report) Accepted() bool {
	if r.Acceptance != nil {
		return r.Acceptance.Passed
	}
	return !r.Failed()
}

// String renders a concise regression-test-friendly report.
func (r Report) String() string {
	lines := []string{fmt.Sprintf("%s: %d cases, %d passed", r.EvaluationVersion, r.Total, r.Passed)}
	if r.Acceptance != nil {
		lines = append(lines, fmt.Sprintf("acceptance: passed=%t pass_rate=%.4f", r.Acceptance.Passed, r.Acceptance.PassRate))
	}
	for _, failure := range r.Failures {
		category := failure.Category
		if category == "" {
			category = "uncategorized"
		}
		lines = append(lines, fmt.Sprintf("case %s [%s]: %s", failure.CaseID, category, failure.Message))
	}
	return strings.Join(lines, "\n")
}

// AcceptanceThresholds are deliberately injected rather than defaulted. The
// repository has no approved product acceptance values yet; CI or an owner
// approved evaluation job must provide at least one threshold explicitly.
type AcceptanceThresholds struct {
	MinimumPassed   *int     `json:"minimum_passed,omitempty"`
	MinimumPassRate *float64 `json:"minimum_pass_rate,omitempty"`
}

type AcceptanceResult struct {
	Passed          bool     `json:"passed"`
	PassRate        float64  `json:"pass_rate"`
	MinimumPassed   *int     `json:"minimum_passed,omitempty"`
	MinimumPassRate *float64 `json:"minimum_pass_rate,omitempty"`
}

func (t AcceptanceThresholds) Validate(total int) error {
	if total <= 0 {
		return errors.New("acceptance thresholds require a non-empty evaluation")
	}
	if t.MinimumPassed == nil && t.MinimumPassRate == nil {
		return errors.New("acceptance thresholds require an explicit minimum_passed or minimum_pass_rate")
	}
	if t.MinimumPassed != nil && (*t.MinimumPassed < 0 || *t.MinimumPassed > total) {
		return fmt.Errorf("minimum_passed must be between 0 and %d", total)
	}
	if t.MinimumPassRate != nil && (math.IsNaN(*t.MinimumPassRate) || math.IsInf(*t.MinimumPassRate, 0) || *t.MinimumPassRate < 0 || *t.MinimumPassRate > 1) {
		return errors.New("minimum_pass_rate must be between 0 and 1")
	}
	return nil
}

func (r Report) CheckAcceptance(thresholds AcceptanceThresholds) (AcceptanceResult, error) {
	if err := thresholds.Validate(r.Total); err != nil {
		return AcceptanceResult{}, err
	}
	result := AcceptanceResult{
		Passed:          true,
		PassRate:        float64(r.Passed) / float64(r.Total),
		MinimumPassed:   thresholds.MinimumPassed,
		MinimumPassRate: thresholds.MinimumPassRate,
	}
	if thresholds.MinimumPassed != nil && r.Passed < *thresholds.MinimumPassed {
		result.Passed = false
	}
	if thresholds.MinimumPassRate != nil && result.PassRate < *thresholds.MinimumPassRate {
		result.Passed = false
	}
	return result, nil
}

// EvaluateWithThresholds applies an explicitly supplied acceptance gate after
// the existing versioned fixture comparison. A nil configuration is an error;
// this prevents an unapproved default from silently becoming a CI policy.
func EvaluateWithThresholds(ctx context.Context, suite Suite, provider extraction.Provider, validator *extraction.Validator, thresholds *AcceptanceThresholds) (Report, error) {
	if thresholds == nil {
		return Report{}, errors.New("evaluation acceptance thresholds are not configured; owner approval is required")
	}
	report, err := Evaluate(ctx, suite, provider, validator)
	if err != nil {
		return Report{}, err
	}
	acceptance, err := report.CheckAcceptance(*thresholds)
	if err != nil {
		return Report{}, err
	}
	report.Acceptance = &acceptance
	return report, nil
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
	FixtureID      string          `json:"fixture_id"`
	RawReportText  string          `json:"raw_report_text"`
	ExpectedOutput json.RawMessage `json:"expected_extraction"`
}

// LoadSuite loads a manifest and its fixtures, validating every expected
// extraction against the canonical v0 schema before the suite is runnable.
func LoadSuite(manifestPath string, validator *extraction.Validator) (Suite, error) {
	if validator == nil {
		return Suite{}, errors.New("extraction evaluation validator is required")
	}
	contents, err := os.ReadFile(manifestPath)
	if err != nil {
		return Suite{}, fmt.Errorf("read evaluation manifest: %w", err)
	}
	var definition manifest
	if err := json.Unmarshal(contents, &definition); err != nil {
		return Suite{}, fmt.Errorf("decode evaluation manifest: %w", err)
	}
	if definition.EvaluationVersion == "" || definition.ContractVersion == "" || definition.ToleranceVersion == "" {
		return Suite{}, errors.New("evaluation manifest must declare evaluation, contract, and tolerance versions")
	}
	if err := validateTolerances(definition.DefaultTolerances); err != nil {
		return Suite{}, fmt.Errorf("validate default tolerances: %w", err)
	}
	if len(definition.Cases) == 0 {
		return Suite{}, errors.New("evaluation manifest must contain at least one case")
	}

	suite := Suite{
		EvaluationVersion: definition.EvaluationVersion,
		ContractVersion:   definition.ContractVersion,
		ToleranceVersion:  definition.ToleranceVersion,
		DefaultTolerances: definition.DefaultTolerances,
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
			return Suite{}, fmt.Errorf("read fixture %s: %w", definitionCase.Fixture, err)
		}
		var fixtureDefinition fixture
		if err := json.Unmarshal(fixtureContents, &fixtureDefinition); err != nil {
			return Suite{}, fmt.Errorf("decode fixture %s: %w", definitionCase.Fixture, err)
		}
		if fixtureDefinition.FixtureID != definitionCase.ID {
			return Suite{}, fmt.Errorf("case %s fixture_id = %q", definitionCase.ID, fixtureDefinition.FixtureID)
		}
		expected, err := validator.Validate(fixtureDefinition.ExpectedOutput)
		if err != nil {
			return Suite{}, fmt.Errorf("validate expected output for case %s: %w", definitionCase.ID, err)
		}
		if expected.ContractVersion != definition.ContractVersion {
			return Suite{}, fmt.Errorf("case %s contract_version = %q, want %q", definitionCase.ID, expected.ContractVersion, definition.ContractVersion)
		}
		suite.Cases = append(suite.Cases, Case{
			ID:            definitionCase.ID,
			RawReportText: fixtureDefinition.RawReportText,
			Categories:    append([]string(nil), definitionCase.Categories...),
			Expected:      expected,
			Tolerances:    tolerances,
		})
	}
	return suite, nil
}

// Evaluate runs provider output through canonical schema validation, then
// compares every versioned extraction field against its expected fixture.
func Evaluate(ctx context.Context, suite Suite, provider extraction.Provider, validator *extraction.Validator) (Report, error) {
	if provider == nil {
		return Report{}, errors.New("extraction evaluation provider is required")
	}
	if validator == nil {
		return Report{}, errors.New("extraction evaluation validator is required")
	}
	report := Report{
		EvaluationVersion: suite.EvaluationVersion,
		ContractVersion:   suite.ContractVersion,
		Total:             len(suite.Cases),
	}
	for _, testCase := range suite.Cases {
		output, err := provider.Extract(ctx, testCase.RawReportText)
		if err != nil {
			report.addFailure(testCase, fmt.Sprintf("provider error: %v", err))
			continue
		}
		actual, err := validator.Validate(output)
		if err != nil {
			report.addFailure(testCase, fmt.Sprintf("output schema validation failed: %v", err))
			continue
		}
		failures := compareExtraction(testCase.Expected, actual, testCase.Tolerances)
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
	r.Failures = append(r.Failures, Failure{
		CaseID:   testCase.ID,
		Category: strings.Join(testCase.Categories, ","),
		Message:  message,
	})
}

func validateTolerances(tolerances Tolerances) error {
	if !validComparison(tolerances.Candidates) {
		return fmt.Errorf("unsupported candidates comparison %q", tolerances.Candidates)
	}
	if !validComparison(tolerances.EvidenceQuotes) {
		return fmt.Errorf("unsupported evidence_quotes comparison %q", tolerances.EvidenceQuotes)
	}
	return nil
}

func validComparison(value string) bool {
	return value == comparisonExact || value == comparisonUnordered
}

func compareExtraction(expected, actual extraction.Extraction, tolerances Tolerances) []string {
	failures := make([]string, 0)
	if expected.ContractVersion != actual.ContractVersion {
		failures = append(failures, fmt.Sprintf("contract_version mismatch: got %q, want %q", actual.ContractVersion, expected.ContractVersion))
	}
	if expected.TaxonomyVersion != actual.TaxonomyVersion {
		failures = append(failures, fmt.Sprintf("taxonomy_version mismatch: got %q, want %q", actual.TaxonomyVersion, expected.TaxonomyVersion))
	}
	fields := []struct {
		name     string
		expected extraction.Field
		actual   extraction.Field
	}{
		{name: "event_type", expected: expected.EventType, actual: actual.EventType},
		{name: "location_reference", expected: expected.LocationReference, actual: actual.LocationReference},
		{name: "time_reference", expected: expected.TimeReference, actual: actual.TimeReference},
		{name: "source_claim", expected: expected.SourceClaim, actual: actual.SourceClaim},
		{name: "language", expected: expected.Language, actual: actual.Language},
		{name: "severity_candidate", expected: expected.SeverityCandidate, actual: actual.SeverityCandidate},
	}
	for _, field := range fields {
		failures = append(failures, compareField(field.name, field.expected, field.actual, tolerances)...)
	}
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
