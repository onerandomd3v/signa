package location

import (
	"bytes"
	"encoding/json"
	"fmt"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const schemaResource = "https://signa.local/contracts/ai/location-normalization/v0/schema.json"

// Validator validates location-normalization JSON against the checked-in v0
// schema before decoding it into the application type.
type Validator struct {
	schema *jsonschema.Schema
}

// NewValidator compiles the canonical location-normalization schema.
func NewValidator(schemaBytes []byte) (*Validator, error) {
	var document any
	if err := json.Unmarshal(schemaBytes, &document); err != nil {
		return nil, fmt.Errorf("decode location normalization schema: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(schemaResource, document); err != nil {
		return nil, fmt.Errorf("register location normalization schema: %w", err)
	}
	schema, err := compiler.Compile(schemaResource)
	if err != nil {
		return nil, fmt.Errorf("compile location normalization schema: %w", err)
	}
	return &Validator{schema: schema}, nil
}

// Validate checks schema validity and returns the decoded normalization.
func (v *Validator) Validate(data []byte) (Normalization, error) {
	if v == nil || v.schema == nil {
		return Normalization{}, fmt.Errorf("location normalization validator is not initialized")
	}
	var document any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		return Normalization{}, fmt.Errorf("decode location normalization: %w", err)
	}
	if err := v.schema.Validate(document); err != nil {
		return Normalization{}, fmt.Errorf("validate location normalization: %w", err)
	}
	var normalization Normalization
	if err := json.Unmarshal(data, &normalization); err != nil {
		return Normalization{}, fmt.Errorf("decode validated location normalization: %w", err)
	}
	if normalization.LocationState != normalization.SourceLocationReference.Status {
		return Normalization{}, fmt.Errorf("location_state %q does not match source_location_reference status %q", normalization.LocationState, normalization.SourceLocationReference.Status)
	}
	return normalization, nil
}
