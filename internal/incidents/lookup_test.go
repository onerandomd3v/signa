package incidents

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestValidateCandidateLookup(t *testing.T) {
	valid := CandidateLookup{
		EventType:    " possible_gunfire ",
		Latitude:     6.5244,
		Longitude:    3.3792,
		RadiusMeters: 1000,
		AsOf:         time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC),
		TimeWindow:   time.Hour,
		Limit:        10,
	}
	if got, err := validateCandidateLookup(valid); err != nil {
		t.Fatalf("valid lookup error = %v", err)
	} else if got.eventType != "possible_gunfire" {
		t.Fatalf("trimmed event type = %q, want possible_gunfire", got.eventType)
	}

	tests := []struct {
		name   string
		mutate func(*CandidateLookup)
	}{
		{"empty event type", func(input *CandidateLookup) { input.EventType = "   " }},
		{"latitude below range", func(input *CandidateLookup) { input.Latitude = -90.1 }},
		{"latitude above range", func(input *CandidateLookup) { input.Latitude = 90.1 }},
		{"longitude below range", func(input *CandidateLookup) { input.Longitude = -180.1 }},
		{"longitude above range", func(input *CandidateLookup) { input.Longitude = 180.1 }},
		{"nan latitude", func(input *CandidateLookup) { input.Latitude = math.NaN() }},
		{"infinite longitude", func(input *CandidateLookup) { input.Longitude = math.Inf(1) }},
		{"zero radius", func(input *CandidateLookup) { input.RadiusMeters = 0 }},
		{"negative radius", func(input *CandidateLookup) { input.RadiusMeters = -1 }},
		{"infinite radius", func(input *CandidateLookup) { input.RadiusMeters = math.Inf(1) }},
		{"zero as of", func(input *CandidateLookup) { input.AsOf = time.Time{} }},
		{"zero time window", func(input *CandidateLookup) { input.TimeWindow = 0 }},
		{"negative time window", func(input *CandidateLookup) { input.TimeWindow = -time.Second }},
		{"zero limit", func(input *CandidateLookup) { input.Limit = 0 }},
		{"limit above maximum", func(input *CandidateLookup) { input.Limit = 101 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := valid
			test.mutate(&input)
			if _, err := validateCandidateLookup(input); !errors.Is(err, ErrInvalidLookup) {
				t.Fatalf("validateCandidateLookup() error = %v, want ErrInvalidLookup", err)
			}
		})
	}
}
