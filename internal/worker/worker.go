package worker

import (
	"context"
	"log/slog"
	"time"
)

// Run starts the worker process and stops when its context is canceled.
// The ticker is intentionally only a lifecycle heartbeat until worker responsibilities are added.
func Run(ctx context.Context, logger *slog.Logger, interval time.Duration) error {
	if interval <= 0 {
		return &invalidIntervalError{interval: interval}
	}

	if logger != nil {
		logger.Info("worker started", "interval", interval.String())
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			if logger != nil {
				logger.Info("worker stopped", "reason", ctx.Err())
			}
			return nil
		case <-ticker.C:
			if logger != nil {
				logger.Debug("worker heartbeat")
			}
		}
	}
}

type invalidIntervalError struct {
	interval time.Duration
}

func (e *invalidIntervalError) Error() string {
	return "worker interval must be greater than zero"
}
