package extraction

import (
	"context"
	"errors"
	"testing"
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

func (v *fakeValidator) Validate([]byte) (Extraction, error) { return v.result, v.err }

type fakeObserver struct {
	reportID string
	result   Extraction
	err      error
}

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
