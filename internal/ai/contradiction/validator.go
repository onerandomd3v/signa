package contradiction

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const schemaResource = "https://signa.local/contracts/ai/contradiction/v1/schema.json"

type Validator struct{ schema *jsonschema.Schema }

func NewValidator(schemaBytes []byte) (*Validator, error) {
	var document any
	if err := json.Unmarshal(schemaBytes, &document); err != nil {
		return nil, fmt.Errorf("decode contradiction schema: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(schemaResource, document); err != nil {
		return nil, fmt.Errorf("register contradiction schema: %w", err)
	}
	schema, err := compiler.Compile(schemaResource)
	if err != nil {
		return nil, fmt.Errorf("compile contradiction schema: %w", err)
	}
	return &Validator{schema: schema}, nil
}

func (v *Validator) Validate(data []byte) (Assessment, error) {
	if v == nil || v.schema == nil {
		return Assessment{}, fmt.Errorf("contradiction validator is not initialized")
	}
	var document any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		return Assessment{}, fmt.Errorf("decode contradiction assessment: %w", err)
	}
	if err := v.schema.Validate(document); err != nil {
		return Assessment{}, fmt.Errorf("validate contradiction assessment: %w", err)
	}
	var result Assessment
	if err := json.Unmarshal(data, &result); err != nil {
		return Assessment{}, fmt.Errorf("decode validated contradiction assessment: %w", err)
	}
	wanted := map[string]bool{"event_type": true, "location_reference": true, "claim_state": true}
	factors := make(map[string]Factor, len(result.Factors))
	for _, factor := range result.Factors {
		if !wanted[factor.Name] {
			return Assessment{}, fmt.Errorf("duplicate or unsupported contradiction factor %q", factor.Name)
		}
		allowed := map[string]bool{"no_signal": true, "unknown": true, "not_comparable": true}
		if factor.Name == "claim_state" {
			allowed["contradiction"] = true
		}
		if !allowed[factor.Outcome] {
			return Assessment{}, fmt.Errorf("factor %s has unsupported outcome %q", factor.Name, factor.Outcome)
		}
		delete(wanted, factor.Name)
		factors[factor.Name] = factor
	}
	if len(wanted) > 0 {
		return Assessment{}, fmt.Errorf("contradiction assessment missing factors: %v", sorted(wanted))
	}
	event, place, claim := factors["event_type"], factors["location_reference"], factors["claim_state"]
	wantState := "indeterminate"
	if event.Outcome == "not_comparable" || place.Outcome == "not_comparable" {
		wantState = "not_comparable"
		if claim.Outcome != "not_comparable" {
			return Assessment{}, fmt.Errorf("claim_state must be not_comparable when event or location context is not comparable")
		}
	} else if event.Outcome == "no_signal" && place.Outcome == "no_signal" {
		switch claim.Outcome {
		case "contradiction":
			wantState = "contradiction_signal"
		case "no_signal":
			wantState = "no_signal"
		}
	}
	if result.ContradictionState != wantState {
		return Assessment{}, fmt.Errorf("contradiction_state %q does not match factor outcomes %q", result.ContradictionState, wantState)
	}
	return result, nil
}

func sorted(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
