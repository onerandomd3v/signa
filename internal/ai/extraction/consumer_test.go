package extraction

import (
	"context"
	"errors"
	"sync"
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

func TestConsumerDoesNotRetryUnresolvedDestination(t *testing.T) {
	message := StreamMessage{ID: "1740000000000-0", Values: reportCreatedFields()}
	client := &blockedPendingStreamClient{message: message, newRead: make(chan struct{})}
	provider := &fakeProvider{result: []byte(validExtractionJSON())}
	acker := &fakeAcker{}
	processor := NewProcessor(
		&fakeReportReader{rawText: "raw report"},
		provider,
		&fakeValidator{result: Extraction{ContractVersion: "signa.ai.report-extraction.v0"}},
		LoggingObserver{},
		acker,
		"signa:report-events",
		"extractors",
	)
	consumer := NewConsumer(client, processor, "signa:report-events", "extractors", "worker-1", time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- consumer.Run(ctx) }()
	select {
	case <-client.newRead:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("consumer did not continue to its new-message read after blocking the pending message")
	}
	select {
	case err := <-runErr:
		t.Fatalf("consumer stopped after unresolved destination: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	cancel()
	if err := <-runErr; err != nil {
		t.Fatalf("Run() error after cancellation = %v", err)
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want exactly one before the message becomes blocked", provider.calls)
	}
	if acker.calls != 0 {
		t.Fatalf("acks = %d, want none for blocked message", acker.calls)
	}
}

func TestConsumerDefersPermanentProviderFailureAcrossPendingCycles(t *testing.T) {
	message := StreamMessage{ID: "1740000000000-0", Values: reportCreatedFields()}
	client := &repeatingPendingStreamClient{message: message}
	provider := &fakeProvider{err: &ProviderError{Kind: FailurePermanent, Operation: "test", Err: errors.New("invalid API request")}}
	processor := NewProcessor(
		&fakeReportReader{rawText: "raw report"},
		provider,
		&fakeValidator{result: Extraction{ContractVersion: "signa.ai.report-extraction.v0"}},
		&fakeObserver{},
		&fakeAcker{},
		"signa:report-events",
		"extractors",
	)
	consumer := NewConsumer(client, processor, "signa:report-events", "extractors", "worker-1", time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if err := consumer.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if client.pendingReads < 2 {
		t.Fatalf("pending reads = %d, want repeated consumer cycles", client.pendingReads)
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want one terminal attempt", provider.calls)
	}
}

func TestConsumerDefersMalformedOutputAcrossPendingCycles(t *testing.T) {
	message := StreamMessage{ID: "1740000000000-0", Values: reportCreatedFields()}
	client := &repeatingPendingStreamClient{message: message}
	provider := &fakeProvider{result: []byte(`{"malformed":true}`)}
	processor := NewProcessor(
		&fakeReportReader{rawText: "raw report"},
		provider,
		&fakeValidator{err: errors.New("schema validation failed")},
		&fakeObserver{},
		&fakeAcker{},
		"signa:report-events",
		"extractors",
	)
	consumer := NewConsumer(client, processor, "signa:report-events", "extractors", "worker-1", time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if err := consumer.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if client.pendingReads < 2 {
		t.Fatalf("pending reads = %d, want repeated consumer cycles", client.pendingReads)
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want one malformed-output attempt", provider.calls)
	}
}

func TestConsumerRecoversFromTransientProviderFailureOnLaterCycle(t *testing.T) {
	message := StreamMessage{ID: "1740000000000-0", Values: reportCreatedFields()}
	client := &recoveringPendingStreamClient{message: message}
	provider := &recoveringProvider{}
	processor := NewProcessor(
		&fakeReportReader{rawText: "raw report"},
		provider,
		&fakeValidator{result: Extraction{ContractVersion: "signa.ai.report-extraction.v0"}},
		&fakeObserver{},
		client,
		"signa:report-events",
		"extractors",
	)
	consumer := NewConsumer(client, processor, "signa:report-events", "extractors", "worker-1", time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if err := consumer.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if provider.calls != 2 || client.acks != 1 {
		t.Fatalf("provider calls=%d acks=%d, want transient recovery on second cycle", provider.calls, client.acks)
	}
}

func TestReadPendingCombinesLocalAndStaleMessagesWithoutDuplicates(t *testing.T) {
	client := &combinedPendingStreamClient{
		local:   []StreamMessage{{ID: "local-1"}},
		claimed: []StreamMessage{{ID: "local-1"}, {ID: "stale-1"}},
	}
	messages, err := readPending(context.Background(), client, "stream", "group", "consumer")
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[0].ID != "local-1" || messages[1].ID != "stale-1" {
		t.Fatalf("messages = %+v", messages)
	}
}

func TestConsumerDoesNotLetLocalFailureStarveClaimedOrNewMessages(t *testing.T) {
	client := &combinedPendingStreamClient{
		local:   []StreamMessage{{ID: "local-1"}},
		claimed: []StreamMessage{{ID: "stale-1"}},
		new:     []StreamMessage{{ID: "new-1"}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	processor := &starvationProcessor{client: client, cancel: cancel}
	consumer := NewConsumer(client, processor, "stream", "group", "consumer", time.Millisecond)
	if err := consumer.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if processor.calls["stale-1"] != 1 || processor.calls["new-1"] != 1 {
		t.Fatalf("processed calls = %+v", processor.calls)
	}
	if client.acks["stale-1"] != 1 || client.acks["new-1"] != 1 {
		t.Fatalf("acks = %+v", client.acks)
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

type blockedPendingStreamClient struct {
	message     StreamMessage
	newRead     chan struct{}
	newReadOnce sync.Once
}

type repeatingPendingStreamClient struct {
	message      StreamMessage
	pendingReads int
}

type recoveringPendingStreamClient struct {
	message StreamMessage
	acked   bool
	acks    int
}

func (c *recoveringPendingStreamClient) EnsureGroup(context.Context, string, string) error {
	return nil
}

func (c *recoveringPendingStreamClient) Read(ctx context.Context, _ string, _ string, _ string, pending bool, _ time.Duration) ([]StreamMessage, error) {
	if pending && !c.acked {
		return []StreamMessage{c.message}, nil
	}
	timer := time.NewTimer(time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, nil
	}
}

func (c *recoveringPendingStreamClient) Ack(context.Context, string, string, string) error {
	c.acked = true
	c.acks++
	return nil
}

type recoveringProvider struct {
	calls int
}

func (p *recoveringProvider) Extract(_ context.Context, _ string) ([]byte, error) {
	p.calls++
	if p.calls == 1 {
		return nil, transientProviderError("test provider", errors.New("temporary outage"))
	}
	return []byte(validExtractionJSON()), nil
}

func (c *repeatingPendingStreamClient) EnsureGroup(context.Context, string, string) error {
	return nil
}

func (c *repeatingPendingStreamClient) Read(ctx context.Context, _ string, _ string, _ string, pending bool, _ time.Duration) ([]StreamMessage, error) {
	if pending {
		c.pendingReads++
		return []StreamMessage{c.message}, nil
	}
	timer := time.NewTimer(time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, nil
	}
}

func (c *repeatingPendingStreamClient) Ack(context.Context, string, string, string) error {
	return nil
}

func (f *blockedPendingStreamClient) EnsureGroup(context.Context, string, string) error {
	return nil
}

func (f *blockedPendingStreamClient) Read(ctx context.Context, _ string, _ string, _ string, pending bool, _ time.Duration) ([]StreamMessage, error) {
	if pending {
		return []StreamMessage{f.message}, nil
	}
	if f.newRead != nil {
		f.newReadOnce.Do(func() { close(f.newRead) })
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (f *blockedPendingStreamClient) Ack(context.Context, string, string, string) error {
	return nil
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

type combinedPendingStreamClient struct {
	local, claimed, new []StreamMessage
	claimCalls          int
	acks                map[string]int
}

func (c *combinedPendingStreamClient) EnsureGroup(context.Context, string, string) error { return nil }
func (c *combinedPendingStreamClient) Read(ctx context.Context, _ string, _ string, _ string, pending bool, _ time.Duration) ([]StreamMessage, error) {
	if pending {
		return c.local, nil
	}
	if len(c.new) == 0 {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	result := c.new
	c.new = nil
	return result, nil
}
func (c *combinedPendingStreamClient) Ack(_ context.Context, _, _, id string) error {
	if c.acks == nil {
		c.acks = map[string]int{}
	}
	c.acks[id]++
	return nil
}
func (c *combinedPendingStreamClient) ClaimPending(context.Context, string, string, string) ([]StreamMessage, error) {
	c.claimCalls++
	if c.claimCalls == 1 {
		return c.claimed, nil
	}
	return nil, nil
}

type starvationProcessor struct {
	client *combinedPendingStreamClient
	calls  map[string]int
	cancel context.CancelFunc
}

func (p *starvationProcessor) Process(ctx context.Context, message StreamMessage) error {
	if p.calls == nil {
		p.calls = map[string]int{}
	}
	p.calls[message.ID]++
	if message.ID == "local-1" {
		return errors.New("poison pending message")
	}
	if err := p.client.Ack(ctx, "stream", "group", message.ID); err != nil {
		return err
	}
	if message.ID == "new-1" {
		p.cancel()
	}
	return nil
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
