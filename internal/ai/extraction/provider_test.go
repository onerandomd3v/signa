package extraction

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRetryingProviderRetriesTemporaryOutageAndPreservesAttempts(t *testing.T) {
	provider := &retrySequenceProvider{errors: []error{
		transientProviderError("request", errors.New("temporary outage")),
		transientProviderError("request", errors.New("temporary outage")),
		nil,
	}}
	retrying, err := NewRetryingProvider(provider, RetryConfig{Timeout: time.Second, MaxAttempts: 3})
	if err != nil {
		t.Fatal(err)
	}
	output, usage, err := retrying.ExtractWithUsage(context.Background(), "report")
	if err != nil {
		t.Fatalf("ExtractWithUsage() error = %v", err)
	}
	if string(output) != `{"status":"unknown"}` || provider.calls != 3 || usage.Attempts != 3 {
		t.Fatalf("output=%s calls=%d usage=%+v", output, provider.calls, usage)
	}
}

func TestRetryingProviderDoesNotRetryPermanentFailure(t *testing.T) {
	provider := &retrySequenceProvider{errors: []error{permanentProviderError("decode response", errors.New("malformed envelope"))}}
	retrying, err := NewRetryingProvider(provider, RetryConfig{Timeout: time.Second, MaxAttempts: 5})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := retrying.ExtractWithUsage(context.Background(), "report"); err == nil {
		t.Fatal("ExtractWithUsage() error = nil")
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.calls)
	}
}

func TestRetryingProviderTimesOutEachAttemptWithinBound(t *testing.T) {
	provider := &retrySequenceProvider{waitForContext: true, errors: []error{nil, nil}}
	retrying, err := NewRetryingProvider(provider, RetryConfig{Timeout: 5 * time.Millisecond, MaxAttempts: 2})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, usage, err := retrying.ExtractWithUsage(context.Background(), "report")
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ExtractWithUsage() error = %v, want deadline exceeded", err)
	}
	if provider.calls != 2 || usage.Attempts != 2 || time.Since(started) > time.Second {
		t.Fatalf("calls=%d usage=%+v elapsed=%s", provider.calls, usage, time.Since(started))
	}
}

func TestRetryingProviderStopsOnParentCancellation(t *testing.T) {
	provider := &retrySequenceProvider{waitForContext: true, errors: []error{nil, nil}}
	retrying, err := NewRetryingProvider(provider, RetryConfig{Timeout: time.Second, MaxAttempts: 3})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(5 * time.Millisecond)
		cancel()
	}()
	if _, _, err := retrying.ExtractWithUsage(ctx, "report"); !errors.Is(err, context.Canceled) {
		t.Fatalf("ExtractWithUsage() error = %v, want cancellation", err)
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.calls)
	}
}

func TestRetryingProviderRejectsInvalidConfiguration(t *testing.T) {
	for _, config := range []RetryConfig{
		{Timeout: 0, MaxAttempts: 1},
		{Timeout: time.Second, MaxAttempts: 0},
		{Timeout: time.Second, MaxAttempts: 6},
		{Timeout: time.Second, MaxAttempts: 1, Backoff: -time.Millisecond},
	} {
		if _, err := NewRetryingProvider(&retrySequenceProvider{}, config); err == nil {
			t.Fatalf("NewRetryingProvider(%+v) error = nil", config)
		}
	}
}

type retrySequenceProvider struct {
	errors         []error
	waitForContext bool
	calls          int
}

func (p *retrySequenceProvider) Extract(ctx context.Context, _ string) ([]byte, error) {
	p.calls++
	index := p.calls - 1
	if p.waitForContext {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if index < len(p.errors) && p.errors[index] != nil {
		return nil, p.errors[index]
	}
	return []byte(`{"status":"unknown"}`), nil
}
