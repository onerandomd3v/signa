package verification

import (
	"crypto/sha256"
	"errors"
	"testing"
)

func TestFingerprintResponseIsVersionedAndDeterministic(t *testing.T) {
	conclusion := ConclusionConfirm
	observation := ObservationSaw
	input := ResponseInput{Conclusion: &conclusion, Observation: &observation}
	version, digest, err := fingerprintResponse(input)
	if err != nil {
		t.Fatal(err)
	}
	if version != 1 {
		t.Fatalf("version = %d", version)
	}
	want := sha256.Sum256([]byte(`{"version":1,"conclusion":"CONFIRM","observation":"SAW"}`))
	if digest != want {
		t.Fatalf("digest = %x, want %x", digest, want)
	}
	_, again, err := fingerprintResponse(input)
	if err != nil || again != digest {
		t.Fatalf("non-deterministic fingerprint: %x %v", again, err)
	}
}

func TestFingerprintDistinguishesNullableDimensions(t *testing.T) {
	conclusion := ConclusionConfirm
	observation := ObservationSaw
	_, a, err := fingerprintResponse(ResponseInput{Conclusion: &conclusion})
	if err != nil {
		t.Fatal(err)
	}
	wantA := sha256.Sum256([]byte(`{"version":1,"conclusion":"CONFIRM","observation":null}`))
	if a != wantA {
		t.Fatalf("nullable conclusion fingerprint = %x", a)
	}
	_, b, err := fingerprintResponse(ResponseInput{Observation: &observation})
	if err != nil {
		t.Fatal(err)
	}
	wantB := sha256.Sum256([]byte(`{"version":1,"conclusion":null,"observation":"SAW"}`))
	if b != wantB || a == b {
		t.Fatalf("nullable dimensions collapsed: %x %x", a, b)
	}
	_, _, err = fingerprintResponse(ResponseInput{})
	if !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("empty response error = %v", err)
	}
}

func TestFingerprintRejectsUnknownEnums(t *testing.T) {
	for _, input := range []ResponseInput{
		{Conclusion: pointerConclusion("confirm")},
		{Observation: pointerObservation("SEEN")},
	} {
		if _, _, err := fingerprintResponse(input); !errors.Is(err, ErrInvalidResponse) {
			t.Fatalf("input=%+v error=%v", input, err)
		}
	}
}

func pointerConclusion(value Conclusion) *Conclusion    { return &value }
func pointerObservation(value Observation) *Observation { return &value }
