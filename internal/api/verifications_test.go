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
	"github.com/onerandomd3v/signa/internal/auth"
	"github.com/onerandomd3v/signa/internal/config"
	"github.com/onerandomd3v/signa/internal/incidents"
	"github.com/onerandomd3v/signa/internal/verification"
)

type fakeVerificationService struct {
	list   func(context.Context, uuid.UUID) ([]verification.RequestView, error)
	get    func(context.Context, uuid.UUID, uuid.UUID) (verification.RequestView, error)
	submit func(context.Context, uuid.UUID, uuid.UUID, string, verification.ResponseInput) (verification.Response, bool, error)
}

func (f fakeVerificationService) ListRequests(ctx context.Context, user uuid.UUID) ([]verification.RequestView, error) {
	return f.list(ctx, user)
}
func (f fakeVerificationService) GetRequest(ctx context.Context, user, id uuid.UUID) (verification.RequestView, error) {
	return f.get(ctx, user, id)
}
func (f fakeVerificationService) SubmitResponse(ctx context.Context, user, id uuid.UUID, key string, input verification.ResponseInput) (verification.Response, bool, error) {
	return f.submit(ctx, user, id, key, input)
}

func verificationHandler(service verification.Service) http.Handler {
	return NewHandlerWithMediaAndCORSAndPublicIncidentsAndAuth(nil, DefaultRateLimitConfig(), nil, nil, nil, nil, config.PublicIncidentGeometryPolicy{}, AuthConfig{Store: sessionStoreForAPITest{secret: "cookie-secret", principal: auth.Principal{UserID: verificationTestUser}}, Verification: service})
}

var verificationTestUser = uuid.MustParse("11111111-1111-4111-8111-111111111111")
var verificationTestRequest = uuid.MustParse("22222222-2222-4222-8222-222222222222")

func verificationCall(handler http.Handler, method, path, body string, cookie bool, key string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if cookie {
		req.AddCookie(&http.Cookie{Name: "signa_session", Value: "cookie-secret"})
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}
func assertVerificationError(t *testing.T, rec *httptest.ResponseRecorder, status int, code, message string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	want := `{"error":{"code":"` + code + `","message":"` + message + `"}}`
	if strings.TrimSpace(rec.Body.String()) != want {
		t.Fatalf("body=%s want=%s", rec.Body.String(), want)
	}
}

func TestVerificationRoutesUseCanonicalSessionAndCapability(t *testing.T) {
	service := fakeVerificationService{list: func(_ context.Context, user uuid.UUID) ([]verification.RequestView, error) {
		if user != verificationTestUser {
			t.Fatalf("user=%s", user)
		}
		return []verification.RequestView{}, nil
	}}
	handler := verificationHandler(service)
	assertVerificationError(t, verificationCall(handler, "GET", "/v1/verifications/requests?user_id="+uuid.NewString(), "", false, ""), 401, "unauthenticated", "authentication is required")
	bearer := httptest.NewRequest("GET", "/v1/verifications/requests", nil)
	bearer.Header.Set("Authorization", "Bearer "+uuid.NewString())
	bearer.Header.Set("X-User-ID", verificationTestUser.String())
	bearerResponse := httptest.NewRecorder()
	handler.ServeHTTP(bearerResponse, bearer)
	assertVerificationError(t, bearerResponse, 401, "unauthenticated", "authentication is required")
	rec := verificationCall(handler, "GET", "/v1/verifications/requests?user_id="+uuid.NewString(), "", true, "")
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	forbidden := verificationHandler(fakeVerificationService{list: func(context.Context, uuid.UUID) ([]verification.RequestView, error) {
		return nil, verification.ErrNotTrustedVerifier
	}})
	assertVerificationError(t, verificationCall(forbidden, "GET", "/v1/verifications/requests", "", true, ""), 403, "forbidden", "trusted verifier access is required")
	withoutService := NewHandlerWithMediaAndCORSAndPublicIncidentsAndAuth(nil, DefaultRateLimitConfig(), nil, nil, nil, nil, config.PublicIncidentGeometryPolicy{}, AuthConfig{Store: sessionStoreForAPITest{secret: "cookie-secret", principal: auth.Principal{UserID: verificationTestUser}}})
	if rec := verificationCall(withoutService, "GET", "/v1/verifications/requests", "", true, ""); rec.Code != 404 {
		t.Fatalf("unconfigured route status=%d", rec.Code)
	}
}
func TestVerificationRoutesReturnPrivacySafeNotFoundForOtherAssignee(t *testing.T) {
	service := fakeVerificationService{get: func(context.Context, uuid.UUID, uuid.UUID) (verification.RequestView, error) {
		return verification.RequestView{}, verification.ErrRequestNotFound
	}, submit: func(context.Context, uuid.UUID, uuid.UUID, string, verification.ResponseInput) (verification.Response, bool, error) {
		return verification.Response{}, false, verification.ErrRequestNotFound
	}}
	handler := verificationHandler(service)
	path := "/v1/verifications/requests/" + verificationTestRequest.String()
	missing := verificationCall(handler, "GET", path, "", true, "")
	invalid := verificationCall(handler, "GET", "/v1/verifications/requests/not-a-uuid", "", true, "")
	assertVerificationError(t, missing, 404, "verification_request_not_found", "verification request was not found")
	assertVerificationError(t, invalid, 400, "invalid_request", "request_id must be a valid UUID")
	assertVerificationError(t, verificationCall(handler, "POST", path+"/responses", `{"conclusion":"CONFIRM"}`, true, "key"), 404, "verification_request_not_found", "verification request was not found")
	assertVerificationError(t, verificationCall(handler, "POST", "/v1/verifications/requests/not-a-uuid/responses", `{"conclusion":"CONFIRM"}`, true, "key"), 400, "invalid_request", "request_id must be a valid UUID")
}
func TestVerificationResponseValidatesIdempotencyKeyAndEnums(t *testing.T) {
	service := fakeVerificationService{submit: func(context.Context, uuid.UUID, uuid.UUID, string, verification.ResponseInput) (verification.Response, bool, error) {
		t.Fatal("invalid input reached service")
		return verification.Response{}, false, nil
	}}
	handler := verificationHandler(service)
	path := "/v1/verifications/requests/" + verificationTestRequest.String() + "/responses"
	cases := []struct{ body, key string }{{`{"conclusion":"CONFIRM"}`, ""}, {`{"conclusion":"CONFIRM"}`, "   "}, {`{"conclusion":"CONFIRM"}`, strings.Repeat("a", 256)}, {`{"conclusion":"YES"}`, "key"}, {`{"observation":"LOOKED"}`, "key"}, {`{}`, "key"}, {`null`, "key"}, {`[]`, "key"}, {`{"conclusion":"CONFIRM"} {}`, "key"}, {`{"conclusion":"CONFIRM","extra":1}`, "key"}}
	for _, tc := range cases {
		rec := verificationCall(handler, "POST", path, tc.body, true, tc.key)
		if rec.Code != 400 {
			t.Errorf("body=%q key length=%d status=%d response=%s", tc.body, len(tc.key), rec.Code, rec.Body.String())
		}
	}
}
func TestVerificationResponseMapsReplayAndConflict(t *testing.T) {
	conclusion := verification.ConclusionConfirm
	id := uuid.New()
	response := verification.Response{ID: id, RequestID: verificationTestRequest, IncidentID: uuid.New(), Conclusion: &conclusion, CreatedAt: time.Now()}
	created := true
	service := fakeVerificationService{submit: func(_ context.Context, user, request uuid.UUID, key string, input verification.ResponseInput) (verification.Response, bool, error) {
		if user != verificationTestUser || request != verificationTestRequest || key != "trimmed" || input.Conclusion == nil || *input.Conclusion != conclusion {
			t.Fatalf("wrong submission: %s %s %q %+v", user, request, key, input)
		}
		if created {
			created = false
			return response, true, nil
		}
		return response, false, nil
	}}
	handler := verificationHandler(service)
	path := "/v1/verifications/requests/" + verificationTestRequest.String() + "/responses"
	first := verificationCall(handler, "POST", path, `{"conclusion":"CONFIRM"}`, true, " trimmed ")
	replay := verificationCall(handler, "POST", path, `{"conclusion":"CONFIRM"}`, true, " trimmed ")
	if first.Code != 201 || replay.Code != 200 || first.Body.String() != replay.Body.String() {
		t.Fatalf("first=%d %s replay=%d %s", first.Code, first.Body.String(), replay.Code, replay.Body.String())
	}
	conflict := verificationHandler(fakeVerificationService{submit: func(context.Context, uuid.UUID, uuid.UUID, string, verification.ResponseInput) (verification.Response, bool, error) {
		return verification.Response{}, false, verification.ErrIdempotencyConflict
	}})
	assertVerificationError(t, verificationCall(conflict, "POST", path, `{"conclusion":"CONFIRM"}`, true, "key"), 409, "idempotency_conflict", "Idempotency-Key was already used for a different response")
	var body map[string]any
	if err := json.Unmarshal(first.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body) != 6 || body["conclusion"] != "CONFIRM" || body["observation"] != nil {
		t.Fatalf("response fields=%v", body)
	}
}
func TestVerificationErrorsNeverExposeUnderlyingDatabaseText(t *testing.T) {
	secret := "private database detail"
	service := fakeVerificationService{list: func(context.Context, uuid.UUID) ([]verification.RequestView, error) { return nil, errors.New(secret) }, get: func(context.Context, uuid.UUID, uuid.UUID) (verification.RequestView, error) {
		return verification.RequestView{}, errors.New(secret)
	}, submit: func(context.Context, uuid.UUID, uuid.UUID, string, verification.ResponseInput) (verification.Response, bool, error) {
		return verification.Response{}, false, errors.New(secret)
	}}
	handler := verificationHandler(service)
	path := "/v1/verifications/requests"
	for _, rec := range []*httptest.ResponseRecorder{verificationCall(handler, "GET", path, "", true, ""), verificationCall(handler, "GET", path+"/"+verificationTestRequest.String(), "", true, ""), verificationCall(handler, "POST", path+"/"+verificationTestRequest.String()+"/responses", `{"observation":"SAW"}`, true, "key")} {
		assertVerificationError(t, rec, 503, "verification_unavailable", "verification service is unavailable")
		if strings.Contains(rec.Body.String(), secret) {
			t.Fatal("database error leaked")
		}
	}
}
func TestVerificationRequestSerializationContainsOnlyPublicProjection(t *testing.T) {
	incident := incidents.PublicIncident{ID: uuid.New(), Status: "OPEN", ConfidenceState: "UNVERIFIED", PublicGeometry: json.RawMessage(`{"type":"Polygon","coordinates":[]}`), UpdatedAt: time.Now()}
	view := verification.RequestView{ID: verificationTestRequest, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour), Incident: incident, SafetyPrompt: verification.SafetyPrompt}
	service := fakeVerificationService{list: func(context.Context, uuid.UUID) ([]verification.RequestView, error) {
		return []verification.RequestView{view}, nil
	}, get: func(context.Context, uuid.UUID, uuid.UUID) (verification.RequestView, error) { return view, nil }}
	handler := verificationHandler(service)
	for _, path := range []string{"/v1/verifications/requests", "/v1/verifications/requests/" + verificationTestRequest.String()} {
		rec := verificationCall(handler, "GET", path, "", true, "")
		if rec.Code != 200 {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if !strings.Contains(body, verification.SafetyPrompt) || !strings.Contains(body, "public_geometry") {
			t.Fatalf("missing public view: %s", body)
		}
		for _, private := range []string{"reporter_id", "assigned_verifier_id", "verifier_id", "fingerprint", "idempotency_key", "raw_text", "center_point"} {
			if strings.Contains(body, private) {
				t.Fatalf("private field %q: %s", private, body)
			}
		}
	}
}
