package evaluation_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/onerandomd3v/signa/internal/ai/coordination"
	"github.com/onerandomd3v/signa/internal/ai/coordination/evaluation"
)

func TestVersionedV1CoordinationSuite(t *testing.T) {
	validator := validator(t)
	suite, err := evaluation.LoadSuite(manifestPath(t), validator)
	if err != nil {
		t.Fatalf("LoadSuite: %v", err)
	}
	report, err := evaluation.Evaluate(context.Background(), suite, ruleBasedProvider(), validator)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if report.Failed() {
		t.Fatal(report.String())
	}
	if report.Total == 0 || report.Passed != report.Total {
		t.Fatalf("report = %+v", report)
	}
	for _, category := range []string{"exact_duplicate_text", "near_duplicate_text", "shared_media_fingerprint", "same_source_origin", "distinct_source_origins", "forwarded_or_reposted_cluster", "synchronized_submissions", "unsynchronized_submissions", "mixed_duplicate_and_distinct_origin", "sparse_or_unknown_provenance", "missing_timestamps", "repeated_without_timing", "all_known_no_repetition"} {
		if !suite.HasCategory(category) {
			t.Errorf("suite does not cover category %q", category)
		}
	}
}

func TestEvaluatorReportsFieldLevelMismatch(t *testing.T) {
	validator := validator(t)
	suite, err := evaluation.LoadSuite(manifestPath(t), validator)
	if err != nil {
		t.Fatalf("LoadSuite: %v", err)
	}
	suite.Cases[0].Expected.Factors["text_similarity"] = evaluation.ExpectedFactor{Outcome: "no_signal", Weight: floatPointer(0), PairCount: 1, KnownPairCount: 1, SupportingPairCount: 0}
	report, err := evaluation.Evaluate(context.Background(), suite, ruleBasedProvider(), validator)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !report.Failed() || !strings.Contains(report.String(), "text_similarity outcome mismatch") {
		t.Fatalf("expected field-level outcome failure, got: %s", report.String())
	}
}

func floatPointer(value float64) *float64 { return &value }

func TestEvaluatorRejectsSchemaInvalidOutputBeforeComparison(t *testing.T) {
	validator := validator(t)
	suite, err := evaluation.LoadSuite(manifestPath(t), validator)
	if err != nil {
		t.Fatalf("LoadSuite: %v", err)
	}
	provider := coordination.ProviderFunc(func(context.Context, coordination.Input, coordination.Config) ([]byte, error) {
		return []byte(`{"contract_version":"wrong"}`), nil
	})
	report, err := evaluation.Evaluate(context.Background(), suite, provider, validator)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !report.Failed() || !strings.Contains(report.String(), "schema validation failed") {
		t.Fatalf("expected schema rejection, got: %s", report.String())
	}
}

func manifestPath(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "contracts", "ai", "coordination", "v1", "evaluation.json")
}

func ruleBasedProvider() coordination.Provider {
	return coordination.ProviderFunc(func(ctx context.Context, input coordination.Input, config coordination.Config) ([]byte, error) {
		assessment, err := (coordination.RuleBasedEvaluator{}).Assess(ctx, input, config)
		if err != nil {
			return nil, err
		}
		return json.Marshal(assessment)
	})
}

func validator(t *testing.T) *coordination.Validator {
	t.Helper()
	path := filepath.Join(filepath.Dir(manifestPath(t)), "schema.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	result, err := coordination.NewValidator(data)
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}
	return result
}
