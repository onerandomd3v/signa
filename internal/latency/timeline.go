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

var orderedStages = []Stage{
	StageReportPersist,
	StageOutboxPublish,
	StageAIProcessing,
	StageIncidentProcessing,
	StagePriorityEvaluation,
	StageAlertCreation,
	StageDeliveryQueue,
	StageDeliveryAttempt,
}

type Observation struct {
	State          StageState
	StartedAt      *time.Time
	CompletedAt    *time.Time
	LastActivityAt *time.Time
	FailureKind    string
}

type DeliveryObservation struct {
	Observation
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
	Name        Stage      `json:"stage"`
	State       StageState `json:"state"`
	StartedAt   *time.Time `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at"`
	DurationMs  *int64     `json:"duration_ms"`
	AgeMs       *int64     `json:"age_ms"`
	FailureKind string     `json:"failure_kind,omitempty"`
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
		newStage(StageReportPersist, observationForReport(input.ReportSubmittedAt, input.ReportPersistedAt), input.Now),
		newStage(StageOutboxPublish, input.Outbox, input.Now),
		newStage(StageAIProcessing, input.AI, input.Now),
		newStage(StageIncidentProcessing, input.Incident, input.Now),
		newStage(StagePriorityEvaluation, input.Priority, input.Now),
		newStage(StageAlertCreation, input.Alert, input.Now),
		newStage(StageDeliveryQueue, Observation{State: input.Delivery.State, StartedAt: input.Delivery.StartedAt, CompletedAt: input.Delivery.AttemptStartedAt, LastActivityAt: input.Delivery.LastActivityAt, FailureKind: input.Delivery.FailureKind}, input.Now),
		newStage(StageDeliveryAttempt, Observation{State: input.Delivery.State, StartedAt: input.Delivery.AttemptStartedAt, CompletedAt: input.Delivery.AttemptCompletedAt, LastActivityAt: input.Delivery.LastActivityAt, FailureKind: input.Delivery.FailureKind}, input.Now),
	}

	timeline := Timeline{
		ReportID: input.ReportID, IncidentID: input.IncidentID, AlertID: input.AlertID, DeliveryID: input.DeliveryID,
		ReportPersistMs:      durationMilliseconds(input.ReportSubmittedAt, input.ReportPersistedAt),
		OutboxPublishMs:      durationMilliseconds(input.Outbox.StartedAt, input.Outbox.CompletedAt),
		AIProcessingMs:       durationMilliseconds(input.AI.StartedAt, input.AI.CompletedAt),
		IncidentProcessingMs: durationMilliseconds(input.Incident.StartedAt, input.Incident.CompletedAt),
		PriorityEvaluationMs: durationMilliseconds(input.Priority.StartedAt, input.Priority.CompletedAt),
		AlertCreationMs:      durationMilliseconds(input.Alert.StartedAt, input.Alert.CompletedAt),
		DeliveryQueueMs:      durationMilliseconds(input.Delivery.StartedAt, input.Delivery.AttemptStartedAt),
		DeliveryAttemptMs:    durationMilliseconds(input.Delivery.AttemptStartedAt, input.Delivery.AttemptCompletedAt),
		ReportToAlertMs:      durationMilliseconds(input.ReportPersistedAt, input.Alert.CompletedAt),
		ReportToDeliveryMs:   durationMilliseconds(input.ReportPersistedAt, input.Delivery.DeliveredAt),
		Stages:               stages,
	}
	for _, stage := range stages {
		if stage.State != StateCompleted {
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

func observationForReport(submittedAt, persistedAt *time.Time) Observation {
	if persistedAt == nil {
		return Observation{State: StateUnavailable}
	}
	return Observation{State: StateCompleted, StartedAt: submittedAt, CompletedAt: persistedAt}
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
	if state != StateCompleted {
		age = durationMilliseconds(observation.StartedAt, &now)
		if observation.StartedAt == nil {
			age = durationMilliseconds(observation.LastActivityAt, &now)
		}
	}
	var duration *int64
	if state == StateCompleted {
		duration = durationMilliseconds(observation.StartedAt, observation.CompletedAt)
	}
	return StageTiming{Name: name, State: state, StartedAt: observation.StartedAt, CompletedAt: observation.CompletedAt, DurationMs: duration, AgeMs: age, FailureKind: observation.FailureKind}
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
