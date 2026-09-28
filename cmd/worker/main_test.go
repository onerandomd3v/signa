package main

import (
	"context"
	"errors"
	"testing"
	"time"

	webpushlib "github.com/SherClockHolmes/webpush-go"
	"github.com/onerandomd3v/signa/internal/config"
	"github.com/onerandomd3v/signa/internal/incidents"
)

func TestNewWebPushDeliveryConsumerLeavesConsumptionDisabledWithoutProviderConfig(t *testing.T) {
	for _, name := range []string{"SIGNA_WEB_PUSH_VAPID_SUBJECT", "SIGNA_WEB_PUSH_VAPID_PUBLIC_KEY", "SIGNA_WEB_PUSH_VAPID_PRIVATE_KEY"} {
		t.Setenv(name, "")
	}

	consumer, err := newWebPushDeliveryConsumer(nil, nil, nil, config.Config{DeliveryPollInterval: time.Second}, nil)
	if consumer != nil || err == nil {
		t.Fatalf("consumer/error = %v/%v, want disabled consumer and configuration error", consumer, err)
	}
}

func TestNewWebPushDeliveryConsumerWiresConfiguredProvider(t *testing.T) {
	privateKey, publicKey, err := webpushlib.GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SIGNA_WEB_PUSH_VAPID_SUBJECT", "mailto:alerts@example.test")
	t.Setenv("SIGNA_WEB_PUSH_VAPID_PUBLIC_KEY", publicKey)
	t.Setenv("SIGNA_WEB_PUSH_VAPID_PRIVATE_KEY", privateKey)
	t.Setenv("SIGNA_WEB_PUSH_TTL", "5m")
	t.Setenv("SIGNA_WEB_PUSH_TIMEOUT", "10s")

	consumer, err := newWebPushDeliveryConsumer(nil, nil, nil, config.Config{DeliveryPollInterval: time.Second, DeliveryConsumerGroup: "delivery-test", DeliveryRetryBackoff: time.Second}, nil)
	if err != nil || consumer == nil {
		t.Fatalf("consumer/error = %v/%v, want configured consumer", consumer, err)
	}
}

type incidentEventProcessorFunc func(context.Context, incidents.StreamMessage) error

func (f incidentEventProcessorFunc) Process(ctx context.Context, message incidents.StreamMessage) error {
	return f(ctx, message)
}

func TestIncidentLifecycleTargetingProcessorWaitsForDurableLifecycleSuccess(t *testing.T) {
	want := errors.New("lifecycle transaction failed")
	targetingCalls := 0
	processor := incidentLifecycleTargetingProcessor{
		lifecycle: incidentEventProcessorFunc(func(context.Context, incidents.StreamMessage) error { return want }),
		targeting: incidentEventProcessorFunc(func(context.Context, incidents.StreamMessage) error {
			targetingCalls++
			return nil
		}),
	}
	if err := processor.Process(context.Background(), incidents.StreamMessage{}); !errors.Is(err, want) {
		t.Fatalf("Process() error=%v", err)
	}
	if targetingCalls != 0 {
		t.Fatalf("targeting called %d times after lifecycle failure", targetingCalls)
	}
}

func TestIncidentLifecycleTargetingProcessorReturnsTargetingFailureForRetry(t *testing.T) {
	want := errors.New("targeting transaction failed")
	processor := incidentLifecycleTargetingProcessor{
		lifecycle: incidentEventProcessorFunc(func(context.Context, incidents.StreamMessage) error { return nil }),
		targeting: incidentEventProcessorFunc(func(context.Context, incidents.StreamMessage) error { return want }),
	}
	if err := processor.Process(context.Background(), incidents.StreamMessage{}); !errors.Is(err, want) {
		t.Fatalf("Process() error=%v", err)
	}
}
