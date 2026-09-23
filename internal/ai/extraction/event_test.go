package extraction

import "testing"

func TestParseReportCreated(t *testing.T) {
	event, err := ParseReportCreated(map[string]any{
		"event_id":       "event-1",
		"event_name":     "report.created.v1",
		"aggregate_type": "report",
		"payload":        `{"report_id":"11111111-1111-4111-8111-111111111111"}`,
	})
	if err != nil {
		t.Fatalf("ParseReportCreated() error = %v", err)
	}
	if event.EventID != "event-1" || event.ReportID != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("event = %+v", event)
	}
}

func TestParseReportCreatedRejectsInvalidMessages(t *testing.T) {
	for _, test := range []struct {
		name   string
		fields map[string]any
	}{
		{name: "wrong event", fields: map[string]any{"event_id": "event-1", "event_name": "other.v1", "aggregate_type": "report", "payload": `{"report_id":"11111111-1111-4111-8111-111111111111"}`}},
		{name: "missing event id", fields: map[string]any{"event_name": "report.created.v1", "aggregate_type": "report", "payload": `{"report_id":"11111111-1111-4111-8111-111111111111"}`}},
		{name: "malformed payload", fields: map[string]any{"event_id": "event-1", "event_name": "report.created.v1", "aggregate_type": "report", "payload": `{`}},
		{name: "missing report id", fields: map[string]any{"event_id": "event-1", "event_name": "report.created.v1", "aggregate_type": "report", "payload": `{}`}},
		{name: "invalid report id", fields: map[string]any{"event_id": "event-1", "event_name": "report.created.v1", "aggregate_type": "report", "payload": `{"report_id":"not-a-uuid"}`}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ParseReportCreated(test.fields); err == nil {
				t.Fatal("ParseReportCreated() error = nil")
			}
		})
	}
}
