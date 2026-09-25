package delivery

import (
	"context"
	"log/slog"
	"time"

	"github.com/onerandomd3v/signa/internal/ai/extraction"
)

type ProcessResult struct {
	Ack        bool
	RetryAfter time.Duration
}

type Processor struct {
	store   Store
	adapter Adapter
	backoff time.Duration
	logger  *slog.Logger
}

func NewProcessor(store Store, adapter Adapter, backoff time.Duration) *Processor {
	return &Processor{store: store, adapter: adapter, backoff: backoff}
}

func (p *Processor) WithLogger(logger *slog.Logger) *Processor { p.logger = logger; return p }

func (p *Processor) Process(ctx context.Context, message extraction.StreamMessage) (ProcessResult, error) {
	if p == nil || p.store == nil || p.adapter == nil {
		return ProcessResult{}, &configurationError{}
	}
	request, err := ParseRequest(message.Values)
	if err != nil {
		if p.logger != nil {
			p.logger.Error("malformed delivery request; acknowledging poison message", "stream_message_id", message.ID, "error", err)
		}
		return ProcessResult{Ack: true}, nil
	}
	started, err := p.store.StartAttempt(ctx, request, time.Now().UTC())
	if err != nil {
		return ProcessResult{}, err
	}
	if started.Terminal {
		return ProcessResult{Ack: true}, nil
	}
	if started.NotDue {
		return ProcessResult{RetryAfter: started.RetryAfter}, nil
	}
	providerResult, deliveryErr := p.adapter.Deliver(ctx, started.Attempt)
	kind := FailureKind("")
	if deliveryErr != nil {
		kind = failureKind(deliveryErr)
	}
	finished, err := p.store.FinishAttempt(ctx, started.Attempt, providerResult, kind, deliveryErr, time.Now().UTC())
	if err != nil {
		return ProcessResult{}, err
	}
	if finished.Terminal {
		return ProcessResult{Ack: true}, nil
	}
	return ProcessResult{RetryAfter: finished.RetryAfter}, nil
}

type configurationError struct{}

func (*configurationError) Error() string { return "delivery processor dependencies are required" }
