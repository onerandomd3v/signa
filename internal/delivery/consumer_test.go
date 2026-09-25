package delivery

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onerandomd3v/signa/internal/ai/extraction"
)

func TestConsumerAcknowledgesAfterDurableOutcome(t *testing.T) {
	client := &deliveryStreamClient{messages: []extraction.StreamMessage{deliveryMessage("delivery-consumer", "alert-1", "key-consumer")}}
	store := newMemoryStore(2)
	consumer := NewConsumer(client, NewProcessor(store, &recordingAdapter{}, 0), "stream", "group", "consumer", time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { time.Sleep(10 * time.Millisecond); cancel() }()
	if err := consumer.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(client.acks) != 1 {
		t.Fatalf("acks = %d, want 1", len(client.acks))
	}
}

func TestConsumerLeavesTransientFailurePendingForRedelivery(t *testing.T) {
	client := &deliveryStreamClient{messages: []extraction.StreamMessage{deliveryMessage("delivery-retry", "alert-1", "key-retry")}}
	store := newMemoryStore(2)
	adapter := &recordingAdapter{errors: []error{Transient(errors.New("temporary")), nil}}
	consumer := NewConsumer(client, NewProcessor(store, adapter, time.Millisecond), "stream", "group", "consumer", time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { time.Sleep(25 * time.Millisecond); cancel() }()
	if err := consumer.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if adapter.calls != 2 || len(client.acks) != 1 {
		t.Fatalf("calls/acks = %d/%d, want 2/1", adapter.calls, len(client.acks))
	}
}

type deliveryStreamClient struct {
	messages []extraction.StreamMessage
	pending  []extraction.StreamMessage
	acks     []string
}

func (c *deliveryStreamClient) EnsureGroup(context.Context, string, string) error { return nil }
func (c *deliveryStreamClient) Read(_ context.Context, _ string, _ string, _ string, pending bool, _ time.Duration) ([]extraction.StreamMessage, error) {
	if pending {
		return append([]extraction.StreamMessage(nil), c.pending...), nil
	}
	if len(c.messages) == 0 {
		return nil, nil
	}
	messages := c.messages
	c.messages = nil
	c.pending = append(c.pending, messages...)
	return messages, nil
}
func (c *deliveryStreamClient) Ack(_ context.Context, _, _, id string) error {
	c.acks = append(c.acks, id)
	filtered := c.pending[:0]
	for _, message := range c.pending {
		if message.ID != id {
			filtered = append(filtered, message)
		}
	}
	c.pending = filtered
	return nil
}
func (c *deliveryStreamClient) ClaimPending(context.Context, string, string, string) ([]extraction.StreamMessage, error) {
	return nil, nil
}
