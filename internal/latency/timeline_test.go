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
	if got.ReportPersistMs == nil || *got.ReportPersistMs != 100 {
		t.Fatalf("report persist = %v, want 100ms", got.ReportPersistMs)
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
	if got.ReportToAlertMs == nil || *got.ReportToAlertMs != 1400 || got.ReportToDeliveryMs == nil || *got.ReportToDeliveryMs != 1900 {
		t.Fatalf("end-to-end durations = %v/%v, want 1400/1900ms", got.ReportToAlertMs, got.ReportToDeliveryMs)
	}
	if got.CurrentBottleneck != string(StagePriorityEvaluation) {
		t.Fatalf("bottleneck = %q, want %q", got.CurrentBottleneck, StagePriorityEvaluation)
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
		Delivery: DeliveryObservation{Observation: Observation{State: StateFailedTerminal, FailureKind: "provider_permanent"}},
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
	if got.ReportPersistMs == nil || *got.ReportPersistMs != 0 || got.OutboxPublishMs == nil || *got.OutboxPublishMs != 0 {
		t.Fatalf("out-of-order durations = %v/%v, want 0/0", got.ReportPersistMs, got.OutboxPublishMs)
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
