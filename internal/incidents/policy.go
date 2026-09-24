package incidents

import (
	"fmt"
	"math"

	"github.com/onerandomd3v/signa/internal/ai/similarity"
)

const AttachmentPolicyVersion = "signa.incident-attachment.v1"

type Decision string

const (
	DecisionCreate Decision = "CREATE"
	DecisionAttach Decision = "ATTACH"
)

type AssessedCandidate struct {
	ID         string
	Assessment similarity.Assessment
}

type PolicyDecision struct {
	Decision     Decision
	CandidateID  string
	Score        *float64
	WinnerMargin *float64
	Reason       string
}

func DecideAttachment(candidates []AssessedCandidate, threshold, margin float64) (PolicyDecision, error) {
	if !finiteRange(threshold) || !finiteRange(margin) {
		return PolicyDecision{}, fmt.Errorf("attachment policy thresholds must be finite and within 0..1")
	}
	valid := make([]AssessedCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.ID == "" || candidate.Assessment.SimilarityState != "assessed" || candidate.Assessment.SimilarityScore == nil {
			continue
		}
		if !finiteRange(*candidate.Assessment.SimilarityScore) {
			continue
		}
		valid = append(valid, candidate)
	}
	if len(valid) == 0 {
		return PolicyDecision{Decision: DecisionCreate, Reason: "no candidate had an assessed similarity score"}, nil
	}
	for i := 1; i < len(valid); i++ {
		for j := i; j > 0 && *valid[j].Assessment.SimilarityScore > *valid[j-1].Assessment.SimilarityScore; j-- {
			valid[j], valid[j-1] = valid[j-1], valid[j]
		}
	}
	best := valid[0]
	if *best.Assessment.SimilarityScore < threshold {
		return PolicyDecision{Decision: DecisionCreate, Score: best.Assessment.SimilarityScore, Reason: "best similarity score is below threshold"}, nil
	}
	if len(valid) > 1 {
		value := *best.Assessment.SimilarityScore - *valid[1].Assessment.SimilarityScore
		if value <= 0 || value < margin {
			return PolicyDecision{Decision: DecisionCreate, Score: best.Assessment.SimilarityScore, WinnerMargin: &value, Reason: "best candidate does not clear the winner margin"}, nil
		}
		return PolicyDecision{Decision: DecisionAttach, CandidateID: best.ID, Score: best.Assessment.SimilarityScore, WinnerMargin: &value, Reason: "best candidate clears threshold and winner margin"}, nil
	}
	return PolicyDecision{Decision: DecisionAttach, CandidateID: best.ID, Score: best.Assessment.SimilarityScore, Reason: "only scored candidate clears threshold"}, nil
}

func finiteRange(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 1
}
