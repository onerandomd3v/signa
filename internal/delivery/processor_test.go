package delivery

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onerandomd3v/signa/internal/ai/extraction"
)

func TestProcessorDeliversOnceAndReusesTerminalState(t *testing.T) {
	store := newMemoryStore(3)
	adapter := &recordingAdapter{}
	p := NewProcessor(store, adapter, time.Second)
	message := deliveryMessage("delivery-1", "alert-1", "delivery-key-1")

	first, err := p.Process(context.Background(), message)
	if err != nil || !first.Ack {
		t.Fatalf("first Process() = %+v, %v", first, err)
	}
	second, err := p.Process(context.Background(), message)
	if err != nil || !second.Ack {
		t.Fatalf("duplicate Process() = %+v, %v", second, err)
	}
	if adapter.calls != 1 {
		t.Fatalf("adapter calls = %d, want 1", adapter.calls)
	}
	if store.attempts != 1 || store.results != 1 {
		t.Fatalf("durable attempts/results = %d/%d, want 1/1", store.attempts, store.results)
	}
}

func TestProcessorRetriesTransientFailureAndEventuallySucceeds(t *testing.T) {
	store := newMemoryStore(3)
	adapter := &recordingAdapter{errors: []error{Transient(errors.New("temporary outage")), nil}}
	p := NewProcessor(store, adapter, 0)
	message := deliveryMessage("delivery-2", "alert-2", "delivery-key-2")

	first, err := p.Process(context.Background(), message)
	if err != nil || first.Ack {
		t.Fatalf("first transient Process() = %+v, %v, want unacked", first, err)
	}
	second, err := p.Process(context.Background(), message)
	if err != nil || !second.Ack {
		t.Fatalf("eventual success Process() = %+v, %v", second, err)
	}
	if adapter.calls != 2 || store.attempts != 2 || store.results != 2 {
		t.Fatalf("calls/attempts/results = %d/%d/%d, want 2/2/2", adapter.calls, store.attempts, store.results)
	}
	if len(adapter.keys) != 2 || adapter.keys[0] == "" || adapter.keys[0] != adapter.keys[1] {
		t.Fatalf("operation keys = %v, want one stable key across redelivery", adapter.keys)
	}
}

func TestProcessorQuarantinesPermanentAndExhaustedFailures(t *testing.T) {
	tests := []struct {
		name   string
		errors []error
	}{
		{name: "permanent", errors: []error{Permanent(errors.New("invalid subscription"))}},
		{name: "exhausted", errors: []error{Transient(errors.New("timeout")), Transient(errors.New("timeout"))}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newMemoryStore(2)
			adapter := &recordingAdapter{errors: tt.errors}
			p := NewProcessor(store, adapter, 0)
			message := deliveryMessage("delivery-"+tt.name, "alert-"+tt.name, "key-"+tt.name)

			for i := 0; i < len(tt.errors); i++ {
				result, err := p.Process(context.Background(), message)
				if err != nil {
					t.Fatalf("Process() error = %v", err)
				}
				if tt.name == "permanent" || i == len(tt.errors)-1 {
					if !result.Ack {
						t.Fatalf("terminal Process() = %+v, want ack", result)
					}
				} else if result.Ack {
					t.Fatalf("retry Process() = %+v, want unacked", result)
				}
			}
			if store.state != StateQuarantined {
				t.Fatalf("state = %q, want quarantined", store.state)
			}
		})
	}
}

func TestProcessorMalformedRequestRemainsPendingForInspection(t *testing.T) {
	store := newMemoryStore(3)
	adapter := &recordingAdapter{}
	p := NewProcessor(store, adapter, 0)

	result, err := p.Process(context.Background(), extraction.StreamMessage{ID: "bad", Values: map[string]any{"event_name": DeliveryRequestedV1}})
	if err != nil || result.Ack {
		t.Fatalf("malformed Process() = %+v, %v, want pending", result, err)
	}
	if adapter.calls != 0 || store.attempts != 0 {
		t.Fatalf("malformed request caused delivery work: calls=%d attempts=%d", adapter.calls, store.attempts)
	}
}

func TestProcessorSkipsSupersededAlertWithoutDelivery(t *testing.T) {
	store := newMemoryStore(3)
	store.state = StateSkipped
	adapter := &recordingAdapter{}
	p := NewProcessor(store, adapter, 0)

	result, err := p.Process(context.Background(), deliveryMessage("delivery-stale", "alert-superseded", "key-stale"))
	if err != nil || !result.Ack {
		t.Fatalf("superseded Process() = %+v, %v", result, err)
	}
	if adapter.calls != 0 {
		t.Fatalf("adapter calls = %d, want 0", adapter.calls)
	}
}

type recordingAdapter struct {
	calls  int
	errors []error
	keys   []string
}

type memoryStore struct {
	maxAttempts int
	state       State
	attempts    int
	results     int
}

func newMemoryStore(maxAttempts int) *memoryStore {
	return &memoryStore{maxAttempts: maxAttempts, state: StatePending}
}

func (s *memoryStore) StartAttempt(_ context.Context, request Request, _ time.Time) (StartResult, error) {
	if s.state == StateSucceeded || s.state == StateQuarantined || s.state == StateSkipped {
		return StartResult{Terminal: true}, nil
	}
	s.attempts++
	s.state = StateInFlight
	return StartResult{Attempt: Attempt{DeliveryID: request.DeliveryID, AlertID: request.AlertID, IdempotencyKey: request.IdempotencyKey, Attempt: s.attempts, OperationKey: "delivery/" + request.DeliveryID}}, nil
}

func (s *memoryStore) FinishAttempt(_ context.Context, _ Attempt, _ ProviderResult, kind FailureKind, _ error, _ time.Time) (FinishResult, error) {
	s.results++
	if kind == "" {
		s.state = StateSucceeded
		return FinishResult{State: s.state, Terminal: true}, nil
	}
	if kind == FailurePermanent || s.attempts >= s.maxAttempts {
		s.state = StateQuarantined
		return FinishResult{State: s.state, Terminal: true}, nil
	}
	s.state = StatePending
	return FinishResult{State: s.state, RetryAfter: 0}, nil
}

func (a *recordingAdapter) Deliver(_ context.Context, attempt Attempt) (ProviderResult, error) {
	a.calls++
	a.keys = append(a.keys, attempt.OperationKey)
	if len(a.errors) == 0 {
		return ProviderResult{Response: "accepted"}, nil
	}
	err := a.errors[0]
	a.errors = a.errors[1:]
	return ProviderResult{}, err
}

func deliveryMessage(deliveryID, alertID, key string) extraction.StreamMessage {
	return extraction.StreamMessage{ID: "stream-" + deliveryID, Values: map[string]any{
		"event_id": "event-" + deliveryID, "event_name": DeliveryRequestedV1, "aggregate_type": "delivery",
		"aggregate_id": deliveryID, "payload": `{"delivery_id":"` + deliveryID + `","alert_id":"` + alertID + `","idempotency_key":"` + key + `"}`,
	}}
}
