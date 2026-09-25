package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const DeliveryRequestedV1 = "delivery.requested.v1"

type State string

const (
	StatePending     State = "PENDING"
	StateInFlight    State = "IN_FLIGHT"
	StateSucceeded   State = "SUCCEEDED"
	StateQuarantined State = "QUARANTINED"
	StateSkipped     State = "SKIPPED"
)

type FailureKind string

const (
	FailureTransient FailureKind = "transient"
	FailurePermanent FailureKind = "permanent"
)

type DeliveryError struct {
	Kind FailureKind
	Err  error
}

func (e *DeliveryError) Error() string { return fmt.Sprintf("%s: %v", e.Kind, e.Err) }
func (e *DeliveryError) Unwrap() error { return e.Err }

func Transient(err error) error { return &DeliveryError{Kind: FailureTransient, Err: err} }
func Permanent(err error) error { return &DeliveryError{Kind: FailurePermanent, Err: err} }

func failureKind(err error) FailureKind {
	var deliveryErr *DeliveryError
	if errors.As(err, &deliveryErr) && deliveryErr.Kind == FailurePermanent {
		return FailurePermanent
	}
	return FailureTransient
}

type Request struct {
	EventID        string
	DeliveryID     string
	AlertID        string
	IdempotencyKey string
}

type Attempt struct {
	DeliveryID     string
	AlertID        string
	Channel        string
	Priority       string
	Payload        json.RawMessage
	IdempotencyKey string
	Attempt        int
	OperationKey   string
}

type ProviderResult struct{ Response string }

type StartResult struct {
	Attempt    Attempt
	Terminal   bool
	NotDue     bool
	RetryAfter time.Duration
}

type FinishResult struct {
	State      State
	Terminal   bool
	Stale      bool
	RetryAfter time.Duration
}

type Store interface {
	StartAttempt(context.Context, Request, time.Time) (StartResult, error)
	FinishAttempt(context.Context, Attempt, ProviderResult, FailureKind, error, time.Time) (FinishResult, error)
}

type Adapter interface {
	Deliver(context.Context, Attempt) (ProviderResult, error)
}

func ParseRequest(fields map[string]any) (Request, error) {
	eventID, err := stringField(fields, "event_id")
	if err != nil {
		return Request{}, err
	}
	name, err := stringField(fields, "event_name")
	if err != nil {
		return Request{}, err
	}
	if name != DeliveryRequestedV1 {
		return Request{}, fmt.Errorf("unsupported delivery event %q", name)
	}
	if aggregate, err := stringField(fields, "aggregate_type"); err != nil || aggregate != "delivery" {
		if err != nil {
			return Request{}, err
		}
		return Request{}, fmt.Errorf("unexpected aggregate type %q", aggregate)
	}
	aggregateID, err := stringField(fields, "aggregate_id")
	if err != nil {
		return Request{}, err
	}
	payload, err := stringField(fields, "payload")
	if err != nil {
		return Request{}, err
	}
	var body struct {
		DeliveryID     string `json:"delivery_id"`
		AlertID        string `json:"alert_id"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := json.Unmarshal([]byte(payload), &body); err != nil {
		return Request{}, fmt.Errorf("decode delivery request payload: %w", err)
	}
	if body.DeliveryID == "" || body.DeliveryID != aggregateID {
		return Request{}, fmt.Errorf("delivery_id must match aggregate_id")
	}
	if body.AlertID == "" || body.IdempotencyKey == "" {
		return Request{}, fmt.Errorf("delivery request identity is incomplete")
	}
	return Request{EventID: eventID, DeliveryID: body.DeliveryID, AlertID: body.AlertID, IdempotencyKey: body.IdempotencyKey}, nil
}

func stringField(fields map[string]any, name string) (string, error) {
	value, ok := fields[name]
	if !ok {
		return "", fmt.Errorf("stream message missing %s", name)
	}
	switch value := value.(type) {
	case string:
		if value == "" {
			return "", fmt.Errorf("stream message %s is empty", name)
		}
		return value, nil
	case []byte:
		if len(value) == 0 {
			return "", fmt.Errorf("stream message %s is empty", name)
		}
		return string(value), nil
	default:
		return "", fmt.Errorf("stream message %s has type %T", name, value)
	}
}
