// Package coordination combines pairwise source-independence evidence and
// caller-supplied submission times into an auditable coordination signal.
// It does not make incident truth, confidence, or alert decisions.
package coordination

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/onerandomd3v/signa/internal/ai/independence"
)

const (
	ContractVersion = "signa.ai.source-coordination.v1"
	ConfigVersion   = "signa.ai.source-coordination-config.v1"
)

type Observation struct {
	Evidence    independence.Observation `json:"evidence"`
	SubmittedAt *time.Time               `json:"submitted_at,omitempty"`
}

type Input struct {
	Observations []Observation `json:"observations"`
}

type Config struct {
	ConfigVersion                string `json:"config_version"`
	SynchronizationWindowSeconds int64  `json:"synchronization_window_seconds"`
}

type Assessment struct {
	ContractVersion   string   `json:"contract_version"`
	Config            Config   `json:"config"`
	ObservationCount  int      `json:"observation_count"`
	CoordinationState string   `json:"coordination_state"`
	Factors           []Factor `json:"factors"`
}

type Factor struct {
	Name                string   `json:"name"`
	Outcome             string   `json:"outcome"`
	Weight              *float64 `json:"weight"`
	PairCount           int      `json:"pair_count"`
	KnownPairCount      int      `json:"known_pair_count"`
	SupportingPairCount int      `json:"supporting_pair_count"`
	Reason              string   `json:"reason"`
}

type Provider interface {
	Assess(context.Context, Input, Config) ([]byte, error)
}
type ProviderFunc func(context.Context, Input, Config) ([]byte, error)

func (f ProviderFunc) Assess(ctx context.Context, input Input, config Config) ([]byte, error) {
	if f == nil {
		return nil, errors.New("coordination provider function is nil")
	}
	return f(ctx, input, config)
}

type Processor struct {
	Provider  Provider
	Validator *Validator
}

func (p Processor) Assess(ctx context.Context, input Input, config Config) (Assessment, error) {
	if p.Provider == nil {
		return Assessment{}, errors.New("coordination provider is required")
	}
	if p.Validator == nil {
		return Assessment{}, errors.New("coordination validator is required")
	}
	if err := ValidateInput(input, config); err != nil {
		return Assessment{}, err
	}
	data, err := p.Provider.Assess(ctx, input, config)
	if err != nil {
		return Assessment{}, fmt.Errorf("assess source coordination: %w", err)
	}
	return p.Validator.ValidateForInput(data, input, config)
}

type RuleBasedEvaluator struct{}

func (RuleBasedEvaluator) Assess(ctx context.Context, input Input, config Config) (Assessment, error) {
	if err := ctx.Err(); err != nil {
		return Assessment{}, err
	}
	if err := ValidateInput(input, config); err != nil {
		return Assessment{}, err
	}
	pairs := len(input.Observations) * (len(input.Observations) - 1) / 2
	stats := map[string]*factorStats{
		"text_similarity": {}, "media_fingerprint": {}, "source_origin": {}, "source_claim": {},
	}
	for i := 0; i < len(input.Observations); i++ {
		for j := i + 1; j < len(input.Observations); j++ {
			left, right := input.Observations[i].Evidence, input.Observations[j].Evidence
			data, err := (independence.RuleBasedEvaluator{}).Assess(ctx, independence.Input{Target: left, Related: right})
			if err != nil {
				return Assessment{}, fmt.Errorf("compare evidence pair: %w", err)
			}
			var pair independence.Assessment
			if err := json.Unmarshal(data, &pair); err != nil {
				return Assessment{}, fmt.Errorf("decode evidence pair assessment: %w", err)
			}
			for _, factor := range pair.Factors {
				stat := stats[factor.Name]
				if stat == nil {
					return Assessment{}, fmt.Errorf("unsupported independence factor %q", factor.Name)
				}
				stat.observe(factor)
			}
		}
	}
	factors := make([]Factor, 0, 5)
	for _, name := range []string{"text_similarity", "media_fingerprint", "source_origin", "source_claim"} {
		factors = append(factors, stats[name].factor(name, pairs))
	}
	timing := assessTiming(input.Observations, config, pairs)
	factors = append(factors, timing)
	state := "indeterminate"
	switch timing.Outcome {
	case "unsynchronized":
		state = "no_signal"
	case "synchronized":
		hasUnknown := false
		for _, factor := range factors[:4] {
			if factor.Outcome == "repetition_risk" || factor.Outcome == "mixed_signals" {
				state = "possible_coordination"
				break
			}
			if factor.Outcome == "unknown" {
				hasUnknown = true
			}
		}
		if state == "indeterminate" {
			if factor := stats["source_origin"]; factor.independence > 0 {
				state = "possible_coordination"
			} else if !hasUnknown {
				state = "no_signal"
			}
		}
	}
	return Assessment{ContractVersion: ContractVersion, Config: config, ObservationCount: len(input.Observations), CoordinationState: state, Factors: factors}, nil
}

type factorStats struct {
	known, risk, independence int
	maxScore                  *float64
}

func (s *factorStats) observe(f independence.Factor) {
	if f.Outcome != "unknown" {
		s.known++
	}
	switch f.Outcome {
	case "repetition_risk":
		s.risk++
	case "independence_support":
		s.independence++
	}
	if f.Score != nil && (s.maxScore == nil || *f.Score > *s.maxScore) {
		value := *f.Score
		s.maxScore = &value
	}
}
func (s *factorStats) factor(name string, pairs int) Factor {
	outcome := "unknown"
	switch {
	case s.risk > 0 && s.independence > 0:
		outcome = "mixed_signals"
	case s.risk > 0:
		outcome = "repetition_risk"
	case s.independence > 0:
		outcome = "independence_support"
	case s.known == pairs:
		outcome = "no_signal"
	}
	weight := s.weight()
	if name == "text_similarity" {
		if s.maxScore != nil {
			value := 0.0
			if s.risk > 0 {
				value = *s.maxScore
			}
			weight = &value
		} else {
			weight = nil
		}
	}
	return Factor{Name: name, Outcome: outcome, Weight: weight, PairCount: pairs, KnownPairCount: s.known, SupportingPairCount: s.risk + s.independence, Reason: factorReason(name, outcome, s.known, pairs, s.risk, s.independence)}
}
func (s *factorStats) weight() *float64 {
	if s.known == 0 {
		return nil
	}
	value := float64(s.risk+s.independence) / float64(s.known)
	return &value
}

func assessTiming(observations []Observation, config Config, pairCount int) Factor {
	known := 0
	var first, last time.Time
	for _, observation := range observations {
		if observation.SubmittedAt == nil {
			continue
		}
		stamp := observation.SubmittedAt.UTC()
		if known == 0 || stamp.Before(first) {
			first = stamp
		}
		if known == 0 || stamp.After(last) {
			last = stamp
		}
		known++
	}
	outcome, support := "unknown", 0
	if known == len(observations) {
		window := time.Duration(config.SynchronizationWindowSeconds) * time.Second
		if last.Sub(first) <= window {
			outcome = "synchronized"
			support = pairCount
		} else {
			outcome = "unsynchronized"
		}
	}
	var weight *float64
	switch outcome {
	case "synchronized":
		value := 1.0
		weight = &value
	case "unsynchronized":
		value := 0.0
		weight = &value
	}
	return Factor{Name: "submission_timing", Outcome: outcome, Weight: weight, PairCount: pairCount, KnownPairCount: known * (known - 1) / 2, SupportingPairCount: support, Reason: timingReason(outcome, known, len(observations), config.SynchronizationWindowSeconds)}
}

func ValidateInput(input Input, config Config) error {
	if err := ValidateConfig(config); err != nil {
		return err
	}
	if len(input.Observations) < 2 {
		return errors.New("at least two observations are required")
	}
	seen := make(map[string]bool, len(input.Observations))
	for _, observation := range input.Observations {
		id := strings.TrimSpace(observation.Evidence.EvidenceID)
		if id == "" || seen[id] {
			return errors.New("observations require distinct non-empty evidence ids")
		}
		seen[id] = true
	}
	return nil
}

func ValidateConfig(config Config) error {
	if config.ConfigVersion != ConfigVersion {
		return fmt.Errorf("config_version must be %q", ConfigVersion)
	}
	if config.SynchronizationWindowSeconds <= 0 || config.SynchronizationWindowSeconds > math.MaxInt64/int64(time.Second) {
		return errors.New("synchronization_window_seconds must be positive and representable")
	}
	return nil
}

func factorReason(name, outcome string, known, pairs, risk, independent int) string {
	return fmt.Sprintf("%s: outcome %s across %d of %d known evidence pairs (%d repetition-risk, %d distinct-origin support); counts are descriptive, not confidence", name, outcome, known, pairs, risk, independent)
}
func timingReason(outcome string, known, total int, seconds int64) string {
	return fmt.Sprintf("submission timing is %s: %d of %d caller-supplied timestamps are available; synchronization window is the configured %d seconds", outcome, known, total, seconds)
}
