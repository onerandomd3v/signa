// Package latency contains the privacy-safe report-to-alert latency read model.
package latency

import "time"

type Stage string

const (
	StageReportPersist      Stage = "report_persist"
	StageOutboxPublish      Stage = "outbox_publish"
	StageAIProcessing       Stage = "ai_processing"
	StageIncidentProcessing Stage = "incident_processing"
	StagePriorityEvaluation Stage = "priority_evaluation"
	StageAlertCreation      Stage = "alert_creation"
	StageDeliveryQueue      Stage = "delivery_queue"
	StageDeliveryAttempt    Stage = "delivery_attempt"
)

type StageState string

const (
	StateCompleted       StageState = "completed"
	StatePending         StageState = "pending/backlogged"
	StateFailedRetryable StageState = "failed/retryable"
	StateFailedTerminal  StageState = "failed/terminal"
	StateUnavailable     StageState = "unavailable/not-yet-measurable"
)

type Observation struct {
	State          StageState
	StartedAt      *time.Time
	CompletedAt    *time.Time
	LastActivityAt *time.Time
	FailureKind    string
	Attempts       int
}

type DeliveryObservation struct {
	Observation
	QueueState         StageState
	AttemptState       StageState
	AttemptStartedAt   *time.Time
	AttemptCompletedAt *time.Time
	DeliveredAt        *time.Time
}

type Input struct {
	ReportID          string
	IncidentID        *string
	AlertID           *string
	DeliveryID        *string
	Now               time.Time
	ReportSubmittedAt *time.Time
	ReportPersistedAt *time.Time
	Outbox            Observation
	AI                Observation
	Incident          Observation
	Priority          Observation
	Alert             Observation
	Delivery          DeliveryObservation
}

type StageTiming struct {
	Name         Stage      `json:"stage"`
	State        StageState `json:"state"`
	StartedAt    *time.Time `json:"started_at"`
	CompletedAt  *time.Time `json:"completed_at"`
	DurationMs   *int64     `json:"duration_ms"`
	AgeMs        *int64     `json:"age_ms"`
	FailureKind  string     `json:"failure_kind,omitempty"`
	AttemptCount int        `json:"attempt_count,omitempty"`
}

type Timeline struct {
	ReportID   string  `json:"report_id"`
	IncidentID *string `json:"incident_id,omitempty"`
	AlertID    *string `json:"alert_id,omitempty"`
	DeliveryID *string `json:"delivery_id,omitempty"`

	ReportPersistMs      *int64        `json:"report_persist_ms"`
	OutboxPublishMs      *int64        `json:"outbox_publish_ms"`
	AIProcessingMs       *int64        `json:"ai_processing_ms"`
	IncidentProcessingMs *int64        `json:"incident_processing_ms"`
	PriorityEvaluationMs *int64        `json:"priority_evaluation_ms"`
	AlertCreationMs      *int64        `json:"alert_creation_ms"`
	DeliveryQueueMs      *int64        `json:"delivery_queue_ms"`
	DeliveryAttemptMs    *int64        `json:"delivery_attempt_ms"`
	ReportToAlertMs      *int64        `json:"report_to_alert_ms"`
	ReportToDeliveryMs   *int64        `json:"report_to_delivery_ms"`
	CurrentBottleneck    string        `json:"current_bottleneck"`
	Stages               []StageTiming `json:"stages"`
}

func BuildTimeline(input Input) Timeline {
	stages := []StageTiming{
		newStage(StageReportPersist, observationForReport(input.ReportPersistedAt), input.Now),
		newStage(StageOutboxPublish, input.Outbox, input.Now),
		newStage(StageAIProcessing, input.AI, input.Now),
		newStage(StageIncidentProcessing, input.Incident, input.Now),
		newStage(StagePriorityEvaluation, input.Priority, input.Now),
		newStage(StageAlertCreation, input.Alert, input.Now),
		newStage(StageDeliveryQueue, Observation{State: input.Delivery.QueueState, StartedAt: input.Delivery.StartedAt, CompletedAt: input.Delivery.AttemptStartedAt, LastActivityAt: input.Delivery.LastActivityAt, FailureKind: input.Delivery.FailureKind}, input.Now),
		newStage(StageDeliveryAttempt, Observation{State: input.Delivery.AttemptState, StartedAt: input.Delivery.AttemptStartedAt, CompletedAt: input.Delivery.AttemptCompletedAt, LastActivityAt: input.Delivery.LastActivityAt, FailureKind: input.Delivery.FailureKind, Attempts: input.Delivery.Attempts}, input.Now),
	}

	timeline := Timeline{
		ReportID: input.ReportID, IncidentID: input.IncidentID, AlertID: input.AlertID, DeliveryID: input.DeliveryID,
		// The current report table has no request-start timestamp. Do not infer
		// API acknowledgement time from two database defaults written together.
		ReportPersistMs:      nil,
		OutboxPublishMs:      durationMilliseconds(input.Outbox.StartedAt, input.Outbox.CompletedAt),
		AIProcessingMs:       durationMilliseconds(input.AI.StartedAt, input.AI.CompletedAt),
		IncidentProcessingMs: durationMilliseconds(input.Incident.StartedAt, input.Incident.CompletedAt),
		PriorityEvaluationMs: durationMilliseconds(input.Priority.StartedAt, input.Priority.CompletedAt),
		AlertCreationMs:      durationMilliseconds(input.Alert.StartedAt, input.Alert.CompletedAt),
		DeliveryQueueMs:      durationMilliseconds(input.Delivery.StartedAt, input.Delivery.AttemptStartedAt),
		DeliveryAttemptMs:    durationMilliseconds(input.Delivery.AttemptStartedAt, input.Delivery.AttemptCompletedAt),
		ReportToAlertMs:      durationMilliseconds(input.ReportSubmittedAt, input.Alert.CompletedAt),
		ReportToDeliveryMs:   durationMilliseconds(input.ReportSubmittedAt, input.Delivery.DeliveredAt),
		Stages:               stages,
	}
	for _, stage := range stages {
		if stage.State == StatePending || stage.State == StateFailedRetryable || stage.State == StateFailedTerminal {
			timeline.CurrentBottleneck = string(stage.Name)
			break
		}
	}
	return timeline
}

func (timeline Timeline) Stage(name Stage) StageTiming {
	for _, stage := range timeline.Stages {
		if stage.Name == name {
			return stage
		}
	}
	return StageTiming{Name: name, State: StateUnavailable}
}

func observationForReport(persistedAt *time.Time) Observation {
	if persistedAt == nil {
		return Observation{State: StateUnavailable}
	}
	return Observation{State: StateCompleted, CompletedAt: persistedAt}
}

func newStage(name Stage, observation Observation, now time.Time) StageTiming {
	state := observation.State
	if state == "" {
		switch {
		case observation.CompletedAt != nil:
			state = StateCompleted
		case observation.StartedAt != nil:
			state = StatePending
		default:
			state = StateUnavailable
		}
	}
	var age *int64
	if state == StatePending {
		age = durationMilliseconds(observation.StartedAt, &now)
		if observation.StartedAt == nil {
			age = durationMilliseconds(observation.LastActivityAt, &now)
		}
	}
	var duration *int64
	if observation.CompletedAt != nil && (state == StateCompleted || state == StateFailedRetryable || state == StateFailedTerminal) {
		duration = durationMilliseconds(observation.StartedAt, observation.CompletedAt)
	}
	return StageTiming{Name: name, State: state, StartedAt: observation.StartedAt, CompletedAt: observation.CompletedAt, DurationMs: duration, AgeMs: age, FailureKind: observation.FailureKind, AttemptCount: observation.Attempts}
}

func durationMilliseconds(start, end *time.Time) *int64 {
	if start == nil || end == nil {
		return nil
	}
	duration := end.Sub(*start)
	if duration < 0 {
		duration = 0
	}
	value := duration.Milliseconds()
	if value < 0 {
		value = 0
	}
	return &value
}
