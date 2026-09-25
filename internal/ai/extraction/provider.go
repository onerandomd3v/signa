package extraction

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Provider turns raw report text into JSON that must be validated by Validator.
type Provider interface {
	Extract(context.Context, string) ([]byte, error)
}

// UsageProvider is implemented by providers that can report provider-side
// token usage without exposing the source report or any caller identity.
type UsageProvider interface {
	ExtractWithUsage(context.Context, string) ([]byte, Usage, error)
}

// NamedProvider supplies a stable provider name for metrics. Names must never
// contain report content, credentials, or request-specific identifiers.
type NamedProvider interface {
	ProviderName() string
}

type Usage struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int
	Attempts     int
}

type FailureKind string

const (
	FailureTransient FailureKind = "transient"
	FailurePermanent FailureKind = "permanent"
)

// ProviderError classifies failures so only retryable provider failures are
// retried. Its message is deliberately provider-safe and excludes response
// bodies and report content.
type ProviderError struct {
	Kind       FailureKind
	Operation  string
	StatusCode int
	Err        error
}

func (e *ProviderError) Error() string {
	if e == nil {
		return "provider error"
	}
	if e.StatusCode != 0 {
		return fmt.Sprintf("%s provider failure (HTTP %d): %v", e.Kind, e.StatusCode, e.Err)
	}
	if e.Operation != "" {
		return fmt.Sprintf("%s provider failure during %s: %v", e.Kind, e.Operation, e.Err)
	}
	return fmt.Sprintf("%s provider failure: %v", e.Kind, e.Err)
}

func (e *ProviderError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func transientProviderError(operation string, err error) error {
	return &ProviderError{Kind: FailureTransient, Operation: operation, Err: err}
}

func permanentProviderError(operation string, err error) error {
	return &ProviderError{Kind: FailurePermanent, Operation: operation, Err: err}
}

func httpProviderError(statusCode int) error {
	kind := FailurePermanent
	if statusCode == 408 || statusCode == 409 || statusCode == 425 || statusCode == 429 || statusCode >= 500 {
		kind = FailureTransient
	}
	return &ProviderError{Kind: kind, StatusCode: statusCode, Operation: "HTTP request", Err: fmt.Errorf("status %d", statusCode)}
}

func failureKind(err error) FailureKind {
	var providerErr *ProviderError
	if errors.As(err, &providerErr) && providerErr.Kind != "" {
		return providerErr.Kind
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return FailureTransient
	}
	return FailurePermanent
}

func isTransientFailure(err error) bool {
	return failureKind(err) == FailureTransient || errors.Is(err, context.DeadlineExceeded)
}

type RetryConfig struct {
	Timeout     time.Duration
	MaxAttempts int
	Backoff     time.Duration
}

func (c RetryConfig) Validate() error {
	if c.Timeout <= 0 {
		return fmt.Errorf("provider timeout must be greater than zero")
	}
	if c.MaxAttempts < 1 || c.MaxAttempts > 5 {
		return fmt.Errorf("provider max attempts must be between 1 and 5")
	}
	if c.Backoff < 0 {
		return fmt.Errorf("provider retry backoff cannot be negative")
	}
	return nil
}

// RetryingProvider bounds both per-attempt execution time and total attempts.
// The parent context always wins, so shutdown and cancellation never cause a
// retry loop to outlive the worker request.
type RetryingProvider struct {
	provider Provider
	config   RetryConfig
}

func NewRetryingProvider(provider Provider, config RetryConfig) (*RetryingProvider, error) {
	if provider == nil {
		return nil, fmt.Errorf("provider is required")
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &RetryingProvider{provider: provider, config: config}, nil
}

func (p *RetryingProvider) ProviderName() string {
	if p == nil || p.provider == nil {
		return "provider"
	}
	if named, ok := p.provider.(NamedProvider); ok {
		return named.ProviderName()
	}
	return "provider"
}

func (p *RetryingProvider) Extract(ctx context.Context, rawText string) ([]byte, error) {
	output, _, err := p.ExtractWithUsage(ctx, rawText)
	return output, err
}

func (p *RetryingProvider) ExtractWithUsage(ctx context.Context, rawText string) ([]byte, Usage, error) {
	if p == nil || p.provider == nil {
		return nil, Usage{}, fmt.Errorf("retrying provider is not initialized")
	}
	var lastUsage Usage
	for attempt := 1; attempt <= p.config.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, lastUsage, err
		}
		attemptContext, cancel := context.WithTimeout(ctx, p.config.Timeout)
		var output []byte
		var usage Usage
		var err error
		if usageProvider, ok := p.provider.(UsageProvider); ok {
			output, usage, err = usageProvider.ExtractWithUsage(attemptContext, rawText)
		} else {
			output, err = p.provider.Extract(attemptContext, rawText)
		}
		cancel()
		usage.Attempts = attempt
		lastUsage = usage
		if err == nil {
			return output, usage, nil
		}
		if ctx.Err() != nil {
			return nil, usage, ctx.Err()
		}
		if !isTransientFailure(err) || attempt == p.config.MaxAttempts {
			return nil, usage, err
		}
		if err := waitForRetry(ctx, p.config.Backoff); err != nil {
			return nil, usage, err
		}
	}
	return nil, lastUsage, fmt.Errorf("provider retry loop exhausted")
}

func waitForRetry(ctx context.Context, backoff time.Duration) error {
	if backoff <= 0 {
		return nil
	}
	timer := time.NewTimer(backoff)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func safeNonNegative(value int) int {
	if value < 0 {
		return 0
	}
	return value
}
