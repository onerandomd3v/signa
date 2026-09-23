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
