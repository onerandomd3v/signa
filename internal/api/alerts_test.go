package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onerandomd3v/signa/internal/alerts"
	"github.com/onerandomd3v/signa/internal/auth"
	"github.com/onerandomd3v/signa/internal/config"
	"github.com/onerandomd3v/signa/internal/priority"
)

type alertReaderForAPITest struct {
	read alerts.AlertRead
	err  error
}

func (f *alertReaderForAPITest) ReadAuthorized(context.Context, uuid.UUID, uuid.UUID) (alerts.AlertRead, error) {
	return f.read, f.err
}

func TestAuthenticatedAlertReadRoute(t *testing.T) {
	alertID := uuid.New()
	reader := alertReaderForAPITest{read: alerts.AlertRead{
		ID: alertID, IncidentID: uuid.New(), AlertType: alerts.AlertTypeImmediate,
		ConfidenceSnapshot: "EMERGING", SeveritySnapshot: "HIGH", StatusSnapshot: "OPEN",
		PrioritySnapshot: priority.P1, FreshnessSnapshot: alerts.FreshnessFresh,
		Message: "safe summary", AsOf: time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC),
		CreatedAt: time.Date(2026, 9, 25, 12, 0, 1, 0, time.UTC),
	}}
	server := NewHandlerWithMediaAndCORSAndPublicIncidentsAndAuth(nil, DefaultRateLimitConfig(), nil, nil, nil, nil, config.PublicIncidentGeometryPolicy{}, AuthConfig{
		Store:  sessionStoreForAPITest{secret: "cookie-secret", principal: auth.Principal{UserID: uuid.New()}},
		Alerts: &reader,
	})

	t.Run("missing session is canonical JSON 401", func(t *testing.T) {
		response := requestAlert(server, "missing", nil)
		assertAlertErrorResponse(t, response, http.StatusUnauthorized, "unauthenticated")
	})
	t.Run("invalid session is canonical JSON 401", func(t *testing.T) {
		response := requestAlert(server, alertID.String(), &http.Cookie{Name: auth.DefaultCookieName, Value: "wrong"})
		assertAlertErrorResponse(t, response, http.StatusUnauthorized, "unauthenticated")
	})
	t.Run("malformed id is canonical JSON 400", func(t *testing.T) {
		response := requestAlert(server, "not-a-uuid", &http.Cookie{Name: auth.DefaultCookieName, Value: "cookie-secret"})
		assertAlertErrorResponse(t, response, http.StatusBadRequest, "invalid_alert_id")
	})
	t.Run("authorized user receives safe snapshot", func(t *testing.T) {
		response := requestAlert(server, alertID.String(), &http.Cookie{Name: auth.DefaultCookieName, Value: "cookie-secret"})
		if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("response = %d %v", response.Code, response.Header())
		}
		body := response.Body.String()
		for _, forbidden := range []string{"provider_response", "subscription", "endpoint", "reporter", "coordinates", "request_fingerprint", "eligibility_reasons", "idempotency_key"} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("alert response leaked %q: %s", forbidden, body)
			}
		}
		var decoded alerts.AlertRead
		if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil || decoded.ID != alertID || decoded.Message != "safe summary" {
			t.Fatalf("decoded response = %+v, err = %v", decoded, err)
		}
	})
	t.Run("not visible is privacy safe 404", func(t *testing.T) {
		reader.err = alerts.ErrAlertNotVisible
		response := requestAlert(server, alertID.String(), &http.Cookie{Name: auth.DefaultCookieName, Value: "cookie-secret"})
		assertAlertErrorResponse(t, response, http.StatusNotFound, "alert_not_found")
	})
	t.Run("session store outage remains JSON 503", func(t *testing.T) {
		outage := NewHandlerWithMediaAndCORSAndPublicIncidentsAndAuth(nil, DefaultRateLimitConfig(), nil, nil, nil, nil, config.PublicIncidentGeometryPolicy{}, AuthConfig{
			Store:  sessionStoreForAPITest{secret: "cookie-secret", err: errors.New("connection refused")},
			Alerts: &alertReaderForAPITest{},
		})
		response := requestAlert(outage, alertID.String(), &http.Cookie{Name: auth.DefaultCookieName, Value: "cookie-secret"})
		assertAlertErrorResponse(t, response, http.StatusServiceUnavailable, "authentication_unavailable")
	})
}

func requestAlert(handler http.Handler, alertID string, cookie *http.Cookie) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, "/alerts/"+alertID, nil)
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func assertAlertErrorResponse(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status || response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("response = %d %v, want %d JSON", response.Code, response.Header(), status)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Error.Code != code {
		t.Fatalf("error body = %q, decoded code=%q, err=%v", response.Body.String(), body.Error.Code, err)
	}
}
