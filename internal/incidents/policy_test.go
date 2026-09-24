package incidents

import (
	"testing"

	"github.com/onerandomd3v/signa/internal/ai/similarity"
)

func scored(id string, score float64) AssessedCandidate {
	return AssessedCandidate{ID: id, Assessment: similarity.Assessment{CandidateIncidentID: id, SimilarityState: "assessed", SimilarityScore: &score}}
}

func TestDecideAttachmentRequiresClearWinner(t *testing.T) {
	decision, err := DecideAttachment([]AssessedCandidate{scored("a", .9), scored("b", .7)}, .8, .1)
	if err != nil || decision.Decision != DecisionAttach || decision.CandidateID != "a" {
		t.Fatalf("decision = %+v, err = %v", decision, err)
	}

	decision, err = DecideAttachment([]AssessedCandidate{scored("a", .9), scored("b", .85)}, .8, .1)
	if err != nil || decision.Decision != DecisionCreate {
		t.Fatalf("insufficient margin decision = %+v, err = %v", decision, err)
	}

	decision, err = DecideAttachment([]AssessedCandidate{scored("a", .9), scored("b", .9)}, .8, 0)
	if err != nil || decision.Decision != DecisionCreate {
		t.Fatalf("tie decision = %+v, err = %v", decision, err)
	}
}

func TestDecideAttachmentDoesNotUseUnscoredCandidate(t *testing.T) {
	decision, err := DecideAttachment([]AssessedCandidate{{ID: "unknown", Assessment: similarity.Assessment{SimilarityState: "insufficient_evidence"}}, scored("a", .9)}, .8, .1)
	if err != nil || decision.Decision != DecisionAttach || decision.CandidateID != "a" {
		t.Fatalf("decision = %+v, err = %v", decision, err)
	}
}
