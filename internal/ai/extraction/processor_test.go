package extraction

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestProcessorLoadsExtractsValidatesObservesAndAcknowledges(t *testing.T) {
	const reportID = "11111111-1111-4111-8111-111111111111"
	reader := &fakeReportReader{rawText: "raw report"}
	provider := &fakeProvider{result: []byte(validExtractionJSON())}
	validator := &fakeValidator{result: Extraction{ContractVersion: "signa.ai.report-extraction.v0"}}
	observer := &fakeObserver{}
	acker := &fakeAcker{}
	processor := NewProcessor(reader, provider, validator, observer, acker, "signa:report-events", "group-1")

	err := processor.Process(context.Background(), StreamMessage{ID: "message-1", Values: map[string]any{
		"event_id": "event-1", "event_name": "report.created.v1", "aggregate_type": "report",
		"payload": `{"report_id":"` + reportID + `"}`,
	}})
	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if reader.reportID != reportID || provider.rawText != "raw report" || observer.reportID != reportID || observer.result.ContractVersion == "" {
		t.Fatalf("processing values = reader %q, provider %q, observer %+v", reader.reportID, provider.rawText, observer)
	}
	if acker.calls != 1 || acker.messageID != "message-1" {
		t.Fatalf("ack = %+v", acker)
	}
}

func TestProcessorDoesNotAcknowledgeFailures(t *testing.T) {
	for _, test := range []struct {
		name        string
		readerErr   error
		providerErr error
		validateErr error
		observeErr  error
	}{
		{name: "reader", readerErr: errors.New("database unavailable")},
		{name: "provider", providerErr: errors.New("provider unavailable")},
		{name: "validator", validateErr: errors.New("schema invalid")},
		{name: "observer", observeErr: errors.New("observer unavailable")},
	} {
		t.Run(test.name, func(t *testing.T) {
			processor := NewProcessor(
				&fakeReportReader{rawText: "raw report", err: test.readerErr},
				&fakeProvider{result: []byte(validExtractionJSON()), err: test.providerErr},
				&fakeValidator{result: Extraction{}, err: test.validateErr},
				&fakeObserver{err: test.observeErr},
				&fakeAcker{}, "signa:report-events", "group-1",
			)
			err := processor.Process(context.Background(), StreamMessage{ID: "message-1", Values: map[string]any{
				"event_id": "event-1", "event_name": "report.created.v1", "aggregate_type": "report",
				"payload": `{"report_id":"11111111-1111-4111-8111-111111111111"}`,
			}})
			if err == nil {
				t.Fatal("Process() error = nil")
			}
			if processor.acker.(*fakeAcker).calls != 0 {
				t.Fatal("Process() acknowledged failed message")
			}
		})
	}
}

func TestProcessorMalformedOutputCannotBeObservedOrAcknowledged(t *testing.T) {
	observer := &fakeObserver{}
	acker := &fakeAcker{}
	processor := NewProcessor(
		&fakeReportReader{rawText: "report"},
		&fakeProvider{result: []byte(`{"not":"the extraction contract"}`)},
		&fakeValidator{err: errors.New("schema validation failed")},
		observer,
		acker,
		"signa:report-events",
		"group-1",
	)
	err := processor.Process(context.Background(), StreamMessage{ID: "message-1", Values: map[string]any{
		"event_id": "event-1", "event_name": ReportCreatedV1, "aggregate_type": "report",
		"payload": `{"report_id":"11111111-1111-4111-8111-111111111111"}`,
	}})
	if err == nil {
		t.Fatal("Process() error = nil")
	}
	if observer.result.ContractVersion != "" || acker.calls != 0 {
		t.Fatalf("malformed output reached durable path: observer=%+v acks=%d", observer.result, acker.calls)
	}
}

func TestProcessorRecordsSafeAIUsageMetric(t *testing.T) {
	metrics := make([]AIMetric, 0, 1)
	processor := NewProcessor(
		&fakeReportReader{rawText: "private report text"},
		&usageTestProvider{result: []byte(validExtractionJSON()), usage: measuredUsage(3, 4, 7)},
		&fakeValidator{result: Extraction{ContractVersion: "signa.ai.report-extraction.v0"}},
		&fakeObserver{},
		&fakeAcker{},
		"signa:report-events",
		"group-1",
	).WithMetrics(MetricsFunc(func(metric AIMetric) { metrics = append(metrics, metric) }))
	if err := processor.Process(context.Background(), StreamMessage{ID: "message-1", Values: map[string]any{
		"event_id": "event-1", "event_name": ReportCreatedV1, "aggregate_type": "report",
		"payload": `{"report_id":"11111111-1111-4111-8111-111111111111"}`,
	}}); err != nil {
		t.Fatal(err)
	}
	if len(metrics) != 1 || metrics[0].Outcome != "success" || metrics[0].InputTokens != 3 || metrics[0].OutputTokens != 4 || metrics[0].TotalTokens != 7 || !metrics[0].InputTokensAvailable || !metrics[0].OutputTokensAvailable || !metrics[0].TotalTokensAvailable {
		t.Fatalf("metrics = %+v", metrics)
	}
}

func TestProcessorRecordsDurableAIStageStateWithoutChangingAcknowledgement(t *testing.T) {
	state := &recordingStateObserver{}
	processor := NewProcessor(
		&fakeReportReader{rawText: "raw report"},
		&fakeProvider{result: []byte(validExtractionJSON())},
		&fakeValidator{result: Extraction{ContractVersion: "signa.ai.report-extraction.v0"}},
		state,
		&fakeAcker{}, "signa:report-events", "group-1",
	)

	if err := processor.Process(context.Background(), StreamMessage{ID: "message-1", Values: map[string]any{
		"event_id": "event-1", "event_name": ReportCreatedV1, "aggregate_type": "report",
		"payload": `{"report_id":"11111111-1111-4111-8111-111111111111"}`,
	}}); err != nil {
		t.Fatal(err)
	}
	if got, want := state.states, []string{"started", "succeeded"}; !equalStrings(got, want) {
		t.Fatalf("state transitions = %v, want %v", got, want)
	}
	if state.failureKind != "" {
		t.Fatalf("successful AI state recorded failure kind %q", state.failureKind)
	}
}

func TestProcessorRecordsRetryableAndTerminalAIFailuresUsingSafeKinds(t *testing.T) {
	for _, test := range []struct {
		name      string
		err       error
		wantState string
		wantKind  string
	}{
		{name: "retryable provider", err: transientProviderError("extract", errors.New("temporary")), wantState: "FAILED_RETRYABLE", wantKind: string(FailureTransient)},
		{name: "terminal provider", err: permanentProviderError("extract", errors.New("rejected")), wantState: "FAILED_TERMINAL", wantKind: string(FailurePermanent)},
		{name: "terminal schema", err: errors.New("schema invalid"), wantState: "FAILED_TERMINAL", wantKind: string(FailurePermanent)},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := &recordingStateObserver{}
			processor := NewProcessor(
				&fakeReportReader{rawText: "raw report"},
				&fakeProvider{result: []byte(validExtractionJSON()), err: test.err},
				&fakeValidator{result: Extraction{ContractVersion: "signa.ai.report-extraction.v0"}},
				state,
				&fakeAcker{}, "signa:report-events", "group-1",
			)
			if err := processor.Process(context.Background(), StreamMessage{ID: "message-1", Values: map[string]any{
				"event_id": "event-1", "event_name": ReportCreatedV1, "aggregate_type": "report",
				"payload": `{"report_id":"11111111-1111-4111-8111-111111111111"}`,
			}}); err == nil {
				t.Fatal("Process() error = nil")
			}
			if state.failureState != test.wantState || state.failureKind != test.wantKind {
				t.Fatalf("failure state = %q/%q, want %q/%q", state.failureState, state.failureKind, test.wantState, test.wantKind)
			}
			if state.rawError != "" {
				t.Fatalf("raw error was persisted: %q", state.rawError)
			}
		})
	}
}

func TestLoggingObserverSurfacesDurableDestinationBlocker(t *testing.T) {
	err := (LoggingObserver{}).Observe(context.Background(), "11111111-1111-4111-8111-111111111111", Extraction{ContractVersion: "signa.ai.report-extraction.v0"})
	if !errors.Is(err, ErrDurableExtractionDestinationUnresolved) {
		t.Fatalf("LoggingObserver.Observe() error = %v, want durable-destination blocker", err)
	}
}

type fakeReportReader struct {
	reportID string
	rawText  string
	err      error
}

func (r *fakeReportReader) LoadRawText(_ context.Context, reportID string) (string, error) {
	r.reportID = reportID
	return r.rawText, r.err
}

type fakeProvider struct {
	rawText string
	result  []byte
	err     error
	calls   int
}

func (p *fakeProvider) Extract(_ context.Context, rawText string) ([]byte, error) {
	p.calls++
	p.rawText = rawText
	return p.result, p.err
}

type fakeValidator struct {
	result Extraction
	err    error
}

type usageTestProvider struct {
	result []byte
	usage  Usage
}

func (p *usageTestProvider) Extract(context.Context, string) ([]byte, error) {
	return p.result, nil
}

func (p *usageTestProvider) ExtractWithUsage(context.Context, string) ([]byte, Usage, error) {
	return p.result, p.usage, nil
}

func (v *fakeValidator) Validate([]byte) (Extraction, error) { return v.result, v.err }

type fakeObserver struct {
	reportID string
	result   Extraction
	err      error
}

type recordingStateObserver struct {
	recordingObserver
	states       []string
	failureState string
	failureKind  string
	rawError     string
}

func (o *recordingStateObserver) MarkAIProcessingStarted(context.Context, string, time.Time) error {
	o.states = append(o.states, "started")
	return nil
}

func (o *recordingStateObserver) MarkAIProcessingSucceeded(context.Context, string, time.Time) error {
	o.states = append(o.states, "succeeded")
	return nil
}

func (o *recordingStateObserver) MarkAIProcessingFailed(_ context.Context, _ string, state string, kind FailureKind, _ time.Time) error {
	o.failureState = state
	o.failureKind = string(kind)
	o.states = append(o.states, state)
	return nil
}

type recordingObserver struct{}

func (o *recordingObserver) Observe(context.Context, string, Extraction) error { return nil }

func (o *fakeObserver) Observe(_ context.Context, reportID string, result Extraction) error {
	o.reportID = reportID
	o.result = result
	return o.err
}

type fakeAcker struct {
	calls     int
	messageID string
}

func (a *fakeAcker) Ack(_ context.Context, _, _, messageID string) error {
	a.calls++
	a.messageID = messageID
	return nil
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
