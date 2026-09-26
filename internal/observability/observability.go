// Package observability provides privacy-safe OpenTelemetry instrumentation
// for Signa's report-to-alert processing stages.
package observability

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"
)

type Stage string

const (
	StageReportPersist      Stage = "report.persist"
	StageOutboxPublish      Stage = "outbox.publish"
	StageAIProcessing       Stage = "ai.process"
	StageIncidentProcessing Stage = "incident.process"
	StagePriorityEvaluation Stage = "priority.evaluate"
	StageAlertCreation      Stage = "alert.create"
	StageDeliveryAttempt    Stage = "delivery.attempt"
)

const (
	instrumentationName = "github.com/onerandomd3v/signa/internal/observability"
	durationMetricName  = "signa.processing.stage.duration"
)

var stageDuration = newDurationHistogram()

func newDurationHistogram() metric.Float64Histogram {
	histogram, err := otel.Meter(instrumentationName).Float64Histogram(
		durationMetricName,
		metric.WithUnit("s"),
		metric.WithDescription("Duration of a Signa processing stage"),
	)
	if err != nil {
		histogram, _ = noop.NewMeterProvider().Meter(instrumentationName).Float64Histogram(durationMetricName)
	}
	return histogram
}

// StartStage starts a low-cardinality span and returns a finish function.
// Only the stage name and generic outcome are recorded; error text and domain
// identifiers are intentionally excluded. With no SDK/exporter configured,
// the OpenTelemetry API's default no-op providers keep this boundary inert.
func StartStage(ctx context.Context, stage Stage) (context.Context, func(error)) {
	started := time.Now()
	ctx, span := otel.Tracer(instrumentationName).Start(ctx, string(stage), trace.WithSpanKind(trace.SpanKindInternal))
	var once sync.Once
	return ctx, func(err error) {
		once.Do(func() {
			status := "ok"
			if err != nil {
				status = "error"
				span.SetStatus(codes.Error, "stage failed")
			} else {
				span.SetStatus(codes.Ok, "")
			}
			stageDuration.Record(ctx, time.Since(started).Seconds(), metric.WithAttributes(
				attribute.String("stage", string(stage)),
				attribute.String("status", status),
			))
			span.End()
		})
	}
}
