package similarity

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const schemaResource = "https://signa.local/contracts/ai/similarity/v1/schema.json"

type Validator struct{ schema *jsonschema.Schema }

func NewValidator(schemaBytes []byte) (*Validator, error) {
	var document any
	if err := json.Unmarshal(schemaBytes, &document); err != nil {
		return nil, fmt.Errorf("decode similarity schema: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(schemaResource, document); err != nil {
		return nil, fmt.Errorf("register similarity schema: %w", err)
	}
	schema, err := compiler.Compile(schemaResource)
	if err != nil {
		return nil, fmt.Errorf("compile similarity schema: %w", err)
	}
	return &Validator{schema: schema}, nil
}

func (v *Validator) Validate(data []byte) (Assessment, error) {
	if v == nil || v.schema == nil {
		return Assessment{}, fmt.Errorf("similarity validator is not initialized")
	}
	var document any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		return Assessment{}, fmt.Errorf("decode similarity assessment: %w", err)
	}
	if err := v.schema.Validate(document); err != nil {
		return Assessment{}, fmt.Errorf("validate similarity assessment: %w", err)
	}
	var result Assessment
	if err := json.Unmarshal(data, &result); err != nil {
		return Assessment{}, fmt.Errorf("decode validated similarity assessment: %w", err)
	}
	wantedFactors := map[string]bool{
		"event_type": true, "time_reference": true, "location_reference": true,
		"text_similarity": true, "spatial_proximity": true,
	}
	for _, factor := range result.Factors {
		if !wantedFactors[factor.Name] {
			return Assessment{}, fmt.Errorf("similarity assessment has duplicate or unsupported factor %q", factor.Name)
		}
		delete(wantedFactors, factor.Name)
	}
	if len(wantedFactors) != 0 {
		return Assessment{}, fmt.Errorf("similarity assessment is missing required factors: %v", sortedFactorNames(wantedFactors))
	}
	var sum float64
	count := 0
	expectedSupporting := make([]string, 0)
	expectedLimiting := make([]string, 0)
	for _, factor := range result.Factors {
		if factor.Score != nil {
			sum += *factor.Score
			count++
			if *factor.Score > 0 {
				expectedSupporting = append(expectedSupporting, factor.Name+": "+factor.Reason)
			}
		} else {
			expectedLimiting = append(expectedLimiting, factor.Name+": "+factor.Reason)
		}
	}
	if count == 0 {
		expectedLimiting = append(expectedLimiting, "no comparable evidence was available")
		if result.SimilarityState != "insufficient_evidence" || result.SimilarityScore != nil {
			return Assessment{}, fmt.Errorf("assessment without informative factors requires insufficient_evidence and a null score")
		}
	} else {
		want := rounded(sum / float64(count))
		if result.SimilarityState != "assessed" || result.SimilarityScore == nil || *result.SimilarityScore != want {
			return Assessment{}, fmt.Errorf("assessment score must be the arithmetic mean of informative factors: want %g", want)
		}
	}
	if !equalStrings(result.SupportingReasons, expectedSupporting) {
		return Assessment{}, fmt.Errorf("supporting_reasons must correspond to informative positive factors")
	}
	if !equalStrings(result.LimitingReasons, expectedLimiting) {
		return Assessment{}, fmt.Errorf("limiting_reasons must correspond to indeterminate factors")
	}
	return result, nil
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func sortedFactorNames(names map[string]bool) []string {
	result := make([]string, 0, len(names))
	for name := range names {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}
