package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onerandomd3v/signa/internal/config"
	"github.com/onerandomd3v/signa/internal/incidents"
)

type fakePublicIncidentReader struct {
	items       []incidents.PublicIncident
	listErr     error
	detail      incidents.PublicIncident
	detailErr   error
	listCalls   int
	detailCalls int
}

func (f *fakePublicIncidentReader) ListPublicIncidents(context.Context, config.PublicIncidentGeometryPolicy) ([]incidents.PublicIncident, error) {
	f.listCalls++
	return f.items, f.listErr
}

func (f *fakePublicIncidentReader) GetPublicIncident(context.Context, uuid.UUID, config.PublicIncidentGeometryPolicy) (incidents.PublicIncident, error) {
	f.detailCalls++
	return f.detail, f.detailErr
}

func TestPublicIncidentRoutes(t *testing.T) {
	id := uuid.New()
	reader := &fakePublicIncidentReader{
		items: []incidents.PublicIncident{{
			ID:              id,
			EventType:       stringPointer("road_blockage"),
			Status:          "OPEN",
			ConfidenceState: "UNVERIFIED",
			PublicGeometry:  []byte(`{"type":"Polygon","coordinates":[]}`),
			UpdatedAt:       time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC),
		}},
		detail: incidents.PublicIncident{
			ID:             id,
			Status:         "OPEN",
			PublicGeometry: []byte(`{"type":"Polygon","coordinates":[]}`),
			UpdatedAt:      time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC),
		},
	}
	policy := config.PublicIncidentGeometryPolicy{Version: config.PublicIncidentGeometryPolicyVersion, GridMeters: 100, MinRadiusMeters: 250, SimplifyMeters: 25}
	handler := NewHandlerWithMediaAndCORSAndPublicIncidents(nil, DefaultRateLimitConfig(), []string{"http://localhost:3000"}, nil, nil, reader, policy)

	t.Run("list", func(t *testing.T) {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/incidents", nil))
		if response.Code != http.StatusOK || reader.listCalls != 1 {
			t.Fatalf("status/calls = %d/%d, want 200/1", response.Code, reader.listCalls)
		}
		if body := response.Body.String(); body == "" || containsAny(body, "center_point", "raw_text", "device_location", "latitude", "longitude") {
			t.Fatalf("public response leaked restricted fields: %s", body)
		}
	})

	t.Run("detail", func(t *testing.T) {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/incidents/"+id.String(), nil))
		if response.Code != http.StatusOK || reader.detailCalls != 1 {
			t.Fatalf("status/calls = %d/%d, want 200/1", response.Code, reader.detailCalls)
		}
	})

	t.Run("empty list is an array", func(t *testing.T) {
		reader.items = nil
		reader.listErr = nil
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/incidents", nil))
		if response.Code != http.StatusOK || response.Body.String() != "[]\n" {
			t.Fatalf("status/body = %d/%q, want 200/[]", response.Code, response.Body.String())
		}
	})

	t.Run("invalid id", func(t *testing.T) {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/incidents/not-a-uuid", nil))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", response.Code)
		}
	})

	t.Run("not found", func(t *testing.T) {
		reader.detailErr = incidents.ErrPublicIncidentNotFound
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/incidents/"+uuid.NewString(), nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", response.Code)
		}
	})

	t.Run("internal error", func(t *testing.T) {
		reader.listErr = errors.New("database unavailable")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/incidents", nil))
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", response.Code)
		}
	})
}

func stringPointer(value string) *string { return &value }

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}
