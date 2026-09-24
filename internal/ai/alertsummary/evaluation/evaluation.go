package evaluation

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/onerandomd3v/signa/internal/ai/alertsummary"
)

type Case struct {
	ID         string             `json:"case_id"`
	Categories []string           `json:"categories"`
	State      alertsummary.State `json:"state"`
}
type Suite struct {
	EvaluationVersion string `json:"evaluation_version"`
	ContractVersion   string `json:"contract_version"`
	Cases             []Case `json:"cases"`
}
type Provider interface {
	Summarize(context.Context, alertsummary.State) ([]byte, error)
}
type ProviderFunc func(context.Context, alertsummary.State) ([]byte, error)

func (f ProviderFunc) Summarize(ctx context.Context, s alertsummary.State) ([]byte, error) {
	if f == nil {
		return nil, fmt.Errorf("nil evaluation provider")
	}
	return f(ctx, s)
}

type Failure struct{ CaseID, Category, Message string }
type Report struct {
	EvaluationVersion, ContractVersion string
	Total, Passed                      int
	Failures                           []Failure
}

func (r Report) Failed() bool { return len(r.Failures) != 0 }
func (r Report) String() string {
	return fmt.Sprintf("%s: %d cases, %d passed, %d failures", r.EvaluationVersion, r.Total, r.Passed, len(r.Failures))
}

func LoadSuite(path string) (Suite, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Suite{}, err
	}
	var suite Suite
	if err := json.Unmarshal(data, &suite); err != nil {
		return Suite{}, fmt.Errorf("decode summarization evaluation: %w", err)
	}
	if suite.ContractVersion != alertsummary.ContractVersion || len(suite.Cases) == 0 {
		return Suite{}, fmt.Errorf("invalid summarization evaluation manifest")
	}
	return suite, nil
}
func Evaluate(ctx context.Context, suite Suite, provider Provider, validator *alertsummary.Validator) Report {
	r := Report{EvaluationVersion: suite.EvaluationVersion, ContractVersion: suite.ContractVersion, Total: len(suite.Cases)}
	for _, c := range suite.Cases {
		data, err := provider.Summarize(ctx, c.State)
		if err == nil {
			var summary alertsummary.Summary
			summary, err = validator.Validate(data)
			if err == nil {
				err = alertsummary.ValidateBound(c.State, summary)
			}
		}
		if err != nil {
			category := ""
			if len(c.Categories) > 0 {
				category = c.Categories[0]
			}
			r.Failures = append(r.Failures, Failure{CaseID: c.ID, Category: category, Message: err.Error()})
		} else {
			r.Passed++
		}
	}
	return r
}
