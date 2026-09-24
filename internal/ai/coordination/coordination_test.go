package coordination_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/onerandomd3v/signa/internal/ai/coordination"
	"github.com/onerandomd3v/signa/internal/ai/extraction"
	"github.com/onerandomd3v/signa/internal/ai/independence"
)

func TestRuleBasedEvaluatorPreservesPairwiseSignalsAndTiming(t *testing.T) {
	tests := []struct {
		name         string
		observations []coordination.Observation
		wantState    string
		wantFactors  map[string]string
	}{
		{name: "exact repeated text synchronized", observations: observations(t, []string{"road blocked now", "road blocked now"}, true), wantState: "possible_coordination", wantFactors: map[string]string{"text_similarity": "repetition_risk", "submission_timing": "synchronized"}},
		{name: "near duplicate text", observations: observations(t, []string{"road blocked by police now", "road blocked by police"}, false), wantState: "indeterminate", wantFactors: map[string]string{"text_similarity": "repetition_risk", "submission_timing": "unknown"}},
		{name: "shared media synchronized", observations: withMedia(observations(t, []string{"first report", "second report"}, true), "fp-private-1234"), wantState: "possible_coordination", wantFactors: map[string]string{"media_fingerprint": "repetition_risk"}},
		{name: "same opaque origin", observations: withOrigin(observations(t, []string{"first report", "second report"}, true), "origin-private-1234"), wantState: "possible_coordination", wantFactors: map[string]string{"source_origin": "repetition_risk"}},
		{name: "forwarded provenance", observations: withClaim(observations(t, []string{"first report", "second report"}, true), "forwarded"), wantState: "possible_coordination", wantFactors: map[string]string{"source_claim": "repetition_risk"}},
		{name: "distinct origins and similar reports", observations: withOrigins(observations(t, []string{"road blocked now", "road blocked now"}, true), "origin-private-1234", "origin-private-5678"), wantState: "possible_coordination", wantFactors: map[string]string{"source_origin": "independence_support"}},
		{name: "unsynchronized independent-looking", observations: unsynchronizedObservations(t, []string{"report alpha", "report beta"}), wantState: "no_signal", wantFactors: map[string]string{"submission_timing": "unsynchronized"}},
		{name: "mixed repetition and distinct origins", observations: withOrigins(observations(t, []string{"same report text", "same report text"}, true), "origin-private-1234", "origin-private-5678"), wantState: "possible_coordination", wantFactors: map[string]string{"text_similarity": "repetition_risk", "source_origin": "independence_support"}},
		{name: "sparse provenance", observations: observations(t, []string{"alpha", "beta"}, true), wantState: "indeterminate", wantFactors: map[string]string{"source_origin": "unknown", "source_claim": "unknown"}},
		{name: "missing timestamps", observations: observations(t, []string{"same report", "same report"}, false), wantState: "indeterminate", wantFactors: map[string]string{"submission_timing": "unknown"}},
		{name: "repetition without timing stays indeterminate", observations: observations(t, []string{"same report", "same report"}, false), wantState: "indeterminate", wantFactors: map[string]string{"text_similarity": "repetition_risk"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assessment := assess(t, tt.observations)
			if assessment.CoordinationState != tt.wantState {
				t.Fatalf("coordination_state = %q, want %q", assessment.CoordinationState, tt.wantState)
			}
			factors := factorMap(assessment)
			for name, want := range tt.wantFactors {
				if got := factors[name].Outcome; got != want {
					t.Errorf("factor %s outcome = %q, want %q", name, got, want)
				}
			}
		})
	}
}

func TestProcessorRejectsOpaqueInputEchoes(t *testing.T) {
	for _, secret := range []string{"origin-private-1234", "fp-private-1234"} {
		t.Run(secret, func(t *testing.T) {
			input := observations(t, []string{"one", "two"}, true)
			if strings.HasPrefix(secret, "origin") {
				input = withOrigin(input, secret)
			} else {
				input = withMedia(input, secret)
			}
			validator := testValidator(t)
			provider := coordination.ProviderFunc(func(context.Context, coordination.Input, coordination.Config) ([]byte, error) {
				assessment := assess(t, input)
				assessment.Factors[0].Reason = "contains " + secret
				return json.Marshal(assessment)
			})
			processor := coordination.Processor{Provider: provider, Validator: validator}
			if _, err := processor.Assess(context.Background(), coordination.Input{Observations: input}, testConfig()); err == nil {
				t.Fatal("expected opaque input echo to be rejected")
			}
		})
	}
}

func TestProcessorBindsConfigAndObservationCount(t *testing.T) {
	input := observations(t, []string{"same", "same"}, true)
	provider := coordination.ProviderFunc(func(context.Context, coordination.Input, coordination.Config) ([]byte, error) {
		return json.Marshal(assess(t, input))
	})
	processor := coordination.Processor{Provider: provider, Validator: testValidator(t)}
	if _, err := processor.Assess(context.Background(), coordination.Input{Observations: input}, testConfig()); err != nil {
		t.Fatalf("Assess: %v", err)
	}
	bad := testConfig()
	bad.SynchronizationWindowSeconds++
	if _, err := processor.Assess(context.Background(), coordination.Input{Observations: input}, bad); err == nil {
		t.Fatal("expected mismatched config binding to be rejected")
	}
	three := observations(t, []string{"same", "same", "same"}, true)
	if _, err := processor.Assess(context.Background(), coordination.Input{Observations: three}, testConfig()); err == nil {
		t.Fatal("expected mismatched observation count to be rejected")
	}
}

func TestValidatorRejectsInvalidFactorOutcomeAndUnknownTimingState(t *testing.T) {
	input := observations(t, []string{"same report", "same report"}, false)
	for _, test := range []struct {
		name   string
		mutate func(*coordination.Assessment)
	}{
		{name: "text cannot claim independence support", mutate: func(a *coordination.Assessment) { a.Factors[0].Outcome = "independence_support" }},
		{name: "missing timestamps cannot produce coordination state", mutate: func(a *coordination.Assessment) { a.CoordinationState = "possible_coordination" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider := coordination.ProviderFunc(func(context.Context, coordination.Input, coordination.Config) ([]byte, error) {
				assessment, err := (coordination.RuleBasedEvaluator{}).Assess(context.Background(), coordination.Input{Observations: input}, testConfig())
				if err != nil {
					return nil, err
				}
				test.mutate(&assessment)
				return json.Marshal(assessment)
			})
			processor := coordination.Processor{Provider: provider, Validator: testValidator(t)}
			if _, err := processor.Assess(context.Background(), coordination.Input{Observations: input}, testConfig()); err == nil {
				t.Fatal("expected invalid provider output to be rejected")
			}
		})
	}
}

func assess(t *testing.T, observations []coordination.Observation) coordination.Assessment {
	t.Helper()
	result, err := (coordination.RuleBasedEvaluator{}).Assess(context.Background(), coordination.Input{Observations: observations}, testConfig())
	if err != nil {
		t.Fatalf("RuleBasedEvaluator.Assess: %v", err)
	}
	return result
}

func observations(t *testing.T, texts []string, synchronized bool) []coordination.Observation {
	t.Helper()
	result := make([]coordination.Observation, len(texts))
	base := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	for i, text := range texts {
		obs := coordination.Observation{Evidence: independence.Observation{EvidenceID: "evidence-" + string(rune('a'+i)), Text: text, SourceClaim: extraction.Field{Status: "unknown", Candidates: []string{}, EvidenceQuotes: []string{}}}}
		if synchronized {
			submitted := base.Add(time.Duration(i) * time.Minute)
			obs.SubmittedAt = &submitted
		}
		result[i] = obs
	}
	return result
}

func unsynchronizedObservations(t *testing.T, texts []string) []coordination.Observation {
	result := observations(t, texts, true)
	for i := range result {
		submitted := time.Date(2026, 9, 25, 12, i*20, 0, 0, time.UTC)
		result[i].SubmittedAt = &submitted
	}
	return result
}

func withMedia(in []coordination.Observation, fingerprint string) []coordination.Observation {
	for i := range in {
		in[i].Evidence.MediaFingerprints = []string{fingerprint}
	}
	return in
}
func withOrigin(in []coordination.Observation, origin string) []coordination.Observation {
	return withOrigins(in, origin, origin)
}
func withOrigins(in []coordination.Observation, first, second string) []coordination.Observation {
	in[0].Evidence.SourceOrigin = &first
	in[1].Evidence.SourceOrigin = &second
	return in
}
func withClaim(in []coordination.Observation, claim string) []coordination.Observation {
	value := claim
	for i := range in {
		in[i].Evidence.SourceClaim = extraction.Field{Status: "identified", Value: &value, Candidates: []string{}, EvidenceQuotes: []string{}}
	}
	return in
}
func factorMap(assessment coordination.Assessment) map[string]coordination.Factor {
	result := make(map[string]coordination.Factor)
	for _, factor := range assessment.Factors {
		result[factor.Name] = factor
	}
	return result
}
func testConfig() coordination.Config {
	return coordination.Config{ConfigVersion: coordination.ConfigVersion, SynchronizationWindowSeconds: 300}
}

func testValidator(t *testing.T) *coordination.Validator {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(file), "..", "..", "..", "contracts", "ai", "coordination", "v1", "schema.json")
	schema, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	validator, err := coordination.NewValidator(schema)
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}
	return validator
}
