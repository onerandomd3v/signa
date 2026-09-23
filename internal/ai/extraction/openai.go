package extraction

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const defaultOpenAIBaseURL = "https://api.openai.com/v1"

const extractionSystemPrompt = "Extract only evidence supported by the report into the supplied Signa v0 schema. Preserve identified, ambiguous, and unknown states exactly. Keep literal evidence quotes and raw location/time wording. Do not geocode, invent facts, verify an incident, or make confidence, final severity, priority, or alert decisions."

type OpenAIConfig struct {
	APIKey           string
	Model            string
	BaseURL          string
	HTTPClient       *http.Client
	GenerationSchema []byte
}

type OpenAIProvider struct {
	apiKey           string
	model            string
	endpoint         string
	httpClient       *http.Client
	generationSchema json.RawMessage
}

func NewOpenAIProvider(config OpenAIConfig) (*OpenAIProvider, error) {
	if strings.TrimSpace(config.APIKey) == "" {
		return nil, fmt.Errorf("OpenAI API key is required")
	}
	if strings.TrimSpace(config.Model) == "" {
		return nil, fmt.Errorf("OpenAI model is required")
	}
	if len(config.GenerationSchema) == 0 {
		return nil, fmt.Errorf("OpenAI generation schema is required")
	}
	var schemaDocument any
	if err := json.Unmarshal(config.GenerationSchema, &schemaDocument); err != nil {
		return nil, fmt.Errorf("decode OpenAI generation schema: %w", err)
	}
	if err := validateOpenAISchemaKeywords(schemaDocument); err != nil {
		return nil, err
	}
	baseURL := strings.TrimRight(config.BaseURL, "/")
	if baseURL == "" {
		baseURL = defaultOpenAIBaseURL
	}
	client := config.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	return &OpenAIProvider{
		apiKey:           config.APIKey,
		model:            config.Model,
		endpoint:         baseURL + "/chat/completions",
		httpClient:       client,
		generationSchema: append(json.RawMessage(nil), config.GenerationSchema...),
	}, nil
}

func (p *OpenAIProvider) Extract(ctx context.Context, rawText string) ([]byte, error) {
	if p == nil || p.httpClient == nil {
		return nil, fmt.Errorf("OpenAI provider is not initialized")
	}
	requestBody := struct {
		Model          string         `json:"model"`
		Messages       []message      `json:"messages"`
		ResponseFormat responseFormat `json:"response_format"`
	}{
		Model: p.model,
		Messages: []message{
			{Role: "system", Content: extractionSystemPrompt},
			{Role: "user", Content: rawText},
		},
		ResponseFormat: responseFormat{
			Type: "json_schema",
			JSONSchema: jsonSchemaFormat{
				Name:   "signa_ai_report_extraction_v0",
				Strict: true,
				Schema: p.generationSchema,
			},
		},
	}
	body, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("marshal OpenAI request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create OpenAI request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Content-Type", "application/json")
	response, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call OpenAI: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read OpenAI response: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("OpenAI returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	var envelope struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(responseBody, &envelope); err != nil {
		return nil, fmt.Errorf("decode OpenAI response: %w", err)
	}
	if len(envelope.Choices) == 0 || strings.TrimSpace(envelope.Choices[0].Message.Content) == "" {
		return nil, fmt.Errorf("OpenAI response contained no extraction content")
	}
	return []byte(envelope.Choices[0].Message.Content), nil
}

// OpenAI's strict structured-output subset does not accept the conditional
// keywords used by the canonical contract. The generation schema must remain
// a compatible request shape; the complete canonical schema is applied by the
// worker validator after the model responds.
func validateOpenAISchemaKeywords(document any) error {
	var visit func(any) error
	visit = func(value any) error {
		switch typed := value.(type) {
		case map[string]any:
			for key, child := range typed {
				switch key {
				case "allOf", "if", "then":
					return fmt.Errorf("OpenAI generation schema contains unsupported keyword %q", key)
				}
				if err := visit(child); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range typed {
				if err := visit(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return visit(document)
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type responseFormat struct {
	Type       string           `json:"type"`
	JSONSchema jsonSchemaFormat `json:"json_schema"`
}

type jsonSchemaFormat struct {
	Name   string          `json:"name"`
	Strict bool            `json:"strict"`
	Schema json.RawMessage `json:"schema"`
}
