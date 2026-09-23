package extraction

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func TestFixturesValidate(t *testing.T) {
	validator := newTestValidator(t)
	_, file, _, _ := runtime.Caller(0)
	fixtureDir := filepath.Join(filepath.Dir(file), "..", "..", "..", "contracts", "ai", "extraction", "v0", "fixtures")
	paths, err := filepath.Glob(filepath.Join(fixtureDir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)
	if len(paths) != 11 {
		t.Fatalf("fixture count = %d, want 11", len(paths))
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			fixture := struct {
				Expected json.RawMessage `json:"expected_extraction"`
			}{}
			contents, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(contents, &fixture); err != nil {
				t.Fatal(err)
			}
			if _, err := validator.Validate(fixture.Expected); err != nil {
				t.Fatalf("fixture extraction invalid: %v", err)
			}
		})
	}
}

func TestFieldStateSemanticsAreSchemaEnforced(t *testing.T) {
	validator := newTestValidator(t)
	base := validExtractionJSON()
	for _, test := range []struct {
		name   string
		field  string
		value  string
		wantIn string
	}{
		{name: "identified requires evidence and value", field: "event_type", value: `{"status":"identified","value":null,"candidates":[],"evidence_quotes":[]}`, wantIn: "event_type"},
		{name: "ambiguous requires candidates and null value", field: "event_type", value: `{"status":"ambiguous","value":null,"candidates":["possible_gunfire"],"evidence_quotes":["maybe"]}`, wantIn: "event_type"},
		{name: "unknown cannot carry evidence", field: "time_reference", value: `{"status":"unknown","value":null,"candidates":[],"evidence_quotes":["today"]}`, wantIn: "time_reference"},
	} {
		t.Run(test.name, func(t *testing.T) {
			updated := strings.Replace(base, `"`+test.field+`":`+fieldJSON(base, test.field), `"`+test.field+`":`+test.value, 1)
			if _, err := validator.Validate([]byte(updated)); err == nil {
				t.Fatalf("Validate() error = nil, want invalid %s", test.wantIn)
			}
		})
	}
}

func newTestValidator(t *testing.T) *Validator {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	schemaPath := filepath.Join(filepath.Dir(file), "..", "..", "..", "contracts", "ai", "extraction", "v0", "schema.json")
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

func validExtractionJSON() string {
	return `{"contract_version":"signa.ai.report-extraction.v0","taxonomy_version":"signa.event-taxonomy.v0","event_type":{"status":"identified","value":"fire","candidates":[],"evidence_quotes":["fire"]},"location_reference":{"status":"unknown","value":null,"candidates":[],"evidence_quotes":[]},"time_reference":{"status":"unknown","value":null,"candidates":[],"evidence_quotes":[]},"source_claim":{"status":"unknown","value":null,"candidates":[],"evidence_quotes":[]},"language":{"status":"identified","value":"en","candidates":[],"evidence_quotes":["fire"]},"severity_candidate":{"status":"identified","value":"HIGH","candidates":[],"evidence_quotes":["fire"]}}`
}

func fieldJSON(object, field string) string {
	key := `"` + field + `":`
	start := strings.Index(object, key)
	if start < 0 {
		return ""
	}
	start += len(key)
	depth := 0
	for index := start; index < len(object); index++ {
		switch object[index] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return object[start : index+1]
			}
		}
	}
	return ""
}
