package incidents

import (
	"testing"
)

func TestParseAIProcessedUsesVersionedSnakeCasePayload(t *testing.T) {
	event, err := parseAIProcessed(map[string]any{
		"event_name":     ReportAIProcessedV1,
		"aggregate_type": "report",
		"payload":        `{"report_id":"11111111-1111-4111-8111-111111111111","extraction_id":"22222222-2222-4222-8222-222222222222","contract_version":"signa.ai.report-extraction.v0"}`,
	})
	if err != nil {
		t.Fatalf("parseAIProcessed() error = %v", err)
	}
	if event.ReportID == "" || event.ExtractionID == "" || event.ContractVersion == "" {
		t.Fatalf("parsed event = %+v", event)
	}
}
