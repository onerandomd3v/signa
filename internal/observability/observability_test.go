package observability

import (
	"context"
	"testing"
)

func TestStartStageWithoutConfiguredProviderPreservesContextAndReturns(t *testing.T) {
	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "preserved")
	got, finish := StartStage(ctx, StageReportPersist)
	if got == nil {
		t.Fatal("StartStage returned nil context")
	}
	if got.Value(contextKey{}) != ctx.Value(contextKey{}) {
		t.Fatal("StartStage did not preserve context values")
	}
	finish(nil)
	finish(context.Canceled)
}
