package incidents

import (
	"context"
	"testing"
	"time"
)

func TestLifecycleConsumerProcessesInputsAndAcknowledgesOwnOutputs(t *testing.T) {
	client := &lifecycleConsumerTestClient{}
	processor := &lifecycleConsumerTestProcessor{}
	consumer := NewLifecycleConsumer(client, processor, "incident-stream", "lifecycle-group", "worker-1", time.Second)
	ctx := context.Background()
	messages := []StreamMessage{
		{ID: "created", Values: map[string]any{"event_name": IncidentCreatedV1}},
		{ID: "attached", Values: map[string]any{"event_name": IncidentReportAttachedV1}},
		{ID: "status", Values: map[string]any{"event_name": IncidentStatusChangedV1}},
		{ID: "resolved", Values: map[string]any{"event_name": IncidentResolvedV1}},
		{ID: "confidence", Values: map[string]any{"event_name": IncidentConfidenceChangedV1}},
		{ID: "severity", Values: map[string]any{"event_name": IncidentSeverityChangedV1}},
		{ID: "unknown", Values: map[string]any{"event_name": "incident.future.v1"}},
	}
	if err := consumer.process(ctx, messages); err != nil {
		t.Fatal(err)
	}
	if len(processor.ids) != 2 || processor.ids[0] != "created" || processor.ids[1] != "attached" {
		t.Fatalf("processed ids = %v", processor.ids)
	}
	if client.acks["created"] != 1 || client.acks["attached"] != 1 || client.acks["status"] != 1 || client.acks["resolved"] != 1 || client.acks["confidence"] != 1 || client.acks["severity"] != 1 || client.acks["unknown"] != 0 {
		t.Fatalf("acks = %+v", client.acks)
	}
}

type lifecycleConsumerTestClient struct{ acks map[string]int }

func (c *lifecycleConsumerTestClient) EnsureGroup(context.Context, string, string) error { return nil }
func (c *lifecycleConsumerTestClient) Read(context.Context, string, string, string, bool, time.Duration) ([]StreamMessage, error) {
	return nil, nil
}
func (c *lifecycleConsumerTestClient) Ack(_ context.Context, _, _, id string) error {
	if c.acks == nil {
		c.acks = map[string]int{}
	}
	c.acks[id]++
	return nil
}

type lifecycleConsumerTestProcessor struct{ ids []string }

func (p *lifecycleConsumerTestProcessor) Process(_ context.Context, message StreamMessage) error {
	p.ids = append(p.ids, message.ID)
	return nil
}
