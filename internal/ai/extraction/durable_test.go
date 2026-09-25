package extraction

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestDecodeStoredExtractionRejectsAdditionalProperties(t *testing.T) {
	stored := storedExtractionDocument(t)
	stored["unexpected_property"] = json.RawMessage(`true`)
	assertStoredExtractionInvalid(t, stored)
}

func TestDecodeStoredExtractionRejectsInvalidFields(t *testing.T) {
	stored := storedExtractionDocument(t)
	stored["event_type"] = json.RawMessage(`{"status":"identified","value":"not-a-taxonomy-value","candidates":[],"evidence_quotes":["fire"]}`)
	assertStoredExtractionInvalid(t, stored)
}

func TestDecodeStoredExtractionReturnsValidatedStoredResult(t *testing.T) {
	store := NewDurableStore(nil, newTestValidator(t))
	encoded, err := json.Marshal(storedExtractionDocument(t))
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.decodeStoredExtraction(encoded)
	if err != nil {
		t.Fatalf("decodeStoredExtraction() error = %v", err)
	}
	if result.ContractVersion != "signa.ai.report-extraction.v0" || result.TaxonomyVersion != "signa.event-taxonomy.v0" {
		t.Fatalf("stored result versions = %+v", result)
	}
}

func assertStoredExtractionInvalid(t *testing.T, document map[string]json.RawMessage) {
	t.Helper()
	store := NewDurableStore(nil, newTestValidator(t))
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.decodeStoredExtraction(encoded); !errors.Is(err, ErrInvalidStructuredOutput) {
		t.Fatalf("decodeStoredExtraction() error = %v, want ErrInvalidStructuredOutput", err)
	}
}

func storedExtractionDocument(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	document := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(validExtractionJSON()), &document); err != nil {
		t.Fatal(err)
	}
	return document
}
