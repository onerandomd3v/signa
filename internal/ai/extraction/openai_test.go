package extraction

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestOpenAIProviderSendsStructuredOutputRequest(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("request path = %q, want /chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("authorization = %q", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(body, &requestBody); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"` + strings.ReplaceAll(validExtractionJSON(), `"`, `\"`) + `"}}]}`))
	}))
	defer server.Close()
	generationSchema := readExtractionSchema(t, "openai.schema.json")

	provider, err := NewOpenAIProvider(OpenAIConfig{
		APIKey:           "test-key",
		Model:            "test-model",
		BaseURL:          server.URL,
		HTTPClient:       server.Client(),
		GenerationSchema: generationSchema,
	})
	if err != nil {
		t.Fatalf("NewOpenAIProvider() error = %v", err)
	}
	result, err := provider.Extract(t.Context(), "I heard gunshots near the old market.")
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if string(result) != validExtractionJSON() {
		t.Fatalf("Extract() = %s, want fixture JSON", result)
	}
	if requestBody["model"] != "test-model" {
		t.Fatalf("model = %v", requestBody["model"])
	}
	responseFormat, ok := requestBody["response_format"].(map[string]any)
	if !ok || responseFormat["type"] != "json_schema" {
		t.Fatalf("response_format = %#v", requestBody["response_format"])
	}
	schemaConfig, ok := responseFormat["json_schema"].(map[string]any)
	if !ok || schemaConfig["strict"] != true || schemaConfig["name"] != "signa_ai_report_extraction_v0" {
		t.Fatalf("json_schema = %#v", responseFormat["json_schema"])
	}
	if got := schemaConfig["schema"]; !reflect.DeepEqual(got, mustJSONDocument(t, generationSchema)) {
		t.Fatalf("request schema = %#v, want OpenAI generation schema", got)
	}
	if _, err := newTestValidator(t).Validate(result); err != nil {
		t.Fatalf("canonical validator rejected provider output: %v", err)
	}
	messages, ok := requestBody["messages"].([]any)
	if !ok || len(messages) != 2 {
		t.Fatalf("messages = %#v", requestBody["messages"])
	}
	if !strings.Contains(mustString(messages[1]), "I heard gunshots") {
		t.Fatalf("user message does not contain report text: %#v", messages[1])
	}
}

func TestOpenAIProviderRejectsMissingConfiguration(t *testing.T) {
	for _, test := range []struct {
		name   string
		config OpenAIConfig
	}{
		{name: "missing key", config: OpenAIConfig{Model: "test-model", GenerationSchema: []byte(`{"type":"object"}`)}},
		{name: "missing model", config: OpenAIConfig{APIKey: "test-key", GenerationSchema: []byte(`{"type":"object"}`)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewOpenAIProvider(test.config); err == nil {
				t.Fatal("NewOpenAIProvider() error = nil")
			}
		})
	}
}

func TestOpenAIProviderReturnsHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "provider unavailable", http.StatusBadGateway)
	}))
	defer server.Close()
	provider, err := NewOpenAIProvider(OpenAIConfig{APIKey: "test-key", Model: "test-model", BaseURL: server.URL, HTTPClient: server.Client(), GenerationSchema: []byte(`{"type":"object"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Extract(t.Context(), "report"); err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("Extract() error = %v, want HTTP status", err)
	}
}

func TestOpenAIProviderReportsUsageWithoutReportContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"` + strings.ReplaceAll(validExtractionJSON(), `"`, `\"`) + `"}}],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}}`))
	}))
	defer server.Close()
	provider, err := NewOpenAIProvider(OpenAIConfig{APIKey: "test-key", Model: "test-model", BaseURL: server.URL, HTTPClient: server.Client(), GenerationSchema: []byte(`{"type":"object"}`)})
	if err != nil {
		t.Fatal(err)
	}
	_, usage, err := provider.ExtractWithUsage(t.Context(), "private report text")
	if err != nil {
		t.Fatal(err)
	}
	if usage.InputTokens != 11 || usage.OutputTokens != 7 || usage.TotalTokens != 18 || usage.Attempts != 1 || !usage.InputTokensAvailable || !usage.OutputTokensAvailable || !usage.TotalTokensAvailable {
		t.Fatalf("usage = %+v", usage)
	}
}

func TestOpenAIProviderRejectsMalformedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer server.Close()
	provider, err := NewOpenAIProvider(OpenAIConfig{APIKey: "test-key", Model: "test-model", BaseURL: server.URL, HTTPClient: server.Client(), GenerationSchema: []byte(`{"type":"object"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Extract(t.Context(), "report"); err == nil {
		t.Fatal("Extract() error = nil")
	}
}

func TestOpenAIProviderRejectsCanonicalConditionalSchema(t *testing.T) {
	canonical := readExtractionSchema(t, "schema.json")
	if _, err := NewOpenAIProvider(OpenAIConfig{
		APIKey:           "test-key",
		Model:            "test-model",
		GenerationSchema: canonical,
	}); err == nil || !strings.Contains(err.Error(), "allOf") {
		t.Fatalf("NewOpenAIProvider() error = %v, want unsupported canonical-schema keyword", err)
	}
}

func TestOpenAIGenerationSchemaIsSeparateFromCanonicalSchema(t *testing.T) {
	canonical := readExtractionSchema(t, "schema.json")
	generation := readExtractionSchema(t, "openai.schema.json")
	if string(canonical) == string(generation) {
		t.Fatal("OpenAI generation schema must not be the canonical schema")
	}
	if err := validateOpenAISchemaKeywords(mustJSONDocument(t, generation)); err != nil {
		t.Fatalf("generation schema is not OpenAI-compatible: %v", err)
	}
	if err := json.Unmarshal(canonical, &map[string]any{}); err != nil {
		t.Fatalf("canonical schema is not valid JSON: %v", err)
	}
}

func mustString(value any) string {
	message, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	content, _ := message["content"].(string)
	return content
}

func readExtractionSchema(t *testing.T, name string) []byte {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(file), "..", "..", "..", "contracts", "ai", "extraction", "v0", name)
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return contents
}

func mustJSONDocument(t *testing.T, contents []byte) any {
	t.Helper()
	var document any
	if err := json.Unmarshal(contents, &document); err != nil {
		t.Fatal(err)
	}
	return document
}
