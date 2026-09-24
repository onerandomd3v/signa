package independence

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/onerandomd3v/signa/internal/ai/extraction"
)

func TestProviderOutputRejectsIndependenceSupportFromNonOriginFactors(t *testing.T) {
	validator := testValidator(t)
	input, validOutput := supportedAssessment(t)
	for _, factorName := range []string{"text_similarity", "media_fingerprint", "source_claim"} {
		t.Run(factorName, func(t *testing.T) {
			output := assessmentWithUnsupportedSupport(t, validOutput, factorName)
			var document any
			if err := json.Unmarshal(output, &document); err != nil {
				t.Fatalf("decode provider output: %v", err)
			}
			if err := validator.schema.Validate(document); err == nil {
				t.Errorf("canonical JSON schema accepted independence_support from %s", factorName)
			}
			provider := ProviderFunc(func(context.Context, Input) ([]byte, error) { return output, nil })
			_, err := (Processor{Provider: provider, Validator: validator}).Assess(context.Background(), input)
			if err == nil {
				t.Fatalf("Processor.Assess accepted independence_support from %s", factorName)
			}
		})
	}
}

func TestValidatorEnforcesFactorOutcomeInvariantBeyondSchema(t *testing.T) {
	validator, err := NewValidator([]byte(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`))
	if err != nil {
		t.Fatalf("NewValidator() error = %v", err)
	}
	_, validOutput := supportedAssessment(t)
	for _, factorName := range []string{"text_similarity", "media_fingerprint", "source_claim"} {
		t.Run(factorName, func(t *testing.T) {
			output := assessmentWithUnsupportedSupport(t, validOutput, factorName)
			_, err := validator.Validate(output)
			if err == nil {
				t.Fatalf("Validator.Validate accepted independence_support from %s when schema permits it", factorName)
			}
			if !strings.Contains(err.Error(), "cannot have outcome independence_support") {
				t.Fatalf("Validator.Validate() error = %v, want factor/outcome invariant failure", err)
			}
		})
	}
}

func TestProviderMayEmitIndependenceSupportForSourceOrigin(t *testing.T) {
	input, _ := supportedAssessment(t)
	data, err := (Processor{Provider: RuleBasedEvaluator{}, Validator: testValidator(t)}).Assess(context.Background(), input)
	if err != nil {
		t.Fatalf("Processor.Assess() error = %v", err)
	}
	assessment, err := testValidator(t).Validate(data)
	if err != nil {
		t.Fatalf("Validator.Validate() error = %v", err)
	}
	if assessment.IndependenceState != "independence_supported" {
		t.Fatalf("independence_state = %q, want independence_supported", assessment.IndependenceState)
	}
	found := false
	for _, factor := range assessment.Factors {
		if factor.Name == "source_origin" && factor.Outcome == "independence_support" {
			found = true
		}
	}
	if !found {
		t.Fatal("assessment lacks source_origin independence_support")
	}
}

func testValidator(t *testing.T) *Validator {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	schemaPath := filepath.Join(filepath.Dir(file), "..", "..", "..", "contracts", "ai", "independence", "v1", "schema.json")
	schema, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	validator, err := NewValidator(schema)
	if err != nil {
		t.Fatalf("NewValidator() error = %v", err)
	}
	return validator
}

func supportedAssessment(t *testing.T) (Input, []byte) {
	t.Helper()
	originA, originB := "origin-a", "origin-b"
	firstHand := extraction.Field{Status: "identified", Value: stringPointer("first_hand"), Candidates: []string{}, EvidenceQuotes: []string{}}
	input := Input{
		Target:  Observation{EvidenceID: "e1", Text: "I saw smoke outside the station", SourceClaim: firstHand, SourceOrigin: &originA},
		Related: Observation{EvidenceID: "e2", Text: "Flames near the bus depot", SourceClaim: firstHand, SourceOrigin: &originB},
	}
	data, err := (RuleBasedEvaluator{}).Assess(context.Background(), input)
	if err != nil {
		t.Fatalf("RuleBasedEvaluator.Assess() error = %v", err)
	}
	return input, data
}

func assessmentWithUnsupportedSupport(t *testing.T, data []byte, factorName string) []byte {
	t.Helper()
	var assessment Assessment
	if err := json.Unmarshal(data, &assessment); err != nil {
		t.Fatalf("decode rule-based assessment: %v", err)
	}
	found := false
	for index := range assessment.Factors {
		if assessment.Factors[index].Name == factorName {
			assessment.Factors[index].Outcome = "independence_support"
			found = true
		}
	}
	if !found {
		t.Fatalf("assessment has no %s factor", factorName)
	}
	assessment.IndependenceState = "independence_supported"
	assessment.SupportingReasons = make([]string, 0)
	assessment.LimitingReasons = make([]string, 0)
	for _, factor := range assessment.Factors {
		if factor.Outcome == "independence_support" {
			assessment.SupportingReasons = append(assessment.SupportingReasons, factor.Name+": "+factor.Reason)
		}
		if factor.Outcome == "unknown" {
			assessment.LimitingReasons = append(assessment.LimitingReasons, factor.Name+": "+factor.Reason)
		}
	}
	mutated, err := json.Marshal(assessment)
	if err != nil {
		t.Fatalf("encode mutated assessment: %v", err)
	}
	return mutated
}

func stringPointer(value string) *string { return &value }
