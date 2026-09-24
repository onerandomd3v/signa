package evaluation

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/onerandomd3v/signa/internal/ai/contradiction"
)

func TestVersionedContradictionSuite(t *testing.T) {
	validator := testValidator(t)
	suite, err := LoadSuite(manifestPath(t), validator)
	if err != nil {
		t.Fatal(err)
	}
	report, err := Evaluate(context.Background(), suite, contradiction.RuleBasedEvaluator{}, validator)
	if err != nil {
		t.Fatal(err)
	}
	if report.Failed() {
		t.Fatal(report.String())
	}
	if report.Total != 8 || report.Passed != 8 {
		t.Fatalf("got %d/%d passed, want 8/8", report.Passed, report.Total)
	}
}

func TestVersionedSuiteCoversRequiredCategories(t *testing.T) {
	validator := testValidator(t)
	suite, err := LoadSuite(manifestPath(t), validator)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"blocked-vs-open", "ongoing-vs-stopped", "compatible", "older-newer", "unknown-location", "ambiguous-location", "distinct-location", "distinct-event-type", "incomplete"}
	seen := map[string]bool{}
	for _, testCase := range suite.Cases {
		for _, category := range testCase.Categories {
			seen[category] = true
		}
	}
	for _, category := range want {
		if !seen[category] {
			t.Errorf("suite missing category %q", category)
		}
	}
}

func TestEvaluationReportsFieldLevelMismatch(t *testing.T) {
	validator := testValidator(t)
	suite, err := LoadSuite(manifestPath(t), validator)
	if err != nil {
		t.Fatal(err)
	}
	provider := contradiction.ProviderFunc(func(ctx context.Context, left, right contradiction.Evidence) ([]byte, error) {
		return (contradiction.RuleBasedEvaluator{}).Evaluate(ctx, left, right)
	})
	case0 := suite.Cases[0]
	case0.Expected.State = "no_signal"
	report, err := Evaluate(context.Background(), Suite{EvaluationVersion: suite.EvaluationVersion, ContractVersion: suite.ContractVersion, Cases: []Case{case0}}, provider, validator)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(report.String(), "contradiction_state mismatch") {
		t.Fatalf("report does not identify state field mismatch: %s", report)
	}
}

func testValidator(t *testing.T) *contradiction.Validator {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	data, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "contracts", "ai", "contradiction", "v1", "schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	validator, err := contradiction.NewValidator(data)
	if err != nil {
		t.Fatal(err)
	}
	return validator
}

func manifestPath(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "contracts", "ai", "contradiction", "v1", "evaluation.json")
}
