package extraction

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onerandomd3v/signa/internal/observability"
)

// ErrDurableExtractionDestinationUnresolved is retained for callers compiled
// against COD-191; production wiring now uses DurableStore instead.
var ErrDurableExtractionDestinationUnresolved = errors.New("durable extraction result destination requires owner direction")

// ErrInvalidStructuredOutput marks provider output that failed the canonical
// extraction contract. It remains pending without being sent through an
// endless provider retry loop.
var ErrInvalidStructuredOutput = errors.New("invalid structured extraction output")

// LoggingObserver is a test-only compatibility boundary and must not be wired
// into the worker. DurableStore is the production observer.
type LoggingObserver struct{ Logger *slog.Logger }

func (o LoggingObserver) Observe(_ context.Context, reportID string, result Extraction) error {
	if o.Logger != nil {
		o.Logger.Info("report text extraction completed", "report_id", reportID, "contract_version", result.ContractVersion, "taxonomy_version", result.TaxonomyVersion)
	}
	return fmt.Errorf("%w; validated result was not acknowledged", ErrDurableExtractionDestinationUnresolved)
}

type ReportReader interface {
	LoadRawText(context.Context, string) (string, error)
}

type ExtractionValidator interface {
	Validate([]byte) (Extraction, error)
}

type Observer interface {
	Observe(context.Context, string, Extraction) error
}

type ExistingExtractionFinder interface {
	Find(context.Context, string, string) (Extraction, bool, error)
}

type Acknowledger interface {
	Ack(context.Context, string, string, string) error
}

type Processor struct {
	reader    ReportReader
	provider  Provider
	validator ExtractionValidator
	observer  Observer
	acker     Acknowledger
	stream    string
	group     string
	metrics   Metrics
}

func NewProcessor(reader ReportReader, provider Provider, validator ExtractionValidator, observer Observer, acker Acknowledger, stream, group string) *Processor {
	return &Processor{reader: reader, provider: provider, validator: validator, observer: observer, acker: acker, stream: stream, group: group}
}

func (p *Processor) WithMetrics(metrics Metrics) *Processor {
	p.metrics = metrics
	return p
}

func (p *Processor) Process(ctx context.Context, message StreamMessage) (err error) {
	ctx, finish := observability.StartStage(ctx, observability.StageAIProcessing)
	defer func() { finish(err) }()
	if p == nil || p.reader == nil || p.provider == nil || p.validator == nil || p.observer == nil || p.acker == nil {
		return fmt.Errorf("extraction processor dependencies are required")
	}
	event, err := ParseReportEvent(message.Values)
	if err != nil {
		return err
	}
	if message.ID == "" {
		return fmt.Errorf("stream message id is required")
	}
	if event.Name != ReportCreatedV1 {
		if event.Name == ReportAIProcessedV1 || event.Name == "report.media_attached.v1" {
			if err := p.acker.Ack(ctx, p.stream, p.group, message.ID); err != nil {
				return fmt.Errorf("ack unrelated report event %s: %w", message.ID, err)
			}
			return nil
		}
		return fmt.Errorf("unsupported report event %q", event.Name)
	}
	contractVersion := "signa.ai.report-extraction.v0"
	if finder, ok := p.observer.(ExistingExtractionFinder); ok {
		if _, found, err := finder.Find(ctx, event.ReportID, contractVersion); err != nil {
			return fmt.Errorf("check existing extraction for report %s: %w", event.ReportID, err)
		} else if found {
			if err := p.acker.Ack(ctx, p.stream, p.group, message.ID); err != nil {
				return fmt.Errorf("ack redelivered extraction message %s: %w", message.ID, err)
			}
			return nil
		}
	}
	rawText, err := p.reader.LoadRawText(ctx, event.ReportID)
	if err != nil {
		return fmt.Errorf("load report %s: %w", event.ReportID, err)
	}
	started := time.Now().UTC()
	p.recordAIProcessingStart(ctx, event.ReportID, started)
	var output []byte
	var usage Usage
	if usageProvider, ok := p.provider.(UsageProvider); ok {
		output, usage, err = usageProvider.ExtractWithUsage(ctx, rawText)
	} else {
		output, err = p.provider.Extract(ctx, rawText)
		usage.Attempts = 1
	}
	providerDuration := time.Since(started)
	if err != nil {
		err = normalizeProviderError(err)
		p.recordAIProcessingFailure(ctx, event.ReportID, failureKind(err), time.Now().UTC())
		p.observeMetric(metricFor(p.providerName(), "provider_failure", failureKind(err), providerDuration, usage))
		return fmt.Errorf("extract report %s: %w", event.ReportID, err)
	}
	validated, err := p.validator.Validate(output)
	if err != nil {
		p.recordAIProcessingFailure(ctx, event.ReportID, FailurePermanent, time.Now().UTC())
		p.observeMetric(metricFor(p.providerName(), "malformed_output", FailurePermanent, providerDuration, usage))
		return fmt.Errorf("%w: validate extraction for report %s: %v", ErrInvalidStructuredOutput, event.ReportID, err)
	}
	if err := p.observer.Observe(ctx, event.ReportID, validated); err != nil {
		p.recordAIProcessingFailure(ctx, event.ReportID, FailureTransient, time.Now().UTC())
		p.observeMetric(metricFor(p.providerName(), "durable_persistence_failure", FailureTransient, providerDuration, usage))
		return fmt.Errorf("observe extraction for report %s: %w", event.ReportID, err)
	}
	p.recordAIProcessingSuccess(ctx, event.ReportID, time.Now().UTC())
	p.observeMetric(metricFor(p.providerName(), "success", "", providerDuration, usage))
	if err := p.acker.Ack(ctx, p.stream, p.group, message.ID); err != nil {
		return fmt.Errorf("ack extraction message %s: %w", message.ID, err)
	}
	return nil
}

func (p *Processor) processingRecorder() ProcessingStateRecorder {
	if p == nil {
		return nil
	}
	recorder, _ := p.observer.(ProcessingStateRecorder)
	return recorder
}

func (p *Processor) recordAIProcessingStart(ctx context.Context, reportID string, now time.Time) {
	if recorder := p.processingRecorder(); recorder != nil {
		_ = recorder.MarkAIProcessingStarted(ctx, reportID, now)
	}
}

func (p *Processor) recordAIProcessingSuccess(ctx context.Context, reportID string, now time.Time) {
	if recorder := p.processingRecorder(); recorder != nil {
		_ = recorder.MarkAIProcessingSucceeded(ctx, reportID, now)
	}
}

func (p *Processor) recordAIProcessingFailure(ctx context.Context, reportID string, kind FailureKind, now time.Time) {
	if recorder := p.processingRecorder(); recorder != nil {
		state := "FAILED_TERMINAL"
		if kind == FailureTransient {
			state = "FAILED_RETRYABLE"
		}
		_ = recorder.MarkAIProcessingFailed(ctx, reportID, state, kind, now)
	}
}

func (p *Processor) providerName() string {
	if named, ok := p.provider.(NamedProvider); ok && named.ProviderName() != "" {
		return named.ProviderName()
	}
	return "provider"
}

func (p *Processor) observeMetric(metric AIMetric) {
	if p.metrics != nil {
		p.metrics.Observe(metric)
	}
}

func metricFor(provider, outcome string, kind FailureKind, duration time.Duration, usage Usage) AIMetric {
	return AIMetric{
		Provider:              provider,
		Outcome:               outcome,
		FailureKind:           kind,
		Duration:              duration,
		Attempts:              usage.Attempts,
		InputTokens:           usage.InputTokens,
		OutputTokens:          usage.OutputTokens,
		TotalTokens:           usage.TotalTokens,
		InputTokensAvailable:  usage.InputTokensAvailable,
		OutputTokensAvailable: usage.OutputTokensAvailable,
		TotalTokensAvailable:  usage.TotalTokensAvailable,
	}
}

type PostgresReportReader struct {
	database interface {
		QueryRow(context.Context, string, ...any) pgx.Row
	}
}

func NewPostgresReportReader(database interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}) *PostgresReportReader {
	return &PostgresReportReader{database: database}
}

func (r *PostgresReportReader) LoadRawText(ctx context.Context, reportID string) (string, error) {
	if r == nil || r.database == nil {
		return "", fmt.Errorf("report reader database is required")
	}
	var rawText string
	if err := r.database.QueryRow(ctx, `SELECT raw_text FROM reports WHERE id = $1`, reportID).Scan(&rawText); err != nil {
		return "", err
	}
	return rawText, nil
}
