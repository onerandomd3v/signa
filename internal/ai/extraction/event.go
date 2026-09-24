package extraction

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
)

const ReportCreatedV1 = "report.created.v1"
const ReportAIProcessedV1 = "report.ai_processed.v1"

type StreamMessage struct {
	ID     string
	Values map[string]any
}

type ReportCreatedEvent struct {
	EventID  string
	ReportID string
}

func ParseReportCreated(fields map[string]any) (ReportCreatedEvent, error) {
	event, err := ParseReportEvent(fields)
	if err != nil {
		return ReportCreatedEvent{}, err
	}
	if event.Name != ReportCreatedV1 {
		return ReportCreatedEvent{}, fmt.Errorf("unsupported stream event %q", event.Name)
	}
	return ReportCreatedEvent{EventID: event.ID, ReportID: event.ReportID}, nil
}

type ReportEvent struct {
	ID       string
	Name     string
	ReportID string
}

func ParseReportEvent(fields map[string]any) (ReportEvent, error) {
	eventID, err := stringField(fields, "event_id")
	if err != nil {
		return ReportEvent{}, err
	}
	eventName, err := stringField(fields, "event_name")
	if err != nil {
		return ReportEvent{}, err
	}
	aggregateType, err := stringField(fields, "aggregate_type")
	if err != nil {
		return ReportEvent{}, err
	}
	if aggregateType != "report" {
		return ReportEvent{}, fmt.Errorf("unexpected aggregate type %q", aggregateType)
	}
	payload, err := stringField(fields, "payload")
	if err != nil {
		return ReportEvent{}, err
	}
	var body struct {
		ReportID string `json:"report_id"`
	}
	if err := json.Unmarshal([]byte(payload), &body); err != nil {
		return ReportEvent{}, fmt.Errorf("decode report event payload: %w", err)
	}
	if _, err := uuid.Parse(body.ReportID); err != nil {
		return ReportEvent{}, fmt.Errorf("invalid report_id: %w", err)
	}
	return ReportEvent{ID: eventID, Name: eventName, ReportID: body.ReportID}, nil
}

func stringField(fields map[string]any, name string) (string, error) {
	value, ok := fields[name]
	if !ok {
		return "", fmt.Errorf("stream message missing %s", name)
	}
	switch value := value.(type) {
	case string:
		if value == "" {
			return "", fmt.Errorf("stream message %s is empty", name)
		}
		return value, nil
	case []byte:
		if len(value) == 0 {
			return "", fmt.Errorf("stream message %s is empty", name)
		}
		return string(value), nil
	default:
		return "", fmt.Errorf("stream message %s has type %T", name, value)
	}
}
