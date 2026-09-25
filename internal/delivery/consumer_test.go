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

func TestConsumerProviderUnavailableLeavesValidRequestPending(t *testing.T) {
	client := &deliveryStreamClient{messages: []extraction.StreamMessage{deliveryMessage("delivery-unavailable", "alert-1", "key-unavailable")}}
	store := newMemoryStore(2)
	consumer := NewConsumer(client, NewProcessor(store, nil, time.Hour), "stream", "group", "consumer", time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(10 * time.Millisecond); cancel() }()
	if err := consumer.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(client.acks) != 0 || store.attempts != 0 {
		t.Fatalf("unavailable provider consumed work: acks=%d attempts=%d", len(client.acks), store.attempts)
	}
}

func TestConsumerCancellationDuringRetryReturnsCleanly(t *testing.T) {
	client := &deliveryStreamClient{messages: []extraction.StreamMessage{deliveryMessage("delivery-cancel", "alert-1", "key-cancel")}}
	store := newMemoryStore(2)
	adapter := &recordingAdapter{errors: []error{Transient(errors.New("temporary"))}}
	consumer := NewConsumer(client, NewProcessor(store, adapter, time.Hour), "stream", "group", "consumer", time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(10 * time.Millisecond); cancel() }()
	if err := consumer.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v, want clean shutdown", err)
	}
}

func TestConsumerDefersNotDueDeliveryWithoutStallingReadyWork(t *testing.T) {
	client := &deliveryStreamClient{messages: []extraction.StreamMessage{
		deliveryMessage("delivery-not-due", "alert-1", "key-not-due"),
		deliveryMessage("delivery-ready", "alert-2", "key-ready"),
	}}
	store := &notDueStore{ready: newMemoryStore(2), notDueID: "delivery-not-due"}
	adapter := &recordingAdapter{}
	consumer := NewConsumer(client, NewProcessor(store, adapter, 0), "stream", "group", "consumer", time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(10 * time.Millisecond); cancel() }()
	if err := consumer.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(client.acks) != 1 || client.acks[0] != "stream-delivery-ready" || adapter.calls != 1 {
		t.Fatalf("ready delivery was stalled: acks=%v calls=%d", client.acks, adapter.calls)
	}
}

type notDueStore struct {
	ready    *memoryStore
	notDueID string
}

func (s *notDueStore) StartAttempt(ctx context.Context, request Request, now time.Time) (StartResult, error) {
	if request.DeliveryID == s.notDueID {
		return StartResult{NotDue: true, RetryAfter: time.Hour}, nil
	}
	return s.ready.StartAttempt(ctx, request, now)
}

func (s *notDueStore) FinishAttempt(ctx context.Context, attempt Attempt, provider ProviderResult, kind FailureKind, err error, now time.Time) (FinishResult, error) {
	return s.ready.FinishAttempt(ctx, attempt, provider, kind, err, now)
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
