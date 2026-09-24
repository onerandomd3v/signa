package evaluation

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/onerandomd3v/signa/internal/ai/independence"
)

func TestVersionedV1EvidenceIndependenceSuite(t *testing.T) {
	suite, validator := loadTestSuite(t)
	provider := independence.RuleBasedEvaluator{}
	report, err := Evaluate(context.Background(), suite, provider, validator)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if report.Failed() {
		t.Fatalf("evidence independence evaluation failed:\n%s", report)
	}
	if report.Total != 14 || report.Passed != 14 {
		t.Fatalf("evaluation counts = %d total, %d passed; want 14 comparisons, 14 passed", report.Total, report.Passed)
	}
}

func TestVersionedSuiteCoversRequiredCategories(t *testing.T) {
	suite, _ := loadTestSuite(t)
	want := []string{
		"independent_first_hand_observations",
		"exact_duplicate_text",
		"near_duplicate_reposted_text",
		"duplicate_media_fingerprint",
		"different_media_fingerprints",
		"same_source_origin",
		"distinct_source_origins",
		"forwarded_content",
		"second_hand_or_unclear_provenance",
		"unknown_provenance",
		"duplicate_text_and_same_source",
		"similar_reports_distinct_sources",
		"incomplete_evidence",
		"mixed_repetition_and_origin_signals",
	}
	seen := map[string]bool{}
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

func TestEvaluateReportsFactorLevelIndependenceFailures(t *testing.T) {
	suite, validator := loadTestSuite(t)
	target := suite.Cases[0]
	provider := independence.ProviderFunc(func(ctx context.Context, input independence.Input) ([]byte, error) {
		data, err := (independence.RuleBasedEvaluator{}).Assess(ctx, input)
		if err != nil {
			return nil, err
		}
		actual, err := validator.Validate(data)
		if err != nil {
			return nil, err
		}
		changedScore := 0.2
		actual.Factors[0].Score = &changedScore
		return json.Marshal(actual)
	})
	report, err := Evaluate(context.Background(), Suite{EvaluationVersion: suite.EvaluationVersion, ContractVersion: suite.ContractVersion, Cases: []Case{target}}, provider, validator)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if !strings.Contains(report.String(), "factor text_similarity score mismatch") {
		t.Fatalf("report = %s, want text_similarity score mismatch", report)
	}
}

func TestEvaluateRejectsInvalidStructuredOutputBeforeComparison(t *testing.T) {
	suite, validator := loadTestSuite(t)
	provider := independence.ProviderFunc(func(context.Context, independence.Input) ([]byte, error) {
		return []byte(`{"contract_version":"signa.ai.evidence-independence.v1"}`), nil
	})
	report, err := Evaluate(context.Background(), Suite{EvaluationVersion: suite.EvaluationVersion, ContractVersion: suite.ContractVersion, Cases: []Case{suite.Cases[0]}}, provider, validator)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if !strings.Contains(report.String(), "output schema validation failed") {
		t.Fatalf("report = %s, want schema validation failure", report)
	}
	if strings.Contains(report.String(), "independence_state mismatch") {
		t.Fatalf("invalid output was compared: %s", report)
	}
}

func TestEvaluateRejectsOpaqueSourceTokenEcho(t *testing.T) {
	suite, validator := loadTestSuite(t)
	target := suite.Cases[0]
	provider := independence.ProviderFunc(func(ctx context.Context, input independence.Input) ([]byte, error) {
		data, err := (independence.RuleBasedEvaluator{}).Assess(ctx, input)
		if err != nil {
			return nil, err
		}
		var output independence.Assessment
		if err := json.Unmarshal(data, &output); err != nil {
			return nil, err
		}
		output.Factors[2].Reason += ": origin-a"
		output.SupportingReasons[0] = output.Factors[2].Name + ": " + output.Factors[2].Reason
		return json.Marshal(output)
	})
	report, err := Evaluate(context.Background(), Suite{EvaluationVersion: suite.EvaluationVersion, ContractVersion: suite.ContractVersion, Cases: []Case{target}}, provider, validator)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if !strings.Contains(report.String(), "output schema validation failed: independence assessment echoes an opaque input identifier") {
		t.Fatalf("report = %s, want opaque source token rejection", report)
	}
}

func loadTestSuite(t *testing.T) (Suite, *independence.Validator) {
	t.Helper()
	validator := testValidator(t)
	suite, err := LoadSuite(testManifestPath(t), validator)
	if err != nil {
		t.Fatalf("LoadSuite() error = %v", err)
	}
	return suite, validator
}

func testManifestPath(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "contracts", "ai", "independence", "v1", "evaluation.json")
}

func testValidator(t *testing.T) *independence.Validator {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	schemaPath := filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "contracts", "ai", "independence", "v1", "schema.json")
	schema, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	validator, err := independence.NewValidator(schema)
	if err != nil {
		t.Fatalf("NewValidator() error = %v", err)
	}
	return validator
}
