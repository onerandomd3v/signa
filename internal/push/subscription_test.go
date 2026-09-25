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

func TestValidateSubscriptionRejectsProhibitedLiteralIPDestinations(t *testing.T) {
	for _, endpoint := range []string{
		"https://127.0.0.1/send",
		"https://10.0.0.1/send",
		"https://169.254.1.1/send",
		"https://[::1]/send",
		"https://[fc00::1]/send",
		"https://[fe80::1]/send",
	} {
		t.Run(endpoint, func(t *testing.T) {
			if err := ValidateSubscription(SubscriptionInput{Endpoint: endpoint, P256DH: validP256DH(), Auth: "AAAAAAAAAAAAAAAAAAAAAA"}); err == nil {
				t.Fatal("ValidateSubscription() error = nil, want prohibited destination rejection")
			}
		})
	}
}

func TestValidateSubscriptionAcceptsPublicLiteralIPDestination(t *testing.T) {
	if err := ValidateSubscription(SubscriptionInput{Endpoint: "https://1.1.1.1/send", P256DH: validP256DH(), Auth: "AAAAAAAAAAAAAAAAAAAAAA"}); err != nil {
		t.Fatalf("ValidateSubscription() error = %v, want public destination accepted", err)
	}
}

func validP256DH() string {
	return base64.RawURLEncoding.EncodeToString(append([]byte{4}, make([]byte, 64)...))
}
