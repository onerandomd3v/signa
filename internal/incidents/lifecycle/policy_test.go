package lifecycle

import (
	"testing"
	"time"
)

func TestEvaluateLifecycleBoundaries(t *testing.T) {
	base := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	policy := Policy{ResolvingAfter: time.Hour, ResolvedAfter: 2 * time.Hour, ExpiredAfter: 3 * time.Hour}
	tests := []struct {
		name string
		now  time.Time
		want string
	}{
		{"fresh", base.Add(59 * time.Minute), Open},
		{"resolving boundary", base.Add(time.Hour), Resolving},
		{"resolved boundary", base.Add(2 * time.Hour), Resolved},
		{"expired boundary", base.Add(3 * time.Hour), Expired},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Evaluate(test.now, Incident{Status: Open, ActivityAt: base}, policy)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != test.want {
				t.Fatalf("status = %q, want %q", got.Status, test.want)
			}
		})
	}
}

func TestEvaluateFutureActivityDoesNotDecay(t *testing.T) {
	base := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	got, err := Evaluate(base, Incident{Status: Resolving, ActivityAt: base.Add(time.Hour)}, Policy{time.Hour, 2 * time.Hour, 3 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != Open || got.Age != 0 {
		t.Fatalf("decision = %+v", got)
	}
}

func TestEvaluateTerminalIncidentsDoNotReopen(t *testing.T) {
	base := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	policy := Policy{time.Hour, 2 * time.Hour, 3 * time.Hour}
	resolvedAt := base.Add(2 * time.Hour)
	for _, status := range []string{Resolved, Expired} {
		got, err := Evaluate(base.Add(30*time.Minute), Incident{Status: status, ActivityAt: base, ResolvedAt: &resolvedAt}, policy)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != status {
			t.Fatalf("status %q reopened as %q", status, got.Status)
		}
	}
}

func TestEvaluateResolvedThenExpiredPreservesResolvedAt(t *testing.T) {
	base := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	resolvedAt := base.Add(2 * time.Hour)
	got, err := Evaluate(base.Add(3*time.Hour), Incident{Status: Resolved, ActivityAt: base, ResolvedAt: &resolvedAt}, Policy{time.Hour, 2 * time.Hour, 3 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != Expired || got.ResolvedAt == nil || !got.ResolvedAt.Equal(resolvedAt) {
		t.Fatalf("decision = %+v", got)
	}
}

func TestPolicyRejectsInvalidThresholds(t *testing.T) {
	if _, err := Evaluate(time.Now(), Incident{Status: Open, ActivityAt: time.Now()}, Policy{time.Hour, time.Hour, 2 * time.Hour}); err == nil {
		t.Fatal("expected invalid threshold error")
	}
}
