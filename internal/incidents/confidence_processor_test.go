package incidents

import (
	"context"
	"testing"

	"github.com/onerandomd3v/signa/internal/ai/independence"
)

func TestCompareToPriorEvidenceUsesReporterOrigin(t *testing.T) {
	left, right := "reporter-a", "reporter-b"
	got := compareToPriorEvidence(context.Background(), policyEvidenceRow{ID: "right", RawText: "a distinct report", ReporterID: &right}, []independence.Observation{{EvidenceID: "left", Text: "the first report", SourceOrigin: &left}})
	if got != "independence_supported" {
		t.Fatalf("independence state = %q", got)
	}
}
