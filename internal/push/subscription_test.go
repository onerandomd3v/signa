package push

import (
	"encoding/base64"
	"testing"
)

func TestValidateSubscriptionAcceptsBrowserSubscription(t *testing.T) {
	input := SubscriptionInput{
		Endpoint: "https://push.example.test/send/token",
		P256DH:   validP256DH(),
		Auth:     "AAAAAAAAAAAAAAAAAAAAAA",
	}

	if err := ValidateSubscription(input); err != nil {
		t.Fatalf("ValidateSubscription() error = %v", err)
	}
}

func TestValidateSubscriptionRejectsMalformedProtocolFields(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input SubscriptionInput
	}{
		{name: "non https endpoint", input: SubscriptionInput{Endpoint: "http://push.example.test/send", P256DH: validP256DH(), Auth: "AAAAAAAAAAAAAAAAAAAAAA"}},
		{name: "invalid public key", input: SubscriptionInput{Endpoint: "https://push.example.test/send", P256DH: "not-a-key", Auth: "AAAAAAAAAAAAAAAAAAAAAA"}},
		{name: "invalid auth key", input: SubscriptionInput{Endpoint: "https://push.example.test/send", P256DH: validP256DH(), Auth: "not-an-auth-key"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateSubscription(tc.input); err == nil {
				t.Fatal("ValidateSubscription() error = nil")
			}
		})
	}
}

func validP256DH() string {
	return base64.RawURLEncoding.EncodeToString(append([]byte{4}, make([]byte, 64)...))
}
