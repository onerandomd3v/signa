package extraction

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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

	provider, err := NewOpenAIProvider(OpenAIConfig{
		APIKey:     "test-key",
		Model:      "test-model",
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
		Schema:     []byte(`{"type":"object"}`),
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
		{name: "missing key", config: OpenAIConfig{Model: "test-model", Schema: []byte(`{"type":"object"}`)}},
		{name: "missing model", config: OpenAIConfig{APIKey: "test-key", Schema: []byte(`{"type":"object"}`)}},
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
	provider, err := NewOpenAIProvider(OpenAIConfig{APIKey: "test-key", Model: "test-model", BaseURL: server.URL, HTTPClient: server.Client(), Schema: []byte(`{"type":"object"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Extract(t.Context(), "report"); err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("Extract() error = %v, want HTTP status", err)
	}
}

func TestOpenAIProviderRejectsMalformedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer server.Close()
	provider, err := NewOpenAIProvider(OpenAIConfig{APIKey: "test-key", Model: "test-model", BaseURL: server.URL, HTTPClient: server.Client(), Schema: []byte(`{"type":"object"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Extract(t.Context(), "report"); err == nil {
		t.Fatal("Extract() error = nil")
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
