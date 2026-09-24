package coordination

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const schemaResource = "https://signa.local/contracts/ai/coordination/v1/schema.json"

type Validator struct{ schema *jsonschema.Schema }

func NewValidator(schemaBytes []byte) (*Validator, error) {
	var document any
	if err := json.Unmarshal(schemaBytes, &document); err != nil {
		return nil, fmt.Errorf("decode coordination schema: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(schemaResource, document); err != nil {
		return nil, fmt.Errorf("register coordination schema: %w", err)
	}
	schema, err := compiler.Compile(schemaResource)
	if err != nil {
		return nil, fmt.Errorf("compile coordination schema: %w", err)
	}
	return &Validator{schema: schema}, nil
}

func (v *Validator) Validate(data []byte) (Assessment, error) {
	if v == nil || v.schema == nil {
		return Assessment{}, fmt.Errorf("coordination validator is not initialized")
	}
	var document any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		return Assessment{}, fmt.Errorf("decode coordination assessment: %w", err)
	}
	if err := v.schema.Validate(document); err != nil {
		return Assessment{}, fmt.Errorf("validate coordination assessment schema: %w", err)
	}
	var result Assessment
	if err := json.Unmarshal(data, &result); err != nil {
		return Assessment{}, fmt.Errorf("decode validated coordination assessment: %w", err)
	}
	if err := validateAssessment(result); err != nil {
		return Assessment{}, err
	}
	return result, nil
}

func (v *Validator) ValidateForInput(data []byte, input Input, config Config) (Assessment, error) {
	assessment, err := v.Validate(data)
	if err != nil {
		return Assessment{}, err
	}
	if err := ValidateInput(input, config); err != nil {
		return Assessment{}, err
	}
	if assessment.Config != config {
		return Assessment{}, fmt.Errorf("assessment config does not match supplied configuration")
	}
	if assessment.ObservationCount != len(input.Observations) {
		return Assessment{}, fmt.Errorf("observation_count does not match supplied input")
	}
	expectedPairCount := len(input.Observations) * (len(input.Observations) - 1) / 2
	for _, factor := range assessment.Factors {
		if factor.PairCount != expectedPairCount {
			return Assessment{}, fmt.Errorf("factor %s pair_count mismatch: got %d, want %d", factor.Name, factor.PairCount, expectedPairCount)
		}
	}
	if err := validateTimingForInput(assessment, input, config, expectedPairCount); err != nil {
		return Assessment{}, err
	}
	if err := validateEvidenceFactorsForInput(assessment, input, config); err != nil {
		return Assessment{}, err
	}
	var document any
	if err := json.Unmarshal(data, &document); err != nil {
		return Assessment{}, fmt.Errorf("decode coordination assessment: %w", err)
	}
	for _, observation := range input.Observations {
		private := append([]string(nil), observation.Evidence.MediaFingerprints...)
		if observation.Evidence.SourceOrigin != nil {
			private = append(private, *observation.Evidence.SourceOrigin)
		}
		for _, value := range private {
			if value != "" && containsOpaque(document, value) {
				return Assessment{}, fmt.Errorf("coordination assessment echoes an opaque input identifier")
			}
		}
	}
	return assessment, nil
}

func validateEvidenceFactorsForInput(assessment Assessment, input Input, config Config) error {
	// Reuse the COD-199 pairwise evaluator and COD-224 aggregate semantics so
	// evidence-derived outcomes and weights cannot be asserted by a provider.
	want, err := (RuleBasedEvaluator{}).Assess(context.Background(), input, config)
	if err != nil {
		return fmt.Errorf("recompute evidence-derived coordination factors: %w", err)
	}
	for _, name := range []string{"text_similarity", "media_fingerprint", "source_origin", "source_claim"} {
		gotFactor, wantFactor := findFactor(assessment, name), findFactor(want, name)
		if gotFactor == nil || wantFactor == nil {
			return fmt.Errorf("factor %s is missing during evidence comparison", name)
		}
		if gotFactor.Outcome != wantFactor.Outcome {
			return fmt.Errorf("%s outcome mismatch: got %q, want %q", name, gotFactor.Outcome, wantFactor.Outcome)
		}
		if gotFactor.PairCount != wantFactor.PairCount {
			return fmt.Errorf("%s pair_count mismatch: got %d, want %d", name, gotFactor.PairCount, wantFactor.PairCount)
		}
		if gotFactor.KnownPairCount != wantFactor.KnownPairCount {
			return fmt.Errorf("%s known_pair_count mismatch: got %d, want %d", name, gotFactor.KnownPairCount, wantFactor.KnownPairCount)
		}
		if gotFactor.SupportingPairCount != wantFactor.SupportingPairCount {
			return fmt.Errorf("%s supporting_pair_count mismatch: got %d, want %d", name, gotFactor.SupportingPairCount, wantFactor.SupportingPairCount)
		}
		if !sameOptionalWeight(gotFactor.Weight, wantFactor.Weight) {
			return fmt.Errorf("%s weight mismatch: got %s, want %s", name, formatWeight(gotFactor.Weight), formatWeight(wantFactor.Weight))
		}
	}
	return nil
}

func findFactor(assessment Assessment, name string) *Factor {
	for i := range assessment.Factors {
		if assessment.Factors[i].Name == name {
			return &assessment.Factors[i]
		}
	}
	return nil
}

func validateTimingForInput(assessment Assessment, input Input, config Config, pairCount int) error {
	want := assessTiming(input.Observations, config, pairCount)
	var got *Factor
	for i := range assessment.Factors {
		if assessment.Factors[i].Name == "submission_timing" {
			got = &assessment.Factors[i]
			break
		}
	}
	if got == nil {
		return fmt.Errorf("submission_timing factor is missing")
	}
	if got.Outcome != want.Outcome {
		return fmt.Errorf("submission_timing outcome mismatch: got %q, want %q", got.Outcome, want.Outcome)
	}
	if !sameOptionalWeight(got.Weight, want.Weight) {
		return fmt.Errorf("submission_timing weight mismatch: got %s, want %s", formatWeight(got.Weight), formatWeight(want.Weight))
	}
	if got.PairCount != want.PairCount {
		return fmt.Errorf("submission_timing pair_count mismatch: got %d, want %d", got.PairCount, want.PairCount)
	}
	if got.KnownPairCount != want.KnownPairCount {
		return fmt.Errorf("submission_timing known_pair_count mismatch: got %d, want %d", got.KnownPairCount, want.KnownPairCount)
	}
	if got.SupportingPairCount != want.SupportingPairCount {
		return fmt.Errorf("submission_timing supporting_pair_count mismatch: got %d, want %d", got.SupportingPairCount, want.SupportingPairCount)
	}
	return nil
}

func sameOptionalWeight(left, right *float64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func formatWeight(value *float64) string {
	if value == nil {
		return "null"
	}
	return fmt.Sprintf("%g", *value)
}

func validateAssessment(result Assessment) error {
	if result.ContractVersion != ContractVersion {
		return fmt.Errorf("unsupported coordination contract version %q", result.ContractVersion)
	}
	if result.Config.ConfigVersion != ConfigVersion || result.Config.SynchronizationWindowSeconds <= 0 {
		return fmt.Errorf("assessment has invalid configuration")
	}
	if result.ObservationCount < 2 {
		return fmt.Errorf("assessment requires at least two observations")
	}
	wanted := map[string]bool{"text_similarity": true, "media_fingerprint": true, "source_origin": true, "source_claim": true, "submission_timing": true}
	allowed := map[string]map[string]bool{
		"text_similarity":   {"repetition_risk": true, "no_signal": true, "unknown": true},
		"media_fingerprint": {"repetition_risk": true, "no_signal": true, "unknown": true},
		"source_origin":     {"repetition_risk": true, "independence_support": true, "unknown": true, "mixed_signals": true},
		"source_claim":      {"repetition_risk": true, "no_signal": true, "unknown": true},
		"submission_timing": {"synchronized": true, "unsynchronized": true, "unknown": true},
	}
	var timing string
	repeated := false
	hasUnknown := false
	for _, factor := range result.Factors {
		if !wanted[factor.Name] {
			return fmt.Errorf("duplicate or unsupported coordination factor %q", factor.Name)
		}
		if !allowed[factor.Name][factor.Outcome] {
			return fmt.Errorf("factor %s cannot have outcome %s", factor.Name, factor.Outcome)
		}
		delete(wanted, factor.Name)
		if factor.PairCount < 0 || factor.KnownPairCount < 0 || factor.SupportingPairCount < 0 || factor.KnownPairCount > factor.PairCount || factor.SupportingPairCount > factor.KnownPairCount {
			return fmt.Errorf("factor %s has inconsistent pair counts", factor.Name)
		}
		if factor.Weight != nil && (*factor.Weight < 0 || *factor.Weight > 1) {
			return fmt.Errorf("factor %s weight must be in [0,1]", factor.Name)
		}
		if factor.Reason == "" {
			return fmt.Errorf("factor %s requires a reason", factor.Name)
		}
		if factor.Name == "submission_timing" {
			timing = factor.Outcome
		}
		if factor.Outcome == "repetition_risk" || factor.Outcome == "mixed_signals" {
			repeated = true
		}
		if factor.Name != "submission_timing" && factor.Outcome == "unknown" {
			hasUnknown = true
		}
	}
	if len(wanted) > 0 {
		return fmt.Errorf("missing required coordination factors: %v", sortedNames(wanted))
	}
	wantState := "indeterminate"
	switch timing {
	case "unsynchronized":
		wantState = "no_signal"
	case "synchronized":
		if repeated {
			wantState = "possible_coordination"
		} else if !hasUnknown {
			wantState = "no_signal"
		}
	}
	if result.CoordinationState != wantState {
		return fmt.Errorf("coordination_state %q does not match factors; want %q", result.CoordinationState, wantState)
	}
	return nil
}

func containsOpaque(value any, needle string) bool {
	switch current := value.(type) {
	case string:
		if len(needle) < 4 {
			return current == needle
		}
		return strings.Contains(current, needle)
	case []any:
		for _, item := range current {
			if containsOpaque(item, needle) {
				return true
			}
		}
	case map[string]any:
		for _, item := range current {
			if containsOpaque(item, needle) {
				return true
			}
		}
	}
	return false
}
func sortedNames(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for name := range values {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}
