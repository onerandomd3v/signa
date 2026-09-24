package evaluation_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/onerandomd3v/signa/internal/ai/coordination"
	"github.com/onerandomd3v/signa/internal/ai/coordination/evaluation"
	"github.com/onerandomd3v/signa/internal/ai/extraction"
	"github.com/onerandomd3v/signa/internal/ai/independence"
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

func TestProcessorRejectsProviderTimingNotBoundToInput(t *testing.T) {
	tests := []struct {
		name      string
		input     coordination.Input
		mutate    func(*coordination.Assessment)
		wantError string
	}{
		{name: "false synchronized", input: bindingInput(2, -1, 20*time.Minute), mutate: func(a *coordination.Assessment) {
			timingFactor(a).Outcome = "synchronized"
			timingFactor(a).Weight = floatPointer(1)
			timingFactor(a).SupportingPairCount = 1
			a.CoordinationState = "indeterminate"
		}, wantError: "submission_timing outcome mismatch"},
		{name: "false unsynchronized", input: bindingInput(2, -1, time.Minute), mutate: func(a *coordination.Assessment) {
			timingFactor(a).Outcome = "unsynchronized"
			timingFactor(a).Weight = floatPointer(0)
			timingFactor(a).SupportingPairCount = 0
			a.CoordinationState = "no_signal"
		}, wantError: "submission_timing outcome mismatch"},
		{name: "missing timestamp claimed synchronized", input: bindingInput(2, 1, time.Minute), mutate: func(a *coordination.Assessment) {
			timingFactor(a).Outcome = "synchronized"
			timingFactor(a).Weight = floatPointer(1)
			timingFactor(a).KnownPairCount = 1
			timingFactor(a).SupportingPairCount = 1
		}, wantError: "submission_timing outcome mismatch"},
		{name: "wrong timing weight", input: bindingInput(2, -1, time.Minute), mutate: func(a *coordination.Assessment) { timingFactor(a).Weight = floatPointer(0) }, wantError: "submission_timing weight mismatch"},
		{name: "wrong timing known pair count", input: bindingInput(3, -1, time.Minute), mutate: func(a *coordination.Assessment) {
			timingFactor(a).KnownPairCount = 2
			timingFactor(a).SupportingPairCount = 2
		}, wantError: "submission_timing known_pair_count mismatch"},
		{name: "wrong timing supporting pair count", input: bindingInput(3, -1, time.Minute), mutate: func(a *coordination.Assessment) { timingFactor(a).SupportingPairCount = 2 }, wantError: "submission_timing supporting_pair_count mismatch"},
		{name: "wrong factor pair count for three observations", input: bindingInput(3, -1, time.Minute), mutate: func(a *coordination.Assessment) { factor(a, "media_fingerprint").PairCount = 2 }, wantError: "factor media_fingerprint pair_count mismatch"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			processor := coordination.Processor{Provider: mutatingProvider(test.mutate), Validator: loadValidator(t)}
			_, err := processor.Assess(context.Background(), test.input, bindingConfig())
			if err == nil {
				t.Fatal("expected provider output inconsistent with input to be rejected")
			}
			if !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("Assess error = %q, want it to contain %q", err, test.wantError)
			}
		})
	}
}

func TestProcessorAcceptsCorrectDeterministicTimingAndPairCounts(t *testing.T) {
	input := bindingInput(3, -1, time.Minute)
	processor := coordination.Processor{Provider: mutatingProvider(func(*coordination.Assessment) {}), Validator: loadValidator(t)}
	assessment, err := processor.Assess(context.Background(), input, bindingConfig())
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	wantPairs := 3
	for _, item := range assessment.Factors {
		if item.PairCount != wantPairs {
			t.Errorf("%s pair_count=%d, want %d", item.Name, item.PairCount, wantPairs)
		}
	}
	timing := timingFactor(&assessment)
	if timing.Outcome != "synchronized" || timing.Weight == nil || *timing.Weight != 1 || timing.KnownPairCount != wantPairs || timing.SupportingPairCount != wantPairs {
		t.Fatalf("accepted timing fields = %+v", timing)
	}
}

func TestProcessorRejectsIndependenceOutcomesUnsupportedByCOD199Factors(t *testing.T) {
	tests := []struct{ name, factorName, outcome string }{
		{name: "media cannot imply independence support", factorName: "media_fingerprint", outcome: "independence_support"},
		{name: "source claim cannot imply independence support", factorName: "source_claim", outcome: "independence_support"},
		{name: "media cannot emit aggregate mixed outcome", factorName: "media_fingerprint", outcome: "mixed_signals"},
		{name: "source claim cannot emit aggregate mixed outcome", factorName: "source_claim", outcome: "mixed_signals"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := mutatingProvider(func(a *coordination.Assessment) {
				factor(a, test.factorName).Outcome = test.outcome
				if test.outcome == "mixed_signals" {
					a.CoordinationState = "possible_coordination"
				}
			})
			processor := coordination.Processor{Provider: provider, Validator: loadValidator(t)}
			_, err := processor.Assess(context.Background(), bindingInput(2, -1, time.Minute), bindingConfig())
			if err == nil {
				t.Fatal("expected unsupported COD-199 factor outcome to be rejected")
			}
		})
	}
}

func mutatingProvider(mutate func(*coordination.Assessment)) coordination.Provider {
	return coordination.ProviderFunc(func(ctx context.Context, input coordination.Input, config coordination.Config) ([]byte, error) {
		assessment, err := (coordination.RuleBasedEvaluator{}).Assess(ctx, input, config)
		if err != nil {
			return nil, err
		}
		mutate(&assessment)
		return json.Marshal(assessment)
	})
}

func bindingInput(count, missingTimestamp int, spacing time.Duration) coordination.Input {
	base := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	input := coordination.Input{Observations: make([]coordination.Observation, count)}
	for i := range input.Observations {
		evidence := independence.Observation{EvidenceID: fmt.Sprintf("binding-%d", i), Text: fmt.Sprintf("distinct report number %d", i), SourceClaim: extraction.Field{Status: "unknown", Candidates: []string{}, EvidenceQuotes: []string{}}}
		input.Observations[i].Evidence = evidence
		if i != missingTimestamp {
			stamp := base.Add(time.Duration(i) * spacing)
			input.Observations[i].SubmittedAt = &stamp
		}
	}
	return input
}

func bindingConfig() coordination.Config {
	return coordination.Config{ConfigVersion: coordination.ConfigVersion, SynchronizationWindowSeconds: 300}
}
func loadValidator(t *testing.T) *coordination.Validator {
	t.Helper()
	schema, err := os.ReadFile(filepath.Join(filepath.Dir(manifestPath(t)), "schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	validator, err := coordination.NewValidator(schema)
	if err != nil {
		t.Fatal(err)
	}
	return validator
}
func factor(assessment *coordination.Assessment, name string) *coordination.Factor {
	for i := range assessment.Factors {
		if assessment.Factors[i].Name == name {
			return &assessment.Factors[i]
		}
	}
	panic("missing factor " + name)
}
func timingFactor(assessment *coordination.Assessment) *coordination.Factor {
	return factor(assessment, "submission_timing")
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
