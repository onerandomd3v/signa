package main

import (
	"testing"
	"time"

	webpushlib "github.com/SherClockHolmes/webpush-go"
	"github.com/onerandomd3v/signa/internal/config"
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
