package verification

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onerandomd3v/signa/internal/config"
	"github.com/onerandomd3v/signa/internal/incidents"
)

type targetingRepositoryFunc func(context.Context, uuid.UUID, config.VerificationTargetingPolicy, config.PublicIncidentGeometryPolicy) (int, error)

func (f targetingRepositoryFunc) TargetIncident(ctx context.Context, incidentID uuid.UUID, targeting config.VerificationTargetingPolicy, public config.PublicIncidentGeometryPolicy) (int, error) {
	return f(ctx, incidentID, targeting, public)
}

func TestTargetingProcessorAcceptsDurableIncidentEvents(t *testing.T) {
	incidentID := uuid.New()
	targetPolicy := config.VerificationTargetingPolicy{
		Version: config.VerificationTargetingPolicyVersion, RadiusMeters: 500,
		LocationMaxAge: 10 * time.Minute, MaxCandidates: 12, RequestLifetime: 45 * time.Minute,
	}
	publicPolicy := config.PublicIncidentGeometryPolicy{
		Version: config.PublicIncidentGeometryPolicyVersion, GridMeters: 100, MinRadiusMeters: 250, SimplifyMeters: 25,
	}
	calls := 0
	processor := newTargetingProcessor(targetingRepositoryFunc(func(_ context.Context, got uuid.UUID, target config.VerificationTargetingPolicy, public config.PublicIncidentGeometryPolicy) (int, error) {
		calls++
		if got != incidentID || target != targetPolicy || public != publicPolicy {
			t.Fatalf("target call got=%s targeting=%+v public=%+v", got, target, public)
		}
		return 1, nil
	}), targetPolicy, publicPolicy)
	for _, name := range []string{incidents.IncidentCreatedV1, incidents.IncidentReportAttachedV1} {
		message := incidents.StreamMessage{Values: map[string]any{
			"event_name": name, "aggregate_type": "incident",
			"payload": `{"incident_id":"` + incidentID.String() + `"}`,
		}}
		if err := processor.Process(context.Background(), message); err != nil {
			t.Fatalf("Process(%s): %v", name, err)
		}
	}
	if calls != 2 {
		t.Fatalf("TargetIncident calls=%d", calls)
	}
}

func TestTargetingProcessorRejectsUnsupportedOrMalformedEvents(t *testing.T) {
	processor := newTargetingProcessor(targetingRepositoryFunc(func(context.Context, uuid.UUID, config.VerificationTargetingPolicy, config.PublicIncidentGeometryPolicy) (int, error) {
		t.Fatal("target repository should not run")
		return 0, nil
	}), testTargetingPolicy(), testPublicGeometryPolicy())
	for _, message := range []incidents.StreamMessage{
		{Values: map[string]any{"event_name": incidents.IncidentResolvedV1, "aggregate_type": "incident", "payload": `{"incident_id":"` + uuid.NewString() + `"}`}},
		{Values: map[string]any{"event_name": incidents.IncidentCreatedV1, "aggregate_type": "report", "payload": `{"incident_id":"` + uuid.NewString() + `"}`}},
		{Values: map[string]any{"event_name": incidents.IncidentCreatedV1, "aggregate_type": "incident", "payload": `{"incident_id":"invalid"}`}},
	} {
		if err := processor.Process(context.Background(), message); err == nil {
			t.Fatal("Process() error = nil")
		}
	}
}

func TestTargetingProcessorReturnsRepositoryFailureForDurableRetry(t *testing.T) {
	privateVerifierID := uuid.NewString()
	want := errors.New("database failure for verifier " + privateVerifierID + " at exact location 6.52439,3.37921")
	processor := newTargetingProcessor(targetingRepositoryFunc(func(context.Context, uuid.UUID, config.VerificationTargetingPolicy, config.PublicIncidentGeometryPolicy) (int, error) {
		return 0, want
	}), testTargetingPolicy(), testPublicGeometryPolicy())
	message := incidents.StreamMessage{Values: map[string]any{
		"event_name": incidents.IncidentCreatedV1, "aggregate_type": "incident",
		"payload": `{"incident_id":"` + uuid.NewString() + `"}`,
	}}
	if err := processor.Process(context.Background(), message); err == nil || errors.Is(err, want) || strings.Contains(err.Error(), privateVerifierID) || strings.Contains(err.Error(), "6.52439") {
		t.Fatalf("Process() must return a sanitized retryable error, got %v", err)
	}
}

func TestTargetingPolicyFingerprintIsStableAndCoversAllPolicyFields(t *testing.T) {
	base := testTargetingPolicy()
	first := targetingPolicyFingerprint(base)
	if second := targetingPolicyFingerprint(base); first != second {
		t.Fatal("same policy produced different fingerprints")
	}
	mutations := []func(*config.VerificationTargetingPolicy){
		func(p *config.VerificationTargetingPolicy) { p.Version += ".next" },
		func(p *config.VerificationTargetingPolicy) { p.RadiusMeters++ },
		func(p *config.VerificationTargetingPolicy) { p.LocationMaxAge++ },
		func(p *config.VerificationTargetingPolicy) { p.MaxCandidates++ },
		func(p *config.VerificationTargetingPolicy) { p.RequestLifetime++ },
	}
	for i, mutate := range mutations {
		changed := base
		mutate(&changed)
		if got := targetingPolicyFingerprint(changed); got == first {
			t.Fatalf("policy field mutation %d did not alter fingerprint", i)
		}
	}
}

func testTargetingPolicy() config.VerificationTargetingPolicy {
	return config.VerificationTargetingPolicy{Version: config.VerificationTargetingPolicyVersion, RadiusMeters: 500, LocationMaxAge: 10 * time.Minute, MaxCandidates: 12, RequestLifetime: 45 * time.Minute}
}

func testPublicGeometryPolicy() config.PublicIncidentGeometryPolicy {
	return config.PublicIncidentGeometryPolicy{Version: config.PublicIncidentGeometryPolicyVersion, GridMeters: 100, MinRadiusMeters: 250, SimplifyMeters: 25}
}
