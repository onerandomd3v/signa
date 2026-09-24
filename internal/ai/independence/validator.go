package independence

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const schemaResource = "https://signa.local/contracts/ai/independence/v1/schema.json"

type Validator struct{ schema *jsonschema.Schema }

func NewValidator(schemaBytes []byte) (*Validator, error) {
	var document any
	if err := json.Unmarshal(schemaBytes, &document); err != nil {
		return nil, fmt.Errorf("decode independence schema: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(schemaResource, document); err != nil {
		return nil, fmt.Errorf("register independence schema: %w", err)
	}
	schema, err := compiler.Compile(schemaResource)
	if err != nil {
		return nil, fmt.Errorf("compile independence schema: %w", err)
	}
	return &Validator{schema: schema}, nil
}

func (v *Validator) Validate(data []byte) (Assessment, error) {
	if v == nil || v.schema == nil {
		return Assessment{}, fmt.Errorf("independence validator is not initialized")
	}
	var document any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		return Assessment{}, fmt.Errorf("decode independence assessment: %w", err)
	}
	if err := v.schema.Validate(document); err != nil {
		return Assessment{}, fmt.Errorf("validate independence assessment: %w", err)
	}
	var result Assessment
	if err := json.Unmarshal(data, &result); err != nil {
		return Assessment{}, fmt.Errorf("decode validated independence assessment: %w", err)
	}
	wanted := map[string]bool{"text_similarity": true, "media_fingerprint": true, "source_origin": true, "source_claim": true}
	allowedOutcomes := map[string]map[string]bool{
		"text_similarity":   {"repetition_risk": true, "no_signal": true, "unknown": true},
		"media_fingerprint": {"repetition_risk": true, "no_signal": true, "unknown": true},
		"source_origin":     {"repetition_risk": true, "independence_support": true, "unknown": true},
		"source_claim":      {"repetition_risk": true, "no_signal": true, "unknown": true},
	}
	risk, support := false, false
	expectedSupporting, expectedLimiting := make([]string, 0), make([]string, 0)
	for _, factor := range result.Factors {
		if !wanted[factor.Name] {
			return Assessment{}, fmt.Errorf("independence assessment has duplicate or unsupported factor %q", factor.Name)
		}
		if !allowedOutcomes[factor.Name][factor.Outcome] {
			return Assessment{}, fmt.Errorf("factor %s cannot have outcome %s", factor.Name, factor.Outcome)
		}
		delete(wanted, factor.Name)
		switch factor.Outcome {
		case "repetition_risk":
			risk = true
		case "independence_support":
			support = true
		}
		if factor.Outcome == "independence_support" {
			expectedSupporting = append(expectedSupporting, factor.Name+": "+factor.Reason)
		}
		if factor.Outcome == "unknown" {
			expectedLimiting = append(expectedLimiting, factor.Name+": "+factor.Reason)
		}
	}
	if len(wanted) != 0 {
		return Assessment{}, fmt.Errorf("independence assessment is missing required factors: %v", sortedNames(wanted))
	}
	wantState := "indeterminate"
	switch {
	case risk && support:
		wantState = "mixed_signals"
	case risk:
		wantState = "repetition_risk"
	case support:
		wantState = "independence_supported"
	}
	if result.IndependenceState != wantState {
		return Assessment{}, fmt.Errorf("independence_state %q does not match factor outcomes %q", result.IndependenceState, wantState)
	}
	if result.SimilarityMethod != SimilarityMethod || result.NearDuplicateCutoff != NearDuplicateCutoff {
		return Assessment{}, fmt.Errorf("assessment does not use the declared v1 similarity method and cutoff")
	}
	if !equalStrings(result.SupportingReasons, expectedSupporting) || !equalStrings(result.LimitingReasons, expectedLimiting) {
		return Assessment{}, fmt.Errorf("supporting_reasons or limiting_reasons do not correspond to factor outcomes")
	}
	return result, nil
}

// ValidateForInput also ensures opaque source-origin tokens and media
// fingerprints are not echoed in model-produced reasons or other output text.
func (v *Validator) ValidateForInput(data []byte, input Input) (Assessment, error) {
	assessment, err := v.Validate(data)
	if err != nil {
		return Assessment{}, err
	}
	var document any
	if err := json.Unmarshal(data, &document); err != nil {
		return Assessment{}, fmt.Errorf("decode independence assessment: %w", err)
	}
	privateValues := make([]string, 0, 2+len(input.Target.MediaFingerprints)+len(input.Related.MediaFingerprints))
	for _, observation := range []Observation{input.Target, input.Related} {
		if observation.SourceOrigin != nil {
			privateValues = append(privateValues, *observation.SourceOrigin)
		}
		privateValues = append(privateValues, observation.MediaFingerprints...)
	}
	for _, value := range privateValues {
		if value != "" && containsString(document, value) {
			return Assessment{}, fmt.Errorf("independence assessment echoes an opaque input identifier")
		}
	}
	return assessment, nil
}

func containsString(value any, needle string) bool {
	switch current := value.(type) {
	case string:
		if len(needle) < 4 {
			return current == needle
		}
		return strings.Contains(current, needle)
	case []any:
		for _, item := range current {
			if containsString(item, needle) {
				return true
			}
		}
	case map[string]any:
		for _, item := range current {
			if containsString(item, needle) {
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
func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

type Processor struct {
	Provider  Provider
	Validator *Validator
}

func (p Processor) Assess(ctx context.Context, input Input) ([]byte, error) {
	if p.Provider == nil {
		return nil, fmt.Errorf("independence provider is required")
	}
	if p.Validator == nil {
		return nil, fmt.Errorf("independence validator is required")
	}
	if err := ValidateInput(input); err != nil {
		return nil, err
	}
	data, err := p.Provider.Assess(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("assess evidence independence: %w", err)
	}
	if _, err := p.Validator.ValidateForInput(data, input); err != nil {
		return nil, err
	}
	return data, nil
}
