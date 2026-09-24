// Package independence produces explainable evidence-repetition signals.
// These signals do not establish truth, confidence, or report attachment.
package independence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode"

	"github.com/onerandomd3v/signa/internal/ai/extraction"
)

const (
	ContractVersion      = "signa.ai.evidence-independence.v1"
	InputContractVersion = "signa.ai.report-extraction.v0"
	SimilarityMethod     = "word_set_jaccard.v1"
	NearDuplicateCutoff  = 0.8
)

// Observation contains only evidence supplied to the evaluator. SourceOrigin
// and media fingerprints are opaque, internal comparison tokens and are never
// copied into the public assessment.
type Observation struct {
	EvidenceID        string           `json:"evidence_id"`
	Text              string           `json:"text"`
	SourceClaim       extraction.Field `json:"source_claim"`
	SourceOrigin      *string          `json:"source_origin,omitempty"`
	MediaFingerprints []string         `json:"media_fingerprints,omitempty"`
}

type Input struct {
	Target  Observation `json:"target"`
	Related Observation `json:"related"`
}

type Assessment struct {
	ContractVersion      string   `json:"contract_version"`
	InputContractVersion string   `json:"input_contract_version"`
	IndependenceState    string   `json:"independence_state"`
	SimilarityMethod     string   `json:"similarity_method"`
	NearDuplicateCutoff  float64  `json:"near_duplicate_cutoff"`
	Factors              []Factor `json:"factors"`
	SupportingReasons    []string `json:"supporting_reasons"`
	LimitingReasons      []string `json:"limiting_reasons"`
}

type Factor struct {
	Name    string   `json:"name"`
	Outcome string   `json:"outcome"`
	Score   *float64 `json:"score"`
	Reason  string   `json:"reason"`
}

type Provider interface {
	Assess(context.Context, Input) ([]byte, error)
}

type ProviderFunc func(context.Context, Input) ([]byte, error)

func (f ProviderFunc) Assess(ctx context.Context, input Input) ([]byte, error) {
	if f == nil {
		return nil, errors.New("independence provider function is nil")
	}
	return f(ctx, input)
}

type RuleBasedEvaluator struct{}

func (RuleBasedEvaluator) Assess(ctx context.Context, input Input) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(input.Target.EvidenceID) == "" || strings.TrimSpace(input.Related.EvidenceID) == "" || input.Target.EvidenceID == input.Related.EvidenceID {
		return nil, errors.New("distinct target and related evidence ids are required")
	}
	factors := []Factor{
		compareText(input.Target.Text, input.Related.Text),
		compareMedia(input.Target.MediaFingerprints, input.Related.MediaFingerprints),
		compareSourceOrigin(input.Target.SourceOrigin, input.Related.SourceOrigin),
		compareSourceClaim(input.Target.SourceClaim, input.Related.SourceClaim),
	}
	state := "indeterminate"
	risk, support := false, false
	supporting, limiting := make([]string, 0), make([]string, 0)
	for _, factor := range factors {
		switch factor.Outcome {
		case "repetition_risk":
			risk = true
		case "independence_support":
			support = true
		}
		if factor.Outcome == "independence_support" {
			supporting = append(supporting, factor.Name+": "+factor.Reason)
		}
		if factor.Outcome == "unknown" {
			limiting = append(limiting, factor.Name+": "+factor.Reason)
		}
	}
	switch {
	case risk && support:
		state = "mixed_signals"
	case risk:
		state = "repetition_risk"
	case support:
		state = "independence_supported"
	}
	assessment := Assessment{
		ContractVersion: ContractVersion, InputContractVersion: InputContractVersion,
		IndependenceState: state, SimilarityMethod: SimilarityMethod,
		NearDuplicateCutoff: NearDuplicateCutoff, Factors: factors,
		SupportingReasons: supporting, LimitingReasons: limiting,
	}
	return json.Marshal(assessment)
}

func compareText(left, right string) Factor {
	leftWords, rightWords := wordSet(left), wordSet(right)
	if len(leftWords) == 0 || len(rightWords) == 0 {
		return unknown("text_similarity", "one or both evidence texts are missing")
	}
	intersection := 0
	for word := range leftWords {
		if _, ok := rightWords[word]; ok {
			intersection++
		}
	}
	union := len(leftWords) + len(rightWords) - intersection
	score := math.Round(float64(intersection)/float64(union)*1e12) / 1e12
	if score == 1 {
		return scored("text_similarity", "repetition_risk", score, "normalized word sets are identical")
	}
	if score >= NearDuplicateCutoff {
		return scored("text_similarity", "repetition_risk", score, "word-set Jaccard similarity meets the versioned near-duplicate cutoff")
	}
	return scored("text_similarity", "no_signal", score, "word-set similarity is below the near-duplicate cutoff; dissimilar wording alone does not establish independence")
}

func compareMedia(left, right []string) Factor {
	if len(left) == 0 || len(right) == 0 {
		return unknown("media_fingerprint", "media fingerprints are missing for one or both observations")
	}
	for _, a := range left {
		for _, b := range right {
			if a != "" && a == b {
				return plain("media_fingerprint", "repetition_risk", "an opaque media fingerprint is shared")
			}
		}
	}
	return plain("media_fingerprint", "no_signal", "supplied media fingerprints differ; different media does not establish independent origin")
}

func compareSourceOrigin(left, right *string) Factor {
	if left == nil || right == nil || strings.TrimSpace(*left) == "" || strings.TrimSpace(*right) == "" {
		return unknown("source_origin", "opaque source-origin evidence is missing")
	}
	if *left == *right {
		return plain("source_origin", "repetition_risk", "both observations have the same opaque source origin")
	}
	return plain("source_origin", "independence_support", "observations have distinct opaque source origins")
}

func compareSourceClaim(left, right extraction.Field) Factor {
	if identifiedClaim(left) && isDependentClaim(*left.Value) || identifiedClaim(right) && isDependentClaim(*right.Value) {
		return plain("source_claim", "repetition_risk", "at least one observation is identified as second-hand or forwarded")
	}
	if !identifiedClaim(left) || !identifiedClaim(right) {
		return unknown("source_claim", "one or both source claims are unknown or ambiguous")
	}
	if *left.Value == "first_hand" && *right.Value == "first_hand" {
		return plain("source_claim", "no_signal", "both claims are first-hand; this alone does not establish independent sources")
	}
	return plain("source_claim", "unknown", "source claims do not establish whether the observations share an origin")
}

func identifiedClaim(field extraction.Field) bool {
	return field.Status == "identified" && field.Value != nil
}
func isDependentClaim(value string) bool { return value == "second_hand" || value == "forwarded" }

func plain(name, outcome, reason string) Factor {
	return Factor{Name: name, Outcome: outcome, Score: nil, Reason: reason}
}
func unknown(name, reason string) Factor { return plain(name, "unknown", reason) }
func scored(name, outcome string, score float64, reason string) Factor {
	return Factor{Name: name, Outcome: outcome, Score: &score, Reason: reason}
}

func wordSet(value string) map[string]struct{} {
	words := make(map[string]struct{})
	var word strings.Builder
	flush := func() {
		if word.Len() > 0 {
			words[strings.ToLower(word.String())] = struct{}{}
			word.Reset()
		}
	}
	for _, char := range value {
		if unicode.IsLetter(char) || unicode.IsNumber(char) {
			word.WriteRune(char)
		} else {
			flush()
		}
	}
	flush()
	return words
}

func ValidateInput(input Input) error {
	if strings.TrimSpace(input.Target.EvidenceID) == "" || strings.TrimSpace(input.Related.EvidenceID) == "" || input.Target.EvidenceID == input.Related.EvidenceID {
		return fmt.Errorf("distinct target and related evidence ids are required")
	}
	return nil
}
