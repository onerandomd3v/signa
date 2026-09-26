package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onerandomd3v/signa/internal/latency"
)

func TestParseOptionsReportIDAndRecentModes(t *testing.T) {
	wantID := uuid.New()
	got, err := parseOptions([]string{"-report-id", wantID.String()})
	if err != nil || got.reportID == nil || *got.reportID != wantID {
		t.Fatalf("report-id options = %+v, %v", got, err)
	}
	got, err = parseOptions([]string{"-limit", "100"})
	if err != nil || got.reportID != nil || got.limit != 100 {
		t.Fatalf("recent options = %+v, %v", got, err)
	}
}

func TestParseOptionsRejectsUnboundedOrAmbiguousModes(t *testing.T) {
	for _, args := range [][]string{{}, {"-report-id", uuid.NewString(), "-limit", "2"}, {"-limit", "101"}, {"-limit", "0"}, {"-report-id", "not-a-uuid"}} {
		if _, err := parseOptions(args); err == nil {
			t.Errorf("parseOptions(%v) error = nil", args)
		}
	}
}

func TestWriteResultEmitsStablePrivacySafeJSON(t *testing.T) {
	id := uuid.New()
	reader := &fakeReader{item: latency.Timeline{ReportID: id.String(), Stages: []latency.StageTiming{{Name: latency.StageReportPersist, State: latency.StateCompleted}}}}
	var output bytes.Buffer
	if err := writeResult(context.Background(), reader, options{reportID: &id}, &output); err != nil {
		t.Fatal(err)
	}
	encoded := output.String()
	for _, want := range []string{`"report_id"`, `"report_persist_ms"`, `"stages"`, `"report_persist"`} {
		if !strings.Contains(encoded, want) {
			t.Errorf("JSON missing %q: %s", want, encoded)
		}
	}
	for _, forbidden := range []string{"raw_text", "reporter_id", "claimed_location", "push_endpoint", "provider_response", "health_score"} {
		if strings.Contains(encoded, forbidden) {
			t.Errorf("JSON contains forbidden %q: %s", forbidden, encoded)
		}
	}
}

func TestWriteResultUsesBoundedRecentModeAndHidesDatabaseError(t *testing.T) {
	reader := &fakeReader{err: errors.New("connection string with private secret")}
	if err := writeResult(context.Background(), reader, options{limit: 3}, &bytes.Buffer{}); err == nil || strings.Contains(err.Error(), "private secret") {
		t.Fatalf("query error = %v, want generic safe error", err)
	}
	if reader.limit != 3 {
		t.Fatalf("Recent limit = %d, want 3", reader.limit)
	}
}

func TestWriteResultReturnsSpecificSafeMessageWhenReportIsMissing(t *testing.T) {
	id := uuid.New()
	reader := &fakeReader{err: fmt.Errorf("store lookup: %w", latency.ErrReportNotFound)}
	err := writeResult(context.Background(), reader, options{reportID: &id}, &bytes.Buffer{})
	if err == nil || err.Error() != "report was not found" {
		t.Fatalf("not-found error = %v, want safe operator message", err)
	}
}

func TestWriteResultKeepsTraceDatabaseFailuresGeneric(t *testing.T) {
	id := uuid.New()
	reader := &fakeReader{err: errors.New("query failed for postgres://user:private-secret@db")}
	err := writeResult(context.Background(), reader, options{reportID: &id}, &bytes.Buffer{})
	if err == nil || err.Error() != "report trace query failed" || strings.Contains(err.Error(), "private-secret") {
		t.Fatalf("trace query error = %v, want generic safe message", err)
	}
}

func TestRunReportsConfigurationAndDatabaseReadinessErrors(t *testing.T) {
	t.Setenv("SIGNA_WORKER_INTERVAL", "not-a-duration")
	err := run(context.Background(), []string{"-limit", "1"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "load Signa configuration") {
		t.Fatalf("configuration error = %v", err)
	}

	t.Setenv("SIGNA_WORKER_INTERVAL", "500ms")
	t.Setenv("SIGNA_DATABASE_URL", "postgres://signa:private-secret@127.0.0.1:1/signa?sslmode=disable")
	err = run(context.Background(), []string{"-limit", "1"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "database is unavailable") || strings.Contains(err.Error(), "private-secret") {
		t.Fatalf("database error = %v, want a safe readiness message", err)
	}
}

type fakeReader struct {
	item  latency.Timeline
	items []latency.Timeline
	err   error
	id    uuid.UUID
	limit int
}

func (f *fakeReader) TraceReport(_ context.Context, id uuid.UUID) (latency.Timeline, error) {
	f.id = id
	return f.item, f.err
}

func (f *fakeReader) Recent(_ context.Context, limit int) ([]latency.Timeline, error) {
	f.limit = limit
	return f.items, f.err
}
