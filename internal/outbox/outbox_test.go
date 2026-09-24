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

func TestMapEventMediaAttachedV1(t *testing.T) {
	got, err := MapEvent(Event{ID: "event-2", EventType: "report.media_attached", AggregateType: "report", AggregateID: "report-1", Payload: json.RawMessage(`{"report_id":"report-1","media_id":"media-1"}`), CreatedAt: time.Unix(0, 0)})
	if err != nil {
		t.Fatalf("MapEvent() error = %v", err)
	}
	if got.EventName != MediaAttachedV1 || got.Payload == "" {
		t.Fatalf("mapped media event = %+v", got)
	}
}

func TestMapEventIncidentAndAIEvents(t *testing.T) {
	for eventType, want := range map[string]string{
		"report.ai_processed":         ReportAIProcessedV1,
		"incident.created":            IncidentCreatedV1,
		"incident.report_attached":    IncidentReportAttachedV1,
		"incident.confidence_changed": IncidentConfidenceChangedV1,
		"incident.severity_changed":   IncidentSeverityChangedV1,
	} {
		got, err := MapEvent(Event{ID: "event-3", EventType: eventType, AggregateType: "report", AggregateID: "report-1", Payload: json.RawMessage(`{"report_id":"report-1"}`), CreatedAt: time.Unix(0, 0)})
		if err != nil || got.EventName != want {
			t.Fatalf("MapEvent(%q) = %+v, err = %v", eventType, got, err)
		}
	}
}
