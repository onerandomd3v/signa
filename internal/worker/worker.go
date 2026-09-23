package worker

import (
	"context"
	"log/slog"
	"time"
)

type Publisher interface{ PublishCycle(context.Context) error }

// Run publishes immediately and then polls until context cancellation.
func Run(ctx context.Context, logger *slog.Logger, interval time.Duration, publishers ...Publisher) error {
	if interval <= 0 {
		return &invalidIntervalError{interval: interval}
	}
	var publisher Publisher
	if len(publishers) > 0 {
		publisher = publishers[0]
	}
	if logger != nil {
		logger.Info("worker started", "interval", interval.String())
	}
	defer func() {
		if logger != nil {
			logger.Info("worker stopped", "reason", ctx.Err())
		}
	}()
	publish := func() {
		if publisher == nil {
			return
		}
		if err := publisher.PublishCycle(ctx); err != nil && ctx.Err() == nil && logger != nil {
			logger.Error("outbox publish cycle failed", "error", err)
		}
	}
	publish()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			publish()
		}
	}
}

type invalidIntervalError struct{ interval time.Duration }

func (e *invalidIntervalError) Error() string { return "worker interval must be greater than zero" }
