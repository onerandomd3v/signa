package latency

import (
	"encoding/json"
	"testing"
	"time"
)

func TestBuildTimelineCompletedPathKeepsStageDurationsAndPrivacySafeJSON(t *testing.T) {
	base := time.Date(2026, 9, 26, 1, 0, 0, 0, time.UTC)
	end := func(ms int) *time.Time { value := base.Add(time.Duration(ms) * time.Millisecond); return &value }
	input := Input{
		ReportID:          "report-1",
		Now:               *end(2500),
		ReportSubmittedAt: end(0), ReportPersistedAt: end(100),
		Outbox:   Observation{StartedAt: end(100), CompletedAt: end(300), State: StateCompleted},
		AI:       Observation{StartedAt: end(400), CompletedAt: end(800), State: StateCompleted},
		Incident: Observation{StartedAt: end(800), CompletedAt: end(1200), State: StateCompleted},
		Priority: Observation{State: StateUnavailable},
		Alert:    Observation{StartedAt: end(1200), CompletedAt: end(1500), State: StateCompleted},
		Delivery: DeliveryObservation{
			Observation:      Observation{StartedAt: end(1600), CompletedAt: end(2000), State: StateCompleted},
			AttemptStartedAt: end(1700), AttemptCompletedAt: end(1900), DeliveredAt: end(2000),
		},
	}

	got := BuildTimeline(input)
	if got.ReportPersistMs != nil || got.Stage(StageReportPersist).State != StateCompleted {
		t.Fatalf("report persistence = %v/%s, want completed with unmeasurable duration", got.ReportPersistMs, got.Stage(StageReportPersist).State)
	}
	if got.OutboxPublishMs == nil || *got.OutboxPublishMs != 200 {
		t.Fatalf("outbox publish = %v, want 200ms", got.OutboxPublishMs)
	}
	if got.AIProcessingMs == nil || *got.AIProcessingMs != 400 || got.IncidentProcessingMs == nil || *got.IncidentProcessingMs != 400 {
		t.Fatalf("processing durations = %v/%v, want 400/400ms", got.AIProcessingMs, got.IncidentProcessingMs)
	}
	if got.AlertCreationMs == nil || *got.AlertCreationMs != 300 || got.DeliveryQueueMs == nil || *got.DeliveryQueueMs != 100 || got.DeliveryAttemptMs == nil || *got.DeliveryAttemptMs != 200 {
		t.Fatalf("downstream durations = %v/%v/%v, want 300/100/200ms", got.AlertCreationMs, got.DeliveryQueueMs, got.DeliveryAttemptMs)
	}
	if got.ReportToAlertMs == nil || *got.ReportToAlertMs != 1500 || got.ReportToDeliveryMs == nil || *got.ReportToDeliveryMs != 2000 {
		t.Fatalf("end-to-end durations = %v/%v, want 1500/2000ms from submitted_at", got.ReportToAlertMs, got.ReportToDeliveryMs)
	}
	if got.CurrentBottleneck != "" {
		t.Fatalf("bottleneck = %q, want empty because only unavailable stages remain", got.CurrentBottleneck)
	}

	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"raw_text", "reporter_id", "device_location", "provider_response", "endpoint", "secret"} {
		if contains(string(encoded), forbidden) {
			t.Fatalf("diagnostic JSON contains forbidden field %q: %s", forbidden, encoded)
		}
	}
}

func TestBuildTimelineClassifiesBacklogAndFailuresWithoutZeroSuccess(t *testing.T) {
	now := time.Date(2026, 9, 26, 1, 0, 10, 0, time.UTC)
	started := now.Add(-10 * time.Second)
	got := BuildTimeline(Input{
		ReportID:          "report-2",
		Now:               now,
		ReportSubmittedAt: timePtr(now.Add(-20 * time.Second)), ReportPersistedAt: timePtr(now.Add(-19 * time.Second)),
		Outbox:   Observation{StartedAt: timePtr(started), State: StatePending},
		AI:       Observation{State: StateFailedRetryable, FailureKind: "provider_transient"},
		Incident: Observation{State: StatePending},
		Priority: Observation{State: StateUnavailable},
		Alert:    Observation{State: StatePending},
		Delivery: DeliveryObservation{Observation: Observation{FailureKind: "provider_permanent"}, QueueState: StateCompleted, AttemptState: StateFailedTerminal},
	})

	if got.OutboxPublishMs != nil || got.AIProcessingMs != nil || got.ReportToAlertMs != nil || got.ReportToDeliveryMs != nil {
		t.Fatalf("uncompleted durations should be null: %+v", got)
	}
	if got.Stage(StageOutboxPublish).State != StatePending || got.Stage(StageAIProcessing).State != StateFailedRetryable {
		t.Fatalf("stage states = %+v, want backlog/retryable", got.Stages)
	}
	if got.Stage(StageDeliveryAttempt).State != StateFailedTerminal {
		t.Fatalf("delivery state = %+v, want terminal failure", got.Stage(StageDeliveryAttempt))
	}
	if got.Stage(StageAIProcessing).DurationMs != nil {
		t.Fatalf("failed AI duration should be unavailable, got %v", got.Stage(StageAIProcessing).DurationMs)
	}
}

func TestBuildTimelineClampsOutOfOrderTimes(t *testing.T) {
	base := time.Date(2026, 9, 26, 1, 0, 0, 0, time.UTC)
	got := BuildTimeline(Input{
		ReportID:          "report-3",
		Now:               base,
		ReportSubmittedAt: timePtr(base.Add(time.Second)), ReportPersistedAt: timePtr(base),
		Outbox: Observation{StartedAt: timePtr(base.Add(3 * time.Second)), CompletedAt: timePtr(base), State: StateCompleted},
		AI:     Observation{StartedAt: timePtr(base.Add(2 * time.Second)), CompletedAt: timePtr(base), State: StateCompleted},
	})
	for _, stage := range got.Stages {
		if stage.DurationMs != nil && *stage.DurationMs < 0 {
			t.Fatalf("stage %s has negative duration %d", stage.Name, *stage.DurationMs)
		}
	}
	if got.ReportPersistMs != nil || got.OutboxPublishMs == nil || *got.OutboxPublishMs != 0 {
		t.Fatalf("out-of-order durations = %v/%v, want unavailable/0", got.ReportPersistMs, got.OutboxPublishMs)
	}
}

func TestBuildTimelineAlertAndDeliveryPendingAndFailureStates(t *testing.T) {
	now := time.Date(2026, 9, 26, 2, 0, 0, 0, time.UTC)
	reportPersisted := now.Add(-8 * time.Second)
	incidentAttached := now.Add(-6 * time.Second)
	alertCreated := now.Add(-4 * time.Second)
	deliveryCreated := now.Add(-3 * time.Second)
	attemptStarted := now.Add(-2 * time.Second)
	attemptFailed := now.Add(-time.Second)
	got := BuildTimeline(Input{
		ReportID:          "report-pending",
		Now:               now,
		ReportPersistedAt: &reportPersisted,
		Incident:          Observation{State: StateCompleted, CompletedAt: &incidentAttached},
		Priority:          Observation{State: StateUnavailable},
		Alert:             Observation{State: StateCompleted, CompletedAt: &alertCreated},
		Delivery: DeliveryObservation{
			Observation:        Observation{StartedAt: &deliveryCreated, LastActivityAt: &attemptFailed, FailureKind: "transient", Attempts: 2},
			QueueState:         StateCompleted,
			AttemptState:       StateFailedRetryable,
			AttemptStartedAt:   &attemptStarted,
			AttemptCompletedAt: &attemptFailed,
		},
	})
	if got.Stage(StageAlertCreation).State != StateCompleted || got.AlertCreationMs != nil {
		t.Fatalf("alert stage = %+v, want completed with unavailable duration", got.Stage(StageAlertCreation))
	}
	if got.Stage(StageDeliveryQueue).State != StateCompleted || got.DeliveryQueueMs == nil || *got.DeliveryQueueMs != 1000 {
		t.Fatalf("delivery queue = %+v, want completed in 1000ms", got.Stage(StageDeliveryQueue))
	}
	if got.Stage(StageDeliveryAttempt).State != StateFailedRetryable || got.DeliveryAttemptMs == nil || *got.DeliveryAttemptMs != 1000 || got.Stage(StageDeliveryAttempt).AttemptCount != 2 {
		t.Fatalf("delivery attempt = %+v, want 1s retryable failure after 2 attempts", got.Stage(StageDeliveryAttempt))
	}
	if got.CurrentBottleneck != string(StageDeliveryAttempt) {
		t.Fatalf("current bottleneck = %q, want delivery attempt", got.CurrentBottleneck)
	}

	got = BuildTimeline(Input{
		ReportID: "report-not-reached", Now: now, ReportPersistedAt: &reportPersisted,
		AI:       Observation{State: StatePending, StartedAt: &reportPersisted},
		Incident: Observation{State: StateUnavailable},
		Alert:    Observation{State: StateUnavailable},
		Delivery: DeliveryObservation{Observation: Observation{StartedAt: &deliveryCreated}, QueueState: StatePending, AttemptState: StateUnavailable},
	})
	if got.Stage(StageAIProcessing).State != StatePending || got.Stage(StageDeliveryQueue).State != StatePending || got.Stage(StageDeliveryAttempt).State != StateUnavailable {
		t.Fatalf("pending/unreached classifications = %s/%s/%s", got.Stage(StageAIProcessing).State, got.Stage(StageDeliveryQueue).State, got.Stage(StageDeliveryAttempt).State)
	}
}

func timePtr(value time.Time) *time.Time { return &value }

func contains(value, needle string) bool {
	for index := 0; index+len(needle) <= len(value); index++ {
		if value[index:index+len(needle)] == needle {
			return true
		}
	}
	return false
}
