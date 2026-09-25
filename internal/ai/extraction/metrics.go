package extraction

import (
	"log/slog"
	"time"
)

type AIMetric struct {
	Provider     string
	Outcome      string
	FailureKind  FailureKind
	Duration     time.Duration
	Attempts     int
	InputTokens  int
	OutputTokens int
	TotalTokens  int
}

type Metrics interface {
	Observe(AIMetric)
}

type MetricsFunc func(AIMetric)

func (f MetricsFunc) Observe(metric AIMetric) {
	if f != nil {
		f(metric)
	}
}

type SlogMetrics struct {
	Logger *slog.Logger
}

func NewSlogMetrics(logger *slog.Logger) SlogMetrics {
	return SlogMetrics{Logger: logger}
}

func (m SlogMetrics) Observe(metric AIMetric) {
	if m.Logger == nil {
		return
	}
	m.Logger.Info("ai extraction metric",
		"provider", metric.Provider,
		"outcome", metric.Outcome,
		"failure_kind", metric.FailureKind,
		"duration_ms", metric.Duration.Milliseconds(),
		"attempts", metric.Attempts,
		"input_tokens", metric.InputTokens,
		"output_tokens", metric.OutputTokens,
		"total_tokens", metric.TotalTokens,
	)
}
