package evaluation

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/onerandomd3v/signa/internal/ai/extraction"
	"github.com/onerandomd3v/signa/internal/ai/location"
)

func TestVersionedV0LocationNormalizationSuite(t *testing.T) {
	suite := loadTestSuite(t)
	report, err := Evaluate(context.Background(), suite, location.RuleBasedNormalizer{}, testValidator(t))
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if report.Failed() {
		t.Fatalf("location normalization evaluation failed:\n%s", report)
	}
	if report.Total != 8 || report.Passed != 8 {
		t.Fatalf("evaluation counts = %d total, %d passed; want 8 total, 8 passed", report.Total, report.Passed)
	}
}

func TestVersionedV0LocationNormalizationCoversRequiredCategories(t *testing.T) {
	suite := loadTestSuite(t)
	want := []string{
		"clear_local_place",
		"vague_place",
		"same_or_similar_place_names",
		"multiple_possible_places",
		"nigerian_lagos_reference",
		"pidgin_or_mixed_language",
		"missing_location",
		"conflicting_location",
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

func TestEvaluateReportsLocationFieldFailures(t *testing.T) {
	suite := loadTestSuite(t)
	target := suite.Cases[0]
	provider := location.ProviderFunc(func(context.Context, extraction.Field) ([]byte, error) {
		return []byte(`{"contract_version":"signa.location-normalization.v0","input_contract_version":"signa.ai.report-extraction.v0","location_state":"identified","source_location_reference":{"status":"identified","value":"wrong place","candidates":[],"evidence_quotes":["wrong place"]},"candidates":[{"reported_text":"wrong place","normalized_text":"wrong place","reference_kind":"named_place","qualifier":null,"evidence_quotes":["wrong place"]}]}`), nil
	})
	report, err := Evaluate(context.Background(), Suite{EvaluationVersion: suite.EvaluationVersion, ContractVersion: suite.ContractVersion, Cases: []Case{target}}, provider, testValidator(t))
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	message := report.String()
	if !strings.Contains(message, "source_location_reference status/value mismatch") {
		t.Fatalf("report = %s, want source reference field mismatch", message)
	}
	if !strings.Contains(message, "candidate[0].reported_text mismatch") {
		t.Fatalf("report = %s, want candidate field mismatch", message)
	}
}

func TestEvaluateValidatesProviderOutputBeforeComparison(t *testing.T) {
	suite := loadTestSuite(t)
	provider := location.ProviderFunc(func(context.Context, extraction.Field) ([]byte, error) {
		return []byte(`{"contract_version":"signa.location-normalization.v0","input_contract_version":"signa.ai.report-extraction.v0","location_state":"unknown","source_location_reference":{"status":"unknown","value":null,"candidates":[],"evidence_quotes":["invented"]},"candidates":[]}`), nil
	})
	report, err := Evaluate(context.Background(), Suite{EvaluationVersion: suite.EvaluationVersion, ContractVersion: suite.ContractVersion, Cases: []Case{suite.Cases[0]}}, provider, testValidator(t))
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	message := report.String()
	if !strings.Contains(message, "output schema validation failed") {
		t.Fatalf("report = %s, want schema validation failure", message)
	}
	if strings.Contains(message, "location_state mismatch") {
		t.Fatalf("invalid output was compared after schema validation: %s", message)
	}
}

func loadTestSuite(t *testing.T) Suite {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	manifestPath := filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "contracts", "ai", "location-normalization", "v0", "evaluation.json")
	suite, err := LoadSuite(manifestPath, testValidator(t), testExtractionValidator(t))
	if err != nil {
		t.Fatalf("LoadSuite() error = %v", err)
	}
	return suite
}

func testValidator(t *testing.T) *location.Validator {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	schemaPath := filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "contracts", "ai", "location-normalization", "v0", "schema.json")
	schema, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	validator, err := location.NewValidator(schema)
	if err != nil {
		t.Fatalf("NewValidator() error = %v", err)
	}
	return validator
}

func testExtractionValidator(t *testing.T) *extraction.Validator {
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
