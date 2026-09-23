package extraction

import (
	"bytes"
	"encoding/json"
	"fmt"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const schemaResource = "https://signa.local/contracts/ai/extraction/v0/schema.json"

// Validator validates extraction JSON against the checked-in v0 JSON Schema
// before decoding it into the application type.
type Validator struct {
	schema *jsonschema.Schema
}

func NewValidator(schemaBytes []byte) (*Validator, error) {
	var document any
	if err := json.Unmarshal(schemaBytes, &document); err != nil {
		return nil, fmt.Errorf("decode extraction schema: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(schemaResource, document); err != nil {
		return nil, fmt.Errorf("register extraction schema: %w", err)
	}
	schema, err := compiler.Compile(schemaResource)
	if err != nil {
		return nil, fmt.Errorf("compile extraction schema: %w", err)
	}
	return &Validator{schema: schema}, nil
}

func (v *Validator) Validate(data []byte) (Extraction, error) {
	if v == nil || v.schema == nil {
		return Extraction{}, fmt.Errorf("extraction validator is not initialized")
	}
	var document any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		return Extraction{}, fmt.Errorf("decode extraction: %w", err)
	}
	if err := v.schema.Validate(document); err != nil {
		return Extraction{}, fmt.Errorf("validate extraction: %w", err)
	}
	var extraction Extraction
	if err := json.Unmarshal(data, &extraction); err != nil {
		return Extraction{}, fmt.Errorf("decode validated extraction: %w", err)
	}
	return extraction, nil
}
