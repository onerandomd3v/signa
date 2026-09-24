package incidents

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestEvidencePolicyConsumerLogsFailureLeavesMessagePendingAndContinues(t *testing.T) {
	client := &confidenceConsumerClient{
		pending: []StreamMessage{{ID: "local-poison", Values: map[string]any{"event_name": IncidentCreatedV1}}},
		claimed: []StreamMessage{{ID: "stale-work", Values: map[string]any{"event_name": IncidentReportAttachedV1}}},
		new:     []StreamMessage{{ID: "new-work", Values: map[string]any{"event_name": IncidentCreatedV1}}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	processor := &confidenceConsumerProcessor{cancel: cancel}
	var output bytes.Buffer
	consumer := NewEvidencePolicyConsumer(client, processor, "incident-stream", "confidence-group", "consumer-1", time.Millisecond).WithLogger(slog.New(slog.NewTextHandler(&output, nil)))
	if err := consumer.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if client.acks["local-poison"] != 0 {
		t.Fatal("failed local message was acknowledged")
	}
	if client.acks["stale-work"] != 1 || client.acks["new-work"] != 1 {
		t.Fatalf("acks = %+v", client.acks)
	}
	if !strings.Contains(output.String(), "local-poison") || !strings.Contains(output.String(), IncidentCreatedV1) || !strings.Contains(output.String(), "confidence evaluation failed") {
		t.Fatalf("log output = %q", output.String())
	}
}

type confidenceConsumerClient struct {
	pending, claimed, new []StreamMessage
	claimCalls            int
	acks                  map[string]int
}

func (c *confidenceConsumerClient) EnsureGroup(context.Context, string, string) error { return nil }

func (c *confidenceConsumerClient) Read(ctx context.Context, _, _, _ string, pending bool, _ time.Duration) ([]StreamMessage, error) {
	if pending {
		return c.pending, nil
	}
	if len(c.new) == 0 {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	messages := c.new
	c.new = nil
	return messages, nil
}

func (c *confidenceConsumerClient) ClaimPending(context.Context, string, string, string) ([]StreamMessage, error) {
	c.claimCalls++
	if c.claimCalls == 1 {
		return c.claimed, nil
	}
	return nil, nil
}

func (c *confidenceConsumerClient) Ack(_ context.Context, _, _, id string) error {
	if c.acks == nil {
		c.acks = make(map[string]int)
	}
	c.acks[id]++
	return nil
}

type confidenceConsumerProcessor struct {
	cancel context.CancelFunc
}

func (p *confidenceConsumerProcessor) Process(_ context.Context, message StreamMessage) error {
	if message.ID == "local-poison" {
		return errors.New("database temporarily unavailable")
	}
	if message.ID == "new-work" {
		p.cancel()
	}
	return nil
}
