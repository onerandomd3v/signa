package outbox

import (
	"encoding/json"
	"testing"
	"time"
)

func TestMapEventReportCreatedV1PreservesStableIdentifiers(t *testing.T) {
	event := Event{ID: "event-1", EventType: "report.created", AggregateType: "report", AggregateID: "report-1", Payload: json.RawMessage(`{"report_id":"report-1"}`), CreatedAt: time.Date(2026, 9, 23, 1, 2, 3, 456000000, time.FixedZone("WAT", 3600))}
	got, err := MapEvent(event)
	if err != nil {
		t.Fatalf("MapEvent() error = %v", err)
	}
	if got.EventID != event.ID || got.EventName != ReportCreatedV1 || got.AggregateID != event.AggregateID || got.Payload != string(event.Payload) {
		t.Fatalf("mapped event = %+v", got)
	}
	if got.OccurredAt != "2026-09-23T00:02:03.456Z" {
		t.Errorf("OccurredAt = %q", got.OccurredAt)
	}
}

func TestMapEventRejectsUnsupportedType(t *testing.T) {
	if _, err := MapEvent(Event{EventType: "unknown"}); err == nil {
		t.Fatal("MapEvent() error = nil")
	}
}
