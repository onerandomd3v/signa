package evaluation

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/onerandomd3v/signa/internal/ai/extraction"
)

type testProvider func(context.Context, string) ([]byte, error)

func (p testProvider) Extract(ctx context.Context, reportText string) ([]byte, error) {
	return p(ctx, reportText)
}

func TestVersionedV0EvaluationSuite(t *testing.T) {
	suite := loadTestSuite(t)

	report, err := Evaluate(context.Background(), suite, replayProvider(suite), testValidator(t))
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if report.Failed() {
		t.Fatalf("deterministic evaluation failed:\n%s", report)
	}
	if report.Total != 13 || report.Passed != 13 {
		t.Fatalf("evaluation counts = %d total, %d passed; want 13 total, 13 passed", report.Total, report.Passed)
	}
}

func TestVersionedV0EvaluationSuiteAcceptsExplicitThresholds(t *testing.T) {
	suite := loadTestSuite(t)
	minimumPassed := len(suite.Cases)
	minimumRate := 1.0
	report, err := EvaluateWithThresholds(context.Background(), suite, replayProvider(suite), testValidator(t), &AcceptanceThresholds{
		MinimumPassed:   &minimumPassed,
		MinimumPassRate: &minimumRate,
	})
	if err != nil {
		t.Fatalf("EvaluateWithThresholds() error = %v", err)
	}
	if !report.Accepted() || report.Acceptance == nil || report.Acceptance.PassRate != 1 {
		t.Fatalf("report = %+v, want accepted complete fixture evaluation", report)
	}
}

func TestEvaluateWithThresholdsRequiresExplicitConfiguration(t *testing.T) {
	suite := loadTestSuite(t)
	if _, err := EvaluateWithThresholds(context.Background(), suite, replayProvider(suite), testValidator(t), nil); err == nil || !strings.Contains(err.Error(), "owner approval") {
		t.Fatalf("EvaluateWithThresholds() error = %v, want missing owner-approved configuration", err)
	}
}

func TestAcceptanceThresholdBoundaries(t *testing.T) {
	minimumPassed := 3
	minimumRate := 0.75
	report := Report{Total: 4, Passed: 3}
	acceptance, err := report.CheckAcceptance(AcceptanceThresholds{MinimumPassed: &minimumPassed, MinimumPassRate: &minimumRate})
	if err != nil || !acceptance.Passed {
		t.Fatalf("boundary acceptance = %+v, err = %v; want pass", acceptance, err)
	}
	minimumPassed = 4
	acceptance, err = report.CheckAcceptance(AcceptanceThresholds{MinimumPassed: &minimumPassed})
	if err != nil || acceptance.Passed {
		t.Fatalf("minimum_passed boundary = %+v, err = %v; want fail", acceptance, err)
	}
	if _, err := report.CheckAcceptance(AcceptanceThresholds{}); err == nil {
		t.Fatal("CheckAcceptance() error = nil for missing thresholds")
	}
}

func TestVersionedV0EvaluationSuiteCoversRequiredCategories(t *testing.T) {
	suite := loadTestSuite(t)
	want := []string{
		"standard_english",
		"nigerian_pidgin",
		"mixed_english_pidgin",
		"hearsay",
		"forwarded_claim",
		"incomplete_vague",
		"conflicting_time",
		"ambiguous_time",
		"conflicting_location",
		"ambiguous_location",
		"unrelated_non_incident",
		"unknown_preservation",
	}
	seen := make(map[string]bool)
	for _, testCase := range suite.Cases {
		for _, category := range testCase.Categories {
			seen[category] = true
		}
	}
	for _, category := range want {
		if !seen[category] {
			t.Errorf("required category %q is not covered", category)
		}
	}
}

func TestEvaluateReportsFieldLevelMismatch(t *testing.T) {
	suite := loadTestSuite(t)
	validator := testValidator(t)
	target := suite.Cases[0]

	report, err := Evaluate(context.Background(), suite, testProvider(func(_ context.Context, reportText string) ([]byte, error) {
		for _, testCase := range suite.Cases {
			if testCase.RawReportText != reportText {
				continue
			}
			actual := testCase.Expected
			if testCase.ID == target.ID {
				actual.EventType.Value = stringPointer("fire")
				actual.EventType.EvidenceQuotes = []string{"fire"}
			}
			return json.Marshal(actual)
		}
		return nil, fmt.Errorf("unknown report text")
	}), validator)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if !report.Failed() {
		t.Fatal("Evaluate() reported success for a mismatched event_type")
	}
	if message := report.String(); !strings.Contains(message, "event_type status/value mismatch") {
		t.Fatalf("report = %s, want event_type status/value mismatch", message)
	}
}

func TestEvaluateValidatesProviderOutputBeforeComparison(t *testing.T) {
	suite := loadTestSuite(t)
	validator := testValidator(t)
	target := suite.Cases[len(suite.Cases)-1]

	report, err := Evaluate(context.Background(), suite, testProvider(func(_ context.Context, reportText string) ([]byte, error) {
		for _, testCase := range suite.Cases {
			if testCase.RawReportText != reportText {
				continue
			}
			actual := testCase.Expected
			if testCase.ID == target.ID {
				actual.Language = extraction.Field{
					Status:         "unknown",
					Value:          nil,
					Candidates:     []string{},
					EvidenceQuotes: []string{"English"},
				}
			}
			return json.Marshal(actual)
		}
		return nil, fmt.Errorf("unknown report text")
	}), validator)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if !strings.Contains(report.String(), "output schema validation failed") {
		t.Fatalf("report = %s, want output schema validation failure", report)
	}
	if strings.Contains(report.String(), "language status/value mismatch") {
		t.Fatalf("invalid output was compared after schema validation: %s", report)
	}
}

func TestEvaluateUsesVersionedUnorderedArrayTolerances(t *testing.T) {
	suite := loadTestSuite(t)
	target := suite.Cases[11]

	report, err := Evaluate(context.Background(), suite, testProvider(func(_ context.Context, reportText string) ([]byte, error) {
		for _, testCase := range suite.Cases {
			if testCase.RawReportText != reportText {
				continue
			}
			actual := testCase.Expected
			if testCase.ID == target.ID {
				actual.TimeReference.Candidates = reverse(actual.TimeReference.Candidates)
				actual.TimeReference.EvidenceQuotes = reverse(actual.TimeReference.EvidenceQuotes)
				actual.SourceClaim.EvidenceQuotes = reverse(actual.SourceClaim.EvidenceQuotes)
			}
			return json.Marshal(actual)
		}
		return nil, fmt.Errorf("unknown report text")
	}), testValidator(t))
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if report.Failed() {
		t.Fatalf("reordered tolerated arrays failed evaluation:\n%s", report)
	}
}

func loadTestSuite(t *testing.T) Suite {
	t.Helper()
	return loadTestSuiteWithValidator(t, testValidator(t))
}

func loadTestSuiteWithValidator(t *testing.T, validator *extraction.Validator) Suite {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	manifestPath := filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "contracts", "ai", "extraction", "v0", "evaluation.json")
	suite, err := LoadSuite(manifestPath, validator)
	if err != nil {
		t.Fatalf("LoadSuite() error = %v", err)
	}
	return suite
}

func testValidator(t *testing.T) *extraction.Validator {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	schemaPath := filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "contracts", "ai", "extraction", "v0", "schema.json")
	schema, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	validator, err := extraction.NewValidator(schema)
	if err != nil {
		t.Fatalf("NewValidator() error = %v", err)
	}
	return validator
}

func replayProvider(suite Suite) extraction.Provider {
	return testProvider(func(_ context.Context, reportText string) ([]byte, error) {
		for _, testCase := range suite.Cases {
			if testCase.RawReportText == reportText {
				return json.Marshal(testCase.Expected)
			}
		}
		return nil, fmt.Errorf("unknown report text %q", reportText)
	})
}

func stringPointer(value string) *string {
	return &value
}

func reverse(values []string) []string {
	result := append([]string(nil), values...)
	sort.Sort(sort.Reverse(sort.StringSlice(result)))
	return result
}
