package main

import (
	"context"
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

func TestIncidentConsumersUseIndependentGroupsWhenTargetingEnabled(t *testing.T) {
	client := &incidentConsumerGroupTestClient{}
	lifecycleProcessor := incidentEventProcessorFunc(func(context.Context, incidents.StreamMessage) error { return nil })
	targetingProcessor := incidentEventProcessorFunc(func(context.Context, incidents.StreamMessage) error { return nil })
	lifecycle, targeting := newIncidentEventConsumers(client, lifecycleProcessor, targetingProcessor, "worker-1", time.Second, nil)
	if targeting == nil {
		t.Fatal("targeting consumer is nil while targeting is enabled")
	}

	runAndCaptureGroup(t, client, func(ctx context.Context) error { return lifecycle.Run(ctx) })
	runAndCaptureGroup(t, client, func(ctx context.Context) error { return targeting.Run(ctx) })
	if len(client.groups) != 2 {
		t.Fatalf("consumer groups = %v", client.groups)
	}
	if client.groups[0] != "signa-incident-lifecycle" {
		t.Fatalf("lifecycle group = %q, want signa-incident-lifecycle", client.groups[0])
	}
	if client.groups[1] != "signa-verification-targeting" {
		t.Fatalf("targeting group = %q, want signa-verification-targeting", client.groups[1])
	}
	if client.groups[0] == client.groups[1] {
		t.Fatalf("lifecycle and targeting groups must differ: %v", client.groups)
	}
}

func TestEnablingTargetingDoesNotChangeLifecycleConsumerGroup(t *testing.T) {
	processor := incidentEventProcessorFunc(func(context.Context, incidents.StreamMessage) error { return nil })
	disabledClient := &incidentConsumerGroupTestClient{}
	disabledLifecycle, disabledTargeting := newIncidentEventConsumers(disabledClient, processor, nil, "worker-1", time.Second, nil)
	if disabledTargeting != nil {
		t.Fatal("targeting consumer should be absent when disabled")
	}
	runAndCaptureGroup(t, disabledClient, func(ctx context.Context) error { return disabledLifecycle.Run(ctx) })

	enabledClient := &incidentConsumerGroupTestClient{}
	enabledLifecycle, enabledTargeting := newIncidentEventConsumers(enabledClient, processor, processor, "worker-1", time.Second, nil)
	if enabledTargeting == nil {
		t.Fatal("targeting consumer should exist when enabled")
	}
	runAndCaptureGroup(t, enabledClient, func(ctx context.Context) error { return enabledLifecycle.Run(ctx) })

	if disabledClient.groups[0] != "signa-incident-lifecycle" || enabledClient.groups[0] != disabledClient.groups[0] {
		t.Fatalf("lifecycle group changed: disabled=%v enabled=%v", disabledClient.groups, enabledClient.groups)
	}
}

func runAndCaptureGroup(t *testing.T, client *incidentConsumerGroupTestClient, run func(context.Context) error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	client.cancel = cancel
	defer cancel()
	if err := run(ctx); err != nil {
		t.Fatal(err)
	}
}

type incidentConsumerGroupTestClient struct {
	groups []string
	cancel context.CancelFunc
}

func (c *incidentConsumerGroupTestClient) EnsureGroup(_ context.Context, _, group string) error {
	c.groups = append(c.groups, group)
	return nil
}
func (c *incidentConsumerGroupTestClient) Read(ctx context.Context, _, _, _ string, pending bool, _ time.Duration) ([]incidents.StreamMessage, error) {
	if pending {
		return nil, nil
	}
	c.cancel()
	return nil, ctx.Err()
}
func (c *incidentConsumerGroupTestClient) Ack(context.Context, string, string, string) error {
	return nil
}
