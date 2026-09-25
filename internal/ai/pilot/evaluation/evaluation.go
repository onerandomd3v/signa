// Package evaluation composes the repository's versioned AI evaluation suites
// into a deterministic, offline pilot benchmark report.
package evaluation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/onerandomd3v/signa/internal/ai/alertsummary"
	alertEvaluation "github.com/onerandomd3v/signa/internal/ai/alertsummary/evaluation"
	"github.com/onerandomd3v/signa/internal/ai/contradiction"
	contradictionEvaluation "github.com/onerandomd3v/signa/internal/ai/contradiction/evaluation"
	"github.com/onerandomd3v/signa/internal/ai/coordination"
	coordinationEvaluation "github.com/onerandomd3v/signa/internal/ai/coordination/evaluation"
	"github.com/onerandomd3v/signa/internal/ai/extraction"
	extractionEvaluation "github.com/onerandomd3v/signa/internal/ai/extraction/evaluation"
	"github.com/onerandomd3v/signa/internal/ai/independence"
	independenceEvaluation "github.com/onerandomd3v/signa/internal/ai/independence/evaluation"
	"github.com/onerandomd3v/signa/internal/ai/location"
	locationEvaluation "github.com/onerandomd3v/signa/internal/ai/location/evaluation"
	"github.com/onerandomd3v/signa/internal/ai/similarity"
	similarityEvaluation "github.com/onerandomd3v/signa/internal/ai/similarity/evaluation"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const manifestRelativePath = "contracts/ai/pilot-benchmark/v1/evaluation.json"
const reportSchemaURL = "https://signa.local/contracts/ai/pilot-benchmark/v1/report.schema.json"

// Providers allows callers to replace component providers. Nil selects each
// component's deterministic offline implementation.
type Providers struct {
	Extraction       extraction.Provider
	Location         location.Provider
	Similarity       similarity.Provider
	Contradiction    contradiction.Provider
	Independence     independence.Provider
	Coordination     coordination.Provider
	AlertSummarizing alertEvaluation.Provider
}

type Report struct {
	ReportVersion             string      `json:"report_version"`
	BenchmarkVersion          string      `json:"benchmark_version"`
	ExecutionMode             string      `json:"execution_mode"`
	AcceptanceThresholdStatus string      `json:"acceptance_threshold_status"`
	Overall                   Counts      `json:"overall"`
	Components                []Component `json:"components"`
	Guardrails                []Component `json:"guardrails"`
	ScenarioCoverage          []Scenario  `json:"scenario_coverage"`
	KnownLimitations          []string    `json:"known_limitations"`
}

type Counts struct {
	TotalCases            int     `json:"total_cases"`
	PassedCases           int     `json:"passed_cases"`
	FailedCases           int     `json:"failed_cases"`
	PassRate              float64 `json:"pass_rate"`
	EvaluationUnitsTotal  int     `json:"evaluation_units_total"`
	EvaluationUnitsPassed int     `json:"evaluation_units_passed"`
	EvaluationUnitsFailed int     `json:"evaluation_units_failed"`
}

type Component struct {
	Name              string  `json:"name"`
	EvaluationVersion string  `json:"evaluation_version"`
	ContractVersion   string  `json:"contract_version"`
	ToleranceVersion  *string `json:"tolerance_version"`
	ProviderMode      string  `json:"provider_mode"`
	Counts
	Failures []Failure `json:"failures"`
}

type Failure struct {
	CaseID     string   `json:"case_id"`
	Categories []string `json:"categories"`
	Kinds      []string `json:"kinds"`
}

type Scenario struct {
	Scenario string             `json:"scenario"`
	Fixtures []FixtureReference `json:"fixtures"`
}

type FixtureReference struct {
	Component string `json:"component"`
	CaseID    string `json:"case_id"`
}

type manifest struct {
	BenchmarkVersion string              `json:"benchmark_version"`
	ReportVersion    string              `json:"report_version"`
	ReportSchema     string              `json:"report_schema"`
	ThresholdStatus  string              `json:"acceptance_threshold_status"`
	Components       []manifestComponent `json:"components"`
	Guardrails       []manifestComponent `json:"guardrails"`
	Coverage         []Scenario          `json:"scenario_coverage"`
	KnownLimitations []string            `json:"known_limitations"`
}

type manifestComponent struct {
	Name                string `json:"name"`
	EvaluationManifest  string `json:"evaluation_manifest"`
	Schema              string `json:"schema"`
	DefaultProviderMode string `json:"default_provider_mode"`
}

// Run loads all checked-in suites and executes the benchmark without network or
// external service access. No acceptance threshold is inferred or applied.
func Run(ctx context.Context, repositoryRoot string, providers Providers) (Report, error) {
	if ctx == nil {
		return Report{}, errors.New("pilot evaluation context is required")
	}
	root, err := filepath.Abs(repositoryRoot)
	if err != nil {
		return Report{}, fmt.Errorf("resolve repository root: %w", err)
	}
	definition, err := loadManifest(filepath.Join(root, filepath.FromSlash(manifestRelativePath)))
	if err != nil {
		return Report{}, err
	}
	if err := validateManifestReferences(root, definition); err != nil {
		return Report{}, err
	}
	result := Report{
		ReportVersion: definition.ReportVersion, BenchmarkVersion: definition.BenchmarkVersion,
		ExecutionMode: "offline_deterministic", AcceptanceThresholdStatus: definition.ThresholdStatus,
		Components: []Component{}, Guardrails: []Component{}, ScenarioCoverage: definition.Coverage,
		KnownLimitations: definition.KnownLimitations,
	}
	if providersInjected(providers) {
		result.ExecutionMode = "injected"
	}
	for _, entry := range definition.Components {
		component, err := evaluateComponent(ctx, root, entry, providers)
		if err != nil {
			return Report{}, fmt.Errorf("evaluate %s: %w", entry.Name, err)
		}
		result.Components = append(result.Components, component)
		addCounts(&result.Overall, component.Counts)
	}
	for _, entry := range definition.Guardrails {
		component, err := evaluateComponent(ctx, root, entry, providers)
		if err != nil {
			return Report{}, fmt.Errorf("evaluate guardrail %s: %w", entry.Name, err)
		}
		result.Guardrails = append(result.Guardrails, component)
	}
	result.Overall.PassRate = rate(result.Overall.PassedCases, result.Overall.TotalCases)
	if err := validateReport(result, filepath.Join(root, filepath.FromSlash(definition.ReportSchema))); err != nil {
		return Report{}, fmt.Errorf("validate pilot report: %w", err)
	}
	return result, nil
}

func (r Report) Marshal() ([]byte, error) {
	sort.Slice(r.Components, func(i, j int) bool { return r.Components[i].Name < r.Components[j].Name })
	sort.Slice(r.Guardrails, func(i, j int) bool { return r.Guardrails[i].Name < r.Guardrails[j].Name })
	return json.MarshalIndent(r, "", "  ")
}

type rawFailure struct{ caseID, categories, message string }

func evaluateComponent(ctx context.Context, root string, item manifestComponent, providers Providers) (Component, error) {
	manifestPath := filepath.Join(root, filepath.FromSlash(item.EvaluationManifest))
	schema, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(item.Schema)))
	if err != nil {
		return Component{}, fmt.Errorf("read component schema: %w", err)
	}
	component := Component{Name: item.Name, ProviderMode: item.DefaultProviderMode, Failures: []Failure{}}
	var total, passed int
	var evalVersion, contractVersion, toleranceVersion string
	var failures []rawFailure
	switch item.Name {
	case "extraction":
		validator, err := extraction.NewValidator(schema)
		if err != nil {
			return Component{}, err
		}
		suite, err := extractionEvaluation.LoadSuite(manifestPath, validator)
		if err != nil {
			return Component{}, err
		}
		provider := providers.Extraction
		if provider == nil {
			provider, err = fixtureReplayProvider(suite)
			if err != nil {
				return Component{}, err
			}
		}
		r, err := extractionEvaluation.Evaluate(ctx, suite, provider, validator)
		if err != nil {
			return Component{}, err
		}
		total, passed, failures = r.Total, r.Passed, mapExtractionFailures(r.Failures)
		evalVersion, contractVersion, toleranceVersion = r.EvaluationVersion, r.ContractVersion, suite.ToleranceVersion
	case "location":
		validator, err := location.NewValidator(schema)
		if err != nil {
			return Component{}, err
		}
		extractionSchema, err := os.ReadFile(filepath.Join(root, "contracts/ai/extraction/v0/schema.json"))
		if err != nil {
			return Component{}, err
		}
		extractionValidator, err := extraction.NewValidator(extractionSchema)
		if err != nil {
			return Component{}, err
		}
		suite, err := locationEvaluation.LoadSuite(manifestPath, validator, extractionValidator)
		if err != nil {
			return Component{}, err
		}
		provider := providers.Location
		if provider == nil {
			provider = location.RuleBasedNormalizer{}
		}
		r, err := locationEvaluation.Evaluate(ctx, suite, provider, validator)
		if err != nil {
			return Component{}, err
		}
		total, passed, failures = r.Total, r.Passed, mapLocationFailures(r.Failures)
		evalVersion, contractVersion, toleranceVersion = r.EvaluationVersion, r.ContractVersion, suite.ToleranceVersion
	case "similarity":
		validator, err := similarity.NewValidator(schema)
		if err != nil {
			return Component{}, err
		}
		suite, err := similarityEvaluation.LoadSuite(manifestPath, validator)
		if err != nil {
			return Component{}, err
		}
		provider := providers.Similarity
		if provider == nil {
			provider = similarity.RuleBasedScorer{}
		}
		r, err := similarityEvaluation.Evaluate(ctx, suite, provider, validator)
		if err != nil {
			return Component{}, err
		}
		total, passed, failures = len(suite.Cases), r.Passed, mapSimilarityFailures(r.Failures)
		evalVersion, contractVersion, toleranceVersion = r.EvaluationVersion, r.ContractVersion, suite.ToleranceVersion
		component.EvaluationUnitsTotal = r.Total
	case "contradiction":
		validator, err := contradiction.NewValidator(schema)
		if err != nil {
			return Component{}, err
		}
		suite, err := contradictionEvaluation.LoadSuite(manifestPath, validator)
		if err != nil {
			return Component{}, err
		}
		provider := providers.Contradiction
		if provider == nil {
			provider = contradiction.RuleBasedEvaluator{}
		}
		r, err := contradictionEvaluation.Evaluate(ctx, suite, provider, validator)
		if err != nil {
			return Component{}, err
		}
		total, passed, failures = r.Total, r.Passed, mapContradictionFailures(r.Failures)
		evalVersion, contractVersion = r.EvaluationVersion, r.ContractVersion
	case "independence":
		validator, err := independence.NewValidator(schema)
		if err != nil {
			return Component{}, err
		}
		suite, err := independenceEvaluation.LoadSuite(manifestPath, validator)
		if err != nil {
			return Component{}, err
		}
		provider := providers.Independence
		if provider == nil {
			provider = independence.RuleBasedEvaluator{}
		}
		r, err := independenceEvaluation.Evaluate(ctx, suite, provider, validator)
		if err != nil {
			return Component{}, err
		}
		total, passed, failures = r.Total, r.Passed, mapIndependenceFailures(r.Failures)
		evalVersion, contractVersion, toleranceVersion = r.EvaluationVersion, r.ContractVersion, suite.ToleranceVersion
	case "coordination":
		validator, err := coordination.NewValidator(schema)
		if err != nil {
			return Component{}, err
		}
		suite, err := coordinationEvaluation.LoadSuite(manifestPath, validator)
		if err != nil {
			return Component{}, err
		}
		provider := providers.Coordination
		if provider == nil {
			provider = coordination.ProviderFunc(func(ctx context.Context, input coordination.Input, config coordination.Config) ([]byte, error) {
				assessment, err := (coordination.RuleBasedEvaluator{}).Assess(ctx, input, config)
				if err != nil {
					return nil, err
				}
				return json.Marshal(assessment)
			})
		}
		r, err := coordinationEvaluation.Evaluate(ctx, suite, provider, validator)
		if err != nil {
			return Component{}, err
		}
		total, passed, failures = r.Total, r.Passed, mapCoordinationFailures(r.Failures)
		evalVersion, contractVersion, toleranceVersion = r.EvaluationVersion, r.ContractVersion, suite.ToleranceVersion
	case "alert_summarization":
		validator, err := alertsummary.NewValidator(schema)
		if err != nil {
			return Component{}, err
		}
		suite, err := alertEvaluation.LoadSuite(manifestPath)
		if err != nil {
			return Component{}, err
		}
		provider := providers.AlertSummarizing
		if provider == nil {
			provider = alertEvaluation.ProviderFunc(func(_ context.Context, state alertsummary.State) ([]byte, error) {
				return json.Marshal(alertsummary.RenderDeterministic(state))
			})
		}
		r := alertEvaluation.Evaluate(ctx, suite, provider, validator)
		total, passed, failures = r.Total, r.Passed, mapAlertFailures(r.Failures)
		evalVersion, contractVersion = r.EvaluationVersion, r.ContractVersion
	default:
		return Component{}, fmt.Errorf("unknown benchmark component %q", item.Name)
	}
	unitsTotal := component.EvaluationUnitsTotal
	if unitsTotal == 0 {
		unitsTotal = total
	}
	failedCases := uniqueFailureCount(failures)
	if err := validateEvaluationCounts(item.Name, total, unitsTotal, passed, failures); err != nil {
		return Component{}, err
	}
	component.EvaluationVersion, component.ContractVersion = evalVersion, contractVersion
	if toleranceVersion != "" {
		component.ToleranceVersion = &toleranceVersion
	}
	component.TotalCases, component.FailedCases, component.PassedCases = total, failedCases, total-failedCases
	component.PassRate = rate(component.PassedCases, total)
	component.EvaluationUnitsTotal, component.EvaluationUnitsPassed = unitsTotal, passed
	component.EvaluationUnitsFailed = unitsTotal - passed
	component.Failures = sanitizeFailures(failures)
	if component.ProviderMode == "" {
		return Component{}, errors.New("provider mode missing from benchmark manifest")
	}
	if providerInjectedFor(item.Name, providers) {
		component.ProviderMode = "injected"
	}
	return component, nil
}

func validateEvaluationCounts(name string, cases, units, passed int, failures []rawFailure) error {
	if cases <= 0 || units < cases || passed < 0 || passed > units {
		return fmt.Errorf("%s evaluator returned malformed case/unit counts", name)
	}
	failedUnits := units - passed
	if (failedUnits == 0) != (len(failures) == 0) {
		return fmt.Errorf("%s evaluator returned inconsistent pass counts and failures", name)
	}
	failedCases := uniqueFailureCount(failures)
	if failedCases > failedUnits {
		return fmt.Errorf("%s evaluator reports more failed cases than failed evaluation units", name)
	}
	for _, failure := range failures {
		if failure.caseID == "" {
			return fmt.Errorf("%s evaluator returned a failure without a case ID", name)
		}
	}
	return nil
}

func fixtureReplayProvider(suite extractionEvaluation.Suite) (extraction.Provider, error) {
	outputs := make(map[string][]byte, len(suite.Cases))
	for _, item := range suite.Cases {
		encoded, err := json.Marshal(item.Expected)
		if err != nil {
			return nil, err
		}
		if old, ok := outputs[item.RawReportText]; ok && string(old) != string(encoded) {
			return nil, errors.New("extraction fixture replay has conflicting expected outputs for identical input")
		}
		outputs[item.RawReportText] = encoded
	}
	return extractionEvaluation.ProviderFunc(func(_ context.Context, text string) ([]byte, error) {
		output, ok := outputs[text]
		if !ok {
			return nil, errors.New("input is absent from extraction fixture replay")
		}
		return append([]byte(nil), output...), nil
	}), nil
}

func loadManifest(path string) (manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return manifest{}, fmt.Errorf("read pilot benchmark manifest: %w", err)
	}
	var result manifest
	if err := json.Unmarshal(data, &result); err != nil {
		return manifest{}, fmt.Errorf("decode pilot benchmark manifest: %w", err)
	}
	if result.BenchmarkVersion != "signa.ai.pilot-benchmark.v1" || result.ReportVersion != "signa.ai.pilot-benchmark-report.v1" || result.ThresholdStatus != "not_configured" || result.ReportSchema == "" || len(result.Coverage) == 0 || len(result.KnownLimitations) == 0 {
		return manifest{}, errors.New("pilot benchmark manifest is incomplete or has unsupported versions/threshold state")
	}
	want := []string{"extraction", "location", "similarity", "contradiction", "independence", "alert_summarization"}
	if len(result.Components) != len(want) {
		return manifest{}, errors.New("pilot benchmark manifest must contain six required components")
	}
	for i, name := range want {
		if result.Components[i].Name != name {
			return manifest{}, fmt.Errorf("benchmark component[%d] = %q, want %q", i, result.Components[i].Name, name)
		}
	}
	if len(result.Guardrails) != 1 || result.Guardrails[0].Name != "coordination" {
		return manifest{}, errors.New("coordination must be the sole pilot dependency guardrail")
	}
	for _, entry := range append(append([]manifestComponent(nil), result.Components...), result.Guardrails...) {
		if entry.EvaluationManifest == "" || entry.Schema == "" || entry.DefaultProviderMode == "" {
			return manifest{}, fmt.Errorf("component %q has incomplete manifest paths/provider mode", entry.Name)
		}
	}
	return result, nil
}

func validateManifestReferences(root string, definition manifest) error {
	known := map[string]map[string]bool{}
	for _, item := range append(append([]manifestComponent(nil), definition.Components...), definition.Guardrails...) {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(item.EvaluationManifest)))
		if err != nil {
			return fmt.Errorf("read %s evaluation manifest: %w", item.Name, err)
		}
		var suite struct {
			Cases []struct {
				ID string `json:"case_id"`
			} `json:"cases"`
		}
		if err := json.Unmarshal(data, &suite); err != nil {
			return fmt.Errorf("decode %s evaluation manifest: %w", item.Name, err)
		}
		known[item.Name] = map[string]bool{}
		for _, entry := range suite.Cases {
			if entry.ID != "" {
				known[item.Name][entry.ID] = true
			}
		}
	}
	for _, scenario := range definition.Coverage {
		if scenario.Scenario == "" || len(scenario.Fixtures) == 0 {
			return errors.New("scenario requires name and fixture references")
		}
		for _, ref := range scenario.Fixtures {
			if !known[ref.Component][ref.CaseID] {
				return fmt.Errorf("scenario %q references unknown %s case %q", scenario.Scenario, ref.Component, ref.CaseID)
			}
		}
	}
	return nil
}

func validateReport(report Report, schemaPath string) error {
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		return fmt.Errorf("read report schema: %w", err)
	}
	var document any
	if err := json.Unmarshal(data, &document); err != nil {
		return fmt.Errorf("decode report schema: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(reportSchemaURL, document); err != nil {
		return fmt.Errorf("register report schema: %w", err)
	}
	schema, err := compiler.Compile(reportSchemaURL)
	if err != nil {
		return fmt.Errorf("compile report schema: %w", err)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		return err
	}
	var instance any
	if err := json.Unmarshal(encoded, &instance); err != nil {
		return err
	}
	if err := schema.Validate(instance); err != nil {
		return err
	}
	return nil
}

func addCounts(total *Counts, value Counts) {
	total.TotalCases += value.TotalCases
	total.PassedCases += value.PassedCases
	total.FailedCases += value.FailedCases
	total.EvaluationUnitsTotal += value.EvaluationUnitsTotal
	total.EvaluationUnitsPassed += value.EvaluationUnitsPassed
	total.EvaluationUnitsFailed += value.EvaluationUnitsFailed
}
func rate(passed, total int) float64 {
	if total == 0 {
		return 0
	}
	return math.Round(float64(passed)/float64(total)*1e6) / 1e6
}

func mapExtractionFailures(in []extractionEvaluation.Failure) []rawFailure {
	out := make([]rawFailure, 0, len(in))
	for _, f := range in {
		out = append(out, rawFailure{f.CaseID, f.Category, f.Message})
	}
	return out
}
func mapLocationFailures(in []locationEvaluation.Failure) []rawFailure {
	out := make([]rawFailure, 0, len(in))
	for _, f := range in {
		out = append(out, rawFailure{f.CaseID, f.Category, f.Message})
	}
	return out
}
func mapSimilarityFailures(in []similarityEvaluation.Failure) []rawFailure {
	out := make([]rawFailure, 0, len(in))
	for _, f := range in {
		out = append(out, rawFailure{f.CaseID, f.Category, f.Message})
	}
	return out
}
func mapContradictionFailures(in []contradictionEvaluation.Failure) []rawFailure {
	out := make([]rawFailure, 0, len(in))
	for _, f := range in {
		out = append(out, rawFailure{f.CaseID, f.Category, f.Message})
	}
	return out
}
func mapIndependenceFailures(in []independenceEvaluation.Failure) []rawFailure {
	out := make([]rawFailure, 0, len(in))
	for _, f := range in {
		out = append(out, rawFailure{f.CaseID, f.Category, f.Message})
	}
	return out
}
func mapCoordinationFailures(in []coordinationEvaluation.Failure) []rawFailure {
	out := make([]rawFailure, 0, len(in))
	for _, f := range in {
		out = append(out, rawFailure{f.CaseID, f.Category, f.Message})
	}
	return out
}
func mapAlertFailures(in []alertEvaluation.Failure) []rawFailure {
	out := make([]rawFailure, 0, len(in))
	for _, f := range in {
		out = append(out, rawFailure{f.CaseID, f.Category, f.Message})
	}
	return out
}

func uniqueFailureCount(items []rawFailure) int {
	seen := map[string]bool{}
	for _, item := range items {
		seen[item.caseID] = true
	}
	return len(seen)
}
func sanitizeFailures(items []rawFailure) []Failure {
	byID := map[string]*Failure{}
	for _, item := range items {
		if item.caseID == "" {
			continue
		}
		failure := byID[item.caseID]
		if failure == nil {
			failure = &Failure{CaseID: item.caseID, Categories: splitCategories(item.categories), Kinds: []string{}}
			byID[item.caseID] = failure
		}
		kind := failureKind(item.message)
		found := false
		for _, old := range failure.Kinds {
			if old == kind {
				found = true
			}
		}
		if !found {
			failure.Kinds = append(failure.Kinds, kind)
		}
	}
	out := make([]Failure, 0, len(byID))
	for _, failure := range byID {
		sort.Strings(failure.Categories)
		sort.Strings(failure.Kinds)
		out = append(out, *failure)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CaseID < out[j].CaseID })
	return out
}
func splitCategories(value string) []string {
	if value == "" {
		return []string{}
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}
func failureKind(message string) string {
	value := strings.ToLower(message)
	switch {
	case strings.Contains(value, "provider error"):
		return "provider_error"
	case strings.Contains(value, "schema validation") || strings.Contains(value, "schema"):
		return "schema_validation_failed"
	case strings.Contains(value, "mismatch") || strings.Contains(value, "unexpected"):
		return "fixture_mismatch"
	default:
		return "evaluation_failure"
	}
}
func providersInjected(p Providers) bool {
	return p.Extraction != nil || p.Location != nil || p.Similarity != nil || p.Contradiction != nil || p.Independence != nil || p.Coordination != nil || p.AlertSummarizing != nil
}
func providerInjectedFor(name string, p Providers) bool {
	switch name {
	case "extraction":
		return p.Extraction != nil
	case "location":
		return p.Location != nil
	case "similarity":
		return p.Similarity != nil
	case "contradiction":
		return p.Contradiction != nil
	case "independence":
		return p.Independence != nil
	case "coordination":
		return p.Coordination != nil
	case "alert_summarization":
		return p.AlertSummarizing != nil
	}
	return false
}
