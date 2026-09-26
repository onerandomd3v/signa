package verification

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onerandomd3v/signa/internal/config"
	"github.com/onerandomd3v/signa/internal/incidents"
)

type testRepository struct {
	active    bool
	requests  []requestRecord
	response  Response
	created   bool
	submitErr error
}

func (r *testRepository) ActiveGrant(context.Context, uuid.UUID) (bool, error) { return r.active, nil }
func (r *testRepository) ListEligible(_ context.Context, _ uuid.UUID) ([]requestRecord, error) {
	return r.requests, nil
}
func (r *testRepository) GetEligible(_ context.Context, _ uuid.UUID, id uuid.UUID) (requestRecord, error) {
	for _, request := range r.requests {
		if request.ID == id {
			return request, nil
		}
	}
	return requestRecord{}, ErrRequestNotFound
}
func (r *testRepository) Submit(ctx context.Context, _, _, incidentID uuid.UUID, _ string, _ ResponseInput, _ int16, _ [32]byte, checkPublic publicEligibilityCheck) (Response, bool, error) {
	if err := checkPublic(ctx, incidentID); err != nil {
		return Response{}, false, err
	}
	return r.response, r.created, r.submitErr
}

type testPublicReader struct {
	missing map[uuid.UUID]bool
	calls   int
}

func (r *testPublicReader) ListPublicIncidents(context.Context, config.PublicIncidentGeometryPolicy) ([]incidents.PublicIncident, error) {
	panic("unused")
}
func (r *testPublicReader) GetPublicIncident(_ context.Context, id uuid.UUID, _ config.PublicIncidentGeometryPolicy) (incidents.PublicIncident, error) {
	r.calls++
	if r.missing[id] {
		return incidents.PublicIncident{}, incidents.ErrPublicIncidentNotFound
	}
	return incidents.PublicIncident{ID: id, Status: "OPEN", ConfidenceState: "UNVERIFIED"}, nil
}
func testService(repo *testRepository, public *testPublicReader) Service {
	return newService(repo, public, config.PublicIncidentGeometryPolicy{})
}
func testRequest() requestRecord {
	return requestRecord{ID: uuid.New(), IncidentID: uuid.New(), CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().Add(time.Hour).UTC()}
}

func TestListRequestsRequiresActiveTrustedVerifier(t *testing.T) {
	repo := &testRepository{}
	_, err := testService(repo, &testPublicReader{}).ListRequests(context.Background(), uuid.New())
	if !errors.Is(err, ErrNotTrustedVerifier) {
		t.Fatalf("error = %v", err)
	}
	repo.active = true
	views, err := testService(repo, &testPublicReader{}).ListRequests(context.Background(), uuid.New())
	if err != nil || len(views) != 0 {
		t.Fatalf("views=%v error=%v", views, err)
	}
}
func TestGetRequestHidesExpiredCancelledAndOtherAssignee(t *testing.T) {
	repo := &testRepository{active: true}
	id := uuid.New()
	_, err := testService(repo, &testPublicReader{}).GetRequest(context.Background(), uuid.New(), id)
	if !errors.Is(err, ErrRequestNotFound) {
		t.Fatalf("error=%v", err)
	}
}
func TestListRequestsUsesMaximumAndDeterministicOrder(t *testing.T) {
	repo := &testRepository{active: true, requests: []requestRecord{testRequest(), testRequest()}}
	views, err := testService(repo, &testPublicReader{}).ListRequests(context.Background(), uuid.New())
	if err != nil || len(views) != 2 {
		t.Fatalf("views=%v error=%v", views, err)
	}
	if views[0].SafetyPrompt != "Respond only from what you already safely know or observed. Never approach an incident or unsafe area to verify it." || views[0].Incident.ID != repo.requests[0].IncidentID {
		t.Fatalf("view=%+v", views[0])
	}
	encoded, err := json.Marshal(views[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"reporter", "raw_report", "assigned_verifier", "center_point", "verifier_id"} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("private field %q in %s", private, encoded)
		}
	}
	if maxRequestListLimit != 100 {
		t.Fatalf("limit=%d", maxRequestListLimit)
	}
}
func TestResponseRequiresAtLeastOneDimension(t *testing.T) {
	repo := &testRepository{active: true, requests: []requestRecord{testRequest()}}
	_, _, err := testService(repo, &testPublicReader{}).SubmitResponse(context.Background(), uuid.New(), repo.requests[0].ID, "key", ResponseInput{})
	if !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("error=%v", err)
	}
}
func TestResponsePreservesConclusionAndObservationIndependently(t *testing.T) {
	repo := &testRepository{active: true, requests: []requestRecord{testRequest()}, created: true}
	conclusion := ConclusionDispute
	repo.response = Response{ID: uuid.New(), Conclusion: &conclusion}
	response, created, err := testService(repo, &testPublicReader{}).SubmitResponse(context.Background(), uuid.New(), repo.requests[0].ID, "key", ResponseInput{Conclusion: &conclusion})
	if err != nil || !created || response.Conclusion == nil || *response.Conclusion != ConclusionDispute || response.Observation != nil {
		t.Fatalf("response=%+v created=%v err=%v", response, created, err)
	}
	observation := ObservationHeard
	repo.response = Response{ID: uuid.New(), Observation: &observation}
	response, created, err = testService(repo, &testPublicReader{}).SubmitResponse(context.Background(), uuid.New(), repo.requests[0].ID, "different-key", ResponseInput{Observation: &observation})
	if err != nil || !created || response.Conclusion != nil || response.Observation == nil || *response.Observation != ObservationHeard {
		t.Fatalf("observation-only response=%+v created=%v err=%v", response, created, err)
	}
}
func TestSubmitResponseMapsIdempotencyReplayAndConflict(t *testing.T) {
	repo := &testRepository{active: true, requests: []requestRecord{testRequest()}, response: Response{ID: uuid.New()}}
	c := ConclusionConfirm
	service := testService(repo, &testPublicReader{})
	response, created, err := service.SubmitResponse(context.Background(), uuid.New(), repo.requests[0].ID, "key", ResponseInput{Conclusion: &c})
	if err != nil || created || response.ID != repo.response.ID {
		t.Fatalf("replay=%+v %v %v", response, created, err)
	}
	repo.submitErr = ErrIdempotencyConflict
	_, _, err = service.SubmitResponse(context.Background(), uuid.New(), repo.requests[0].ID, "key", ResponseInput{Conclusion: &c})
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("error=%v", err)
	}
}
func TestRequestProjectionMissIsNotFound(t *testing.T) {
	request := testRequest()
	repo := &testRepository{active: true, requests: []requestRecord{request}}
	public := &testPublicReader{missing: map[uuid.UUID]bool{request.IncidentID: true}}
	service := testService(repo, public)
	views, err := service.ListRequests(context.Background(), uuid.New())
	if err != nil || len(views) != 0 {
		t.Fatalf("views=%v err=%v", views, err)
	}
	_, err = service.GetRequest(context.Background(), uuid.New(), request.ID)
	if !errors.Is(err, ErrRequestNotFound) {
		t.Fatalf("detail error=%v", err)
	}
	c := ConclusionConfirm
	_, _, err = service.SubmitResponse(context.Background(), uuid.New(), request.ID, "key", ResponseInput{Conclusion: &c})
	if !errors.Is(err, ErrRequestNotFound) {
		t.Fatalf("submit error=%v", err)
	}
	if public.calls != 3 {
		t.Fatalf("public projection calls=%d", public.calls)
	}
}
