package worker

import (
	"context"
	"testing"
	"time"
)

func TestRunStopsWhenContextIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, nil, time.Millisecond)
	}()

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not stop after context cancellation")
	}
}

func TestRunRejectsInvalidInterval(t *testing.T) {
	if err := Run(context.Background(), nil, 0); err == nil {
		t.Fatal("Run() error = nil, want invalid interval error")
	}
}

type testPublisher struct {
	calls  chan struct{}
	errors int
}

func (p *testPublisher) PublishCycle(context.Context) error {
	p.calls <- struct{}{}
	if p.errors > 0 {
		p.errors--
		return context.DeadlineExceeded
	}
	return nil
}

func TestRunPublishesImmediatelyAndStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	publisher := &testPublisher{calls: make(chan struct{}, 1)}
	done := make(chan error, 1)
	go func() { done <- Run(ctx, nil, time.Hour, publisher) }()
	select {
	case <-publisher.calls:
	case <-time.After(time.Second):
		t.Fatal("worker did not publish immediately")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not stop")
	}
}

func TestRunContinuesAfterPublisherError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	publisher := &testPublisher{calls: make(chan struct{}, 3), errors: 1}
	done := make(chan error, 1)
	go func() { done <- Run(ctx, nil, time.Millisecond, publisher) }()
	for range 2 {
		select {
		case <-publisher.calls:
		case <-time.After(time.Second):
			t.Fatal("worker did not continue polling")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run() did not stop")
	}
}
