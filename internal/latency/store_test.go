package latency

import (
	"testing"
)

func TestValidateSampleLimitIsBounded(t *testing.T) {
	for _, limit := range []int{1, 50, 100} {
		if err := validateSampleLimit(limit); err != nil {
			t.Errorf("validateSampleLimit(%d) = %v, want nil", limit, err)
		}
	}
	for _, limit := range []int{-1, 0, 101, 10000} {
		if err := validateSampleLimit(limit); err == nil {
			t.Errorf("validateSampleLimit(%d) = nil, want error", limit)
		}
	}
}

func TestSafeFailureKindRedactsUnknownDatabaseValues(t *testing.T) {
	for input, want := range map[string]string{
		"transient":                   "transient",
		"permanent":                   "permanent",
		"private provider response":   "unknown",
		"https://push.example/secret": "unknown",
	} {
		if got := safeFailureKind(input); got != want {
			t.Errorf("safeFailureKind(%q) = %q, want %q", input, got, want)
		}
	}
}
