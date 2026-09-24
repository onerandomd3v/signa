package evaluation

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/onerandomd3v/signa/internal/ai/similarity"
)

func TestVersionedV1SimilaritySuite(t *testing.T) {
	suite, processor := loadTestSuite(t)
	report, err := Evaluate(context.Background(), suite, processor, processor.Validator)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if report.Failed() {
		t.Fatalf("similarity evaluation failed:\n%s", report)
	}
	if report.Total != 11 || report.Passed != 11 {
		t.Fatalf("evaluation counts = %d total, %d passed; want 11 candidate assessments, 11 passed", report.Total, report.Passed)
	}
}

func TestEvaluateReportsFieldLevelSimilarityFailures(t *testing.T) {
	suite, processor := loadTestSuite(t)
	target := suite.Cases[0]
	provider := similarity.ProviderFunc(func(_ context.Context, _ similarity.ReportEvidence, _ similarity.CandidateIncident) ([]byte, error) {
		return []byte(`{"contract_version":"signa.ai.same-event-similarity.v1","input_contract_version":"signa.ai.report-extraction.v0","candidate_incident_id":"incident-1","similarity_state":"assessed","similarity_score":0.25,"scoring_method":"arithmetic_mean_of_informative_factors.v1","factors":[{"name":"event_type","status":"informative","score":0.25,"reason":"test"},{"name":"time_reference","status":"informative","score":0.25,"reason":"test"},{"name":"location_reference","status":"informative","score":0.25,"reason":"test"},{"name":"text_similarity","status":"informative","score":0.25,"reason":"test"},{"name":"spatial_proximity","status":"informative","score":0.25,"reason":"test"}],"supporting_reasons":[],"limiting_reasons":[]}`), nil
	})
	report, err := Evaluate(context.Background(), Suite{EvaluationVersion: suite.EvaluationVersion, ContractVersion: suite.ContractVersion, Cases: []Case{target}}, provider, processor.Validator)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	message := report.String()
	for _, field := range []string{"similarity_score mismatch", "factor event_type score mismatch"} {
		if !strings.Contains(message, field) {
			t.Errorf("report = %s, want %q", message, field)
		}
	}
}

func TestEvaluateRejectsInvalidProviderOutputBeforeScoring(t *testing.T) {
	suite, processor := loadTestSuite(t)
	provider := similarity.ProviderFunc(func(_ context.Context, _ similarity.ReportEvidence, _ similarity.CandidateIncident) ([]byte, error) {
		return []byte(`{"contract_version":"signa.ai.same-event-similarity.v1","candidate_incident_id":"candidate-1","similarity_score":2,"factors":[]}`), nil
	})
	report, err := Evaluate(context.Background(), Suite{EvaluationVersion: suite.EvaluationVersion, ContractVersion: suite.ContractVersion, Cases: []Case{suite.Cases[0]}}, provider, processor.Validator)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if !strings.Contains(report.String(), "output schema validation failed") {
		t.Fatalf("report = %s, want schema validation failure", report)
	}
	if strings.Contains(report.String(), "similarity_score mismatch") {
		t.Fatalf("invalid output was compared: %s", report)
	}
}

func TestVersionedSuiteCoversRequiredCategories(t *testing.T) {
	suite, _ := loadTestSuite(t)
	want := []string{"clearly_same_event", "different_event_type", "same_type_distant_context", "compatible_time_location", "conflicting_time", "ambiguous_unknown_location", "sparse_evidence", "near_duplicate_wording", "unrelated_report", "multiple_candidates"}
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

func loadTestSuite(t *testing.T) (Suite, similarity.Processor) {
	t.Helper()
	validator := testValidator(t)
	suite, err := LoadSuite(testManifestPath(t), validator)
	if err != nil {
		t.Fatalf("LoadSuite() error = %v", err)
	}
	processor := similarity.Processor{Provider: similarity.RuleBasedScorer{}, Validator: validator}
	return suite, processor
}

func testManifestPath(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "contracts", "ai", "similarity", "v1", "evaluation.json")
}

func testValidator(t *testing.T) *similarity.Validator {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	schemaPath := filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "contracts", "ai", "similarity", "v1", "schema.json")
	schema, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	validator, err := similarity.NewValidator(schema)
	if err != nil {
		t.Fatalf("NewValidator() error = %v", err)
	}
	return validator
}
