package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onerandomd3v/signa/internal/reports"
)

type fakeIngestor struct {
	acknowledgement reports.Acknowledgement
	err             error
	key             string
	input           reports.IngestRequest
	calls           int
}

func (f *fakeIngestor) Ingest(_ context.Context, key string, input reports.IngestRequest) (reports.Acknowledgement, error) {
	f.calls++
	f.key = key
	f.input = input
	return f.acknowledgement, f.err
}

func TestCreateReportAcceptsTextOnly(t *testing.T) {
	ingestor := &fakeIngestor{acknowledgement: reports.Acknowledgement{ReportID: "report-1", SubmittedAt: "2026-09-23T00:00:00Z"}}
	recorder := performReportRequest(t, ingestor, "key-1", `{"raw_text":"  Road is blocked  "}`)

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusAccepted)
	}
	var response map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response["report_id"] != "report-1" || response["status"] != "accepted" || response["submitted_at"] == "" {
		t.Fatalf("response = %+v, want acknowledgement", response)
	}
	if ingestor.input.RawText != "Road is blocked" || ingestor.input.DeviceLocation != nil {
		t.Fatalf("input = %+v, want trimmed text without location", ingestor.input)
	}
}

func TestCreateReportAcceptsDeviceLocation(t *testing.T) {
	ingestor := &fakeIngestor{}
	recorder := performReportRequest(t, ingestor, "key-1", `{"raw_text":"Incident near the junction","device_location":{"latitude":6.5244,"longitude":3.3792,"accuracy":12.5}}`)

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusAccepted)
	}
	if ingestor.input.DeviceLocation == nil || ingestor.input.DeviceLocation.Latitude != 6.5244 || ingestor.input.DeviceLocation.Accuracy == nil || *ingestor.input.DeviceLocation.Accuracy != 12.5 {
		t.Fatalf("input location = %+v, want validated location", ingestor.input.DeviceLocation)
	}
}

func TestCreateReportRejectsInvalidRequests(t *testing.T) {
	tests := []struct {
		name string
		key  string
		body string
	}{
		{name: "missing idempotency key", body: `{"raw_text":"report"}`},
		{name: "idempotency key too long", key: strings.Repeat("k", maxIdempotencyKeyLength+1), body: `{"raw_text":"report"}`},
		{name: "malformed json", key: "key-1", body: `{"raw_text":`},
		{name: "empty text", key: "key-1", body: `{"raw_text":"  "}`},
		{name: "missing latitude", key: "key-1", body: `{"raw_text":"report","device_location":{"longitude":3}}`},
		{name: "invalid latitude", key: "key-1", body: `{"raw_text":"report","device_location":{"latitude":91,"longitude":3}}`},
		{name: "invalid longitude", key: "key-1", body: `{"raw_text":"report","device_location":{"latitude":6,"longitude":181}}`},
		{name: "negative accuracy", key: "key-1", body: `{"raw_text":"report","device_location":{"latitude":6,"longitude":3,"accuracy":-1}}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := performReportRequest(t, &fakeIngestor{}, test.key, test.body)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
			}
			code := "invalid_request"
			switch test.name {
			case "missing idempotency key":
				code = "missing_idempotency_key"
			case "idempotency key too long":
				code = "invalid_idempotency_key"
			}
			assertErrorResponse(t, recorder, code)
		})
	}
}

func TestCreateReportReturnsConflictError(t *testing.T) {
	ingestor := &fakeIngestor{err: reports.ErrIdempotencyConflict}
	recorder := performReportRequest(t, ingestor, "key-1", `{"raw_text":"report"}`)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusConflict)
	}
	assertErrorResponse(t, recorder, "idempotency_conflict")
}

func TestCreateReportReturnsInternalErrorContract(t *testing.T) {
	ingestor := &fakeIngestor{err: errors.New("database unavailable")}
	recorder := performReportRequest(t, ingestor, "key-1", `{"raw_text":"report"}`)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
	assertErrorResponse(t, recorder, "internal_error")
}

func TestCreateReportLogsInternalErrors(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	request := httptest.NewRequest(http.MethodPost, "/reports", strings.NewReader(`{"raw_text":"report"}`))
	request.Header.Set("Idempotency-Key", "key-1")
	recorder := httptest.NewRecorder()
	NewHandler(logger, &fakeIngestor{err: errors.New("database unavailable")}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
	if !strings.Contains(logs.String(), "report ingestion failed") || !strings.Contains(logs.String(), "database unavailable") {
		t.Fatalf("logs = %q, want internal error details", logs.String())
	}
}

func performReportRequest(t *testing.T, ingestor reports.Ingestor, key string, body string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/reports", strings.NewReader(body))
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	request.Header.Set("Content-Type", "application/json")
	NewHandler(nil, ingestor).ServeHTTP(recorder, request)
	return recorder
}

func assertErrorResponse(t *testing.T, recorder *httptest.ResponseRecorder, code string) {
	t.Helper()
	var response errorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if response.Error.Code != code || response.Error.Message == "" {
		t.Fatalf("error response = %+v, want code %q", response.Error, code)
	}
}
