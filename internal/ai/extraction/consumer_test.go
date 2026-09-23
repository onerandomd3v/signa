package extraction

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestConsumerProcessesPendingMessageAfterGroupSetup(t *testing.T) {
	message := StreamMessage{ID: "1740000000000-0", Values: reportCreatedFields()}
	client := &fakeStreamClient{pending: []StreamMessage{message}}
	processor := &fakeMessageProcessor{}
	consumer := NewConsumer(client, processor, "signa:report-events", "extractors", "worker-1", time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	processor.afterProcess = cancel

	if err := consumer.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !client.ensureGroupCalled {
		t.Fatal("EnsureGroup() was not called")
	}
	if len(processor.messages) != 1 || processor.messages[0].ID != message.ID {
		t.Fatalf("processed messages = %+v, want %+v", processor.messages, []StreamMessage{message})
	}
}

func TestConsumerLeavesFailedMessagePendingForRetry(t *testing.T) {
	message := StreamMessage{ID: "1740000000000-0", Values: reportCreatedFields()}
	client := &fakeStreamClient{pending: []StreamMessage{message}}
	processor := &fakeMessageProcessor{err: errors.New("provider unavailable")}
	consumer := NewConsumer(client, processor, "signa:report-events", "extractors", "worker-1", time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	processor.afterFailure = cancel

	if err := consumer.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(processor.messages) != 1 {
		t.Fatalf("processed messages = %d, want 1", len(processor.messages))
	}
	if len(client.acks) != 0 {
		t.Fatalf("acks = %v, want none", client.acks)
	}
}

func reportCreatedFields() map[string]any {
	return map[string]any{
		"event_id":       "event-1",
		"event_name":     ReportCreatedV1,
		"aggregate_type": "report",
		"payload":        `{"report_id":"11111111-1111-4111-8111-111111111111"}`,
	}
}

type fakeStreamClient struct {
	ensureGroupCalled bool
	pending           []StreamMessage
	acks              []string
}

func (f *fakeStreamClient) EnsureGroup(context.Context, string, string) error {
	f.ensureGroupCalled = true
	return nil
}

func (f *fakeStreamClient) Read(ctx context.Context, _ string, _ string, _ string, pending bool, _ time.Duration) ([]StreamMessage, error) {
	if pending && len(f.pending) > 0 {
		messages := f.pending
		f.pending = nil
		return messages, nil
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (f *fakeStreamClient) Ack(_ context.Context, _ string, _ string, id string) error {
	f.acks = append(f.acks, id)
	return nil
}

type fakeMessageProcessor struct {
	messages     []StreamMessage
	err          error
	afterProcess context.CancelFunc
	afterFailure context.CancelFunc
}

func (f *fakeMessageProcessor) Process(_ context.Context, message StreamMessage) error {
	f.messages = append(f.messages, message)
	if f.err == nil && f.afterProcess != nil {
		f.afterProcess()
	}
	if f.err != nil && f.afterFailure != nil {
		f.afterFailure()
	}
	return f.err
}
