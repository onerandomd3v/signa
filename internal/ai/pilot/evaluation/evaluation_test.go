package evaluation_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/onerandomd3v/signa/internal/ai/extraction/evaluation"
	pilotEvaluation "github.com/onerandomd3v/signa/internal/ai/pilot/evaluation"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

type benchmarkManifest struct {
	BenchmarkVersion string              `json:"benchmark_version"`
	ReportVersion    string              `json:"report_version"`
	ReportSchema     string              `json:"report_schema"`
	ThresholdStatus  string              `json:"acceptance_threshold_status"`
	Components       []componentManifest `json:"components"`
	Guardrails       []componentManifest `json:"guardrails"`
	Coverage         []coverageManifest  `json:"scenario_coverage"`
	KnownLimitations []string            `json:"known_limitations"`
}

type componentManifest struct {
	Name               string `json:"name"`
	EvaluationManifest string `json:"evaluation_manifest"`
	Schema             string `json:"schema"`
}

type coverageManifest struct {
	Scenario string             `json:"scenario"`
	Fixtures []fixtureReference `json:"fixtures"`
}

type fixtureReference struct {
	Component string `json:"component"`
	CaseID    string `json:"case_id"`
}

type suiteManifest struct {
	Cases []struct {
		ID string `json:"case_id"`
	} `json:"cases"`
}

func TestPilotManifestPinsRequiredSuitesAndExplicitThresholdState(t *testing.T) {
	manifest := loadBenchmarkManifest(t)
	if manifest.BenchmarkVersion != "signa.ai.pilot-benchmark.v1" {
		t.Fatalf("benchmark_version = %q", manifest.BenchmarkVersion)
	}
	if manifest.ReportVersion != "signa.ai.pilot-benchmark-report.v1" {
		t.Fatalf("report_version = %q", manifest.ReportVersion)
	}
	if manifest.ThresholdStatus != "not_configured" {
		t.Fatalf("acceptance_threshold_status = %q, want not_configured", manifest.ThresholdStatus)
	}
	wantComponents := []string{"extraction", "location", "similarity", "contradiction", "independence", "alert_summarization"}
	if len(manifest.Components) != len(wantComponents) {
		t.Fatalf("benchmark components = %d, want %d", len(manifest.Components), len(wantComponents))
	}
	for i, want := range wantComponents {
		if manifest.Components[i].Name != want {
			t.Fatalf("component[%d] = %q, want %q", i, manifest.Components[i].Name, want)
		}
	}
	if len(manifest.Guardrails) != 1 || manifest.Guardrails[0].Name != "coordination" {
		t.Fatalf("guardrails = %+v, want only COD-224 coordination", manifest.Guardrails)
	}
	if len(manifest.KnownLimitations) == 0 {
		t.Fatal("manifest has no known limitations")
	}
}

func TestPilotCoverageReferencesExistingFixtureCases(t *testing.T) {
	manifest := loadBenchmarkManifest(t)
	root := repositoryRoot(t)
	casesByComponent := make(map[string]map[string]bool)
	for _, component := range append(append([]componentManifest(nil), manifest.Components...), manifest.Guardrails...) {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(component.EvaluationManifest)))
		if err != nil {
			t.Fatalf("read %s manifest: %v", component.Name, err)
		}
		var suite suiteManifest
		if err := json.Unmarshal(data, &suite); err != nil {
			t.Fatalf("decode %s manifest: %v", component.Name, err)
		}
		casesByComponent[component.Name] = make(map[string]bool, len(suite.Cases))
		for _, item := range suite.Cases {
			casesByComponent[component.Name][item.ID] = true
		}
	}
	if len(manifest.Coverage) == 0 {
		t.Fatal("manifest has no scenario coverage")
	}
	for _, scenario := range manifest.Coverage {
		if scenario.Scenario == "" || len(scenario.Fixtures) == 0 {
			t.Fatalf("invalid scenario coverage entry: %+v", scenario)
		}
		for _, reference := range scenario.Fixtures {
			if !casesByComponent[reference.Component][reference.CaseID] {
				t.Errorf("scenario %q references missing %s case %q", scenario.Scenario, reference.Component, reference.CaseID)
			}
		}
	}
}

func TestPilotReportSchemaCompilesAsVersionedJSONSchema(t *testing.T) {
	manifest := loadBenchmarkManifest(t)
	data, err := os.ReadFile(filepath.Join(repositoryRoot(t), filepath.FromSlash(manifest.ReportSchema)))
	if err != nil {
		t.Fatal(err)
	}
	var document any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("decode report schema: %v", err)
	}
	compiler := jsonschema.NewCompiler()
	const resource = "https://signa.local/contracts/ai/pilot-benchmark/v1/report.schema.json"
	if err := compiler.AddResource(resource, document); err != nil {
		t.Fatalf("register report schema: %v", err)
	}
	if _, err := compiler.Compile(resource); err != nil {
		t.Fatalf("compile report schema: %v", err)
	}
}

func TestRunIsDeterministicAndReportsFixtureResultsWithoutThreshold(t *testing.T) {
	root := repositoryRoot(t)
	first, err := pilotEvaluation.Run(context.Background(), root, pilotEvaluation.Providers{})
	if err != nil {
		t.Fatalf("Run(): %v", err)
	}
	second, err := pilotEvaluation.Run(context.Background(), root, pilotEvaluation.Providers{})
	if err != nil {
		t.Fatalf("second Run(): %v", err)
	}
	firstJSON, err := first.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := second.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if string(firstJSON) != string(secondJSON) {
		t.Fatal("repeated benchmark runs produced different JSON")
	}
	if first.ExecutionMode != "offline_deterministic" || first.AcceptanceThresholdStatus != "not_configured" {
		t.Fatalf("unexpected execution/threshold state: %+v", first)
	}
	if len(first.Components) != 6 || len(first.Guardrails) != 1 || first.Guardrails[0].Name != "coordination" {
		t.Fatalf("component boundary changed: components=%d guardrails=%+v", len(first.Components), first.Guardrails)
	}
	if first.Overall.TotalCases == 0 || first.Overall.FailedCases != 0 || first.Overall.PassedCases != first.Overall.TotalCases || first.Overall.PassRate != 1 {
		t.Fatalf("unexpected overall results: %+v", first.Overall)
	}
	var report map[string]any
	if err := json.Unmarshal(firstJSON, &report); err != nil {
		t.Fatal(err)
	}
	manifest := loadBenchmarkManifest(t)
	schemaData, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(manifest.ReportSchema)))
	if err != nil {
		t.Fatal(err)
	}
	var schemaDocument any
	if err := json.Unmarshal(schemaData, &schemaDocument); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	const resource = "https://signa.local/contracts/ai/pilot-benchmark/v1/report.schema.json"
	if err := compiler.AddResource(resource, schemaDocument); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(resource)
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(report); err != nil {
		t.Fatalf("serialized report does not satisfy canonical report schema: %v", err)
	}
}

func TestRunSurfacesInjectedProviderFailuresWithoutLeakingErrorText(t *testing.T) {
	const privateError = "secret-token-and-private-reporter-data"
	report, err := pilotEvaluation.Run(context.Background(), repositoryRoot(t), pilotEvaluation.Providers{
		Extraction: evaluation.ProviderFunc(func(context.Context, string) ([]byte, error) {
			return nil, &privateProviderError{message: privateError}
		}),
	})
	if err != nil {
		t.Fatalf("Run(): %v", err)
	}
	var extractionComponent *pilotEvaluation.Component
	for i := range report.Components {
		if report.Components[i].Name == "extraction" {
			extractionComponent = &report.Components[i]
		}
	}
	if extractionComponent == nil {
		t.Fatal("extraction component missing")
	}
	if extractionComponent.ProviderMode != "injected" || extractionComponent.FailedCases == 0 {
		t.Fatalf("injected failure not surfaced: %+v", extractionComponent)
	}
	if len(extractionComponent.Failures) == 0 || !containsKind(extractionComponent.Failures[0].Kinds, "provider_error") {
		t.Fatalf("provider failure kind missing: %+v", extractionComponent.Failures)
	}
	encoded, err := report.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), privateError) {
		t.Fatal("report leaked provider error contents")
	}
}

func TestRunRejectsMalformedProviderOutputAndSurfacesCaseCategory(t *testing.T) {
	report, err := pilotEvaluation.Run(context.Background(), repositoryRoot(t), pilotEvaluation.Providers{
		Extraction: evaluation.ProviderFunc(func(context.Context, string) ([]byte, error) { return []byte(`{"invalid":true}`), nil }),
	})
	if err != nil {
		t.Fatalf("Run(): %v", err)
	}
	var component *pilotEvaluation.Component
	for i := range report.Components {
		if report.Components[i].Name == "extraction" {
			component = &report.Components[i]
		}
	}
	if component == nil || component.FailedCases == 0 {
		t.Fatalf("malformed output was not counted as failure: %+v", component)
	}
	if len(component.Failures) == 0 || !containsKind(component.Failures[0].Kinds, "schema_validation_failed") {
		t.Fatalf("schema failure kind missing: %+v", component.Failures)
	}
	if len(component.Failures[0].Categories) == 0 {
		t.Fatalf("failed category missing: %+v", component.Failures[0])
	}
}

type privateProviderError struct{ message string }

func (e *privateProviderError) Error() string { return e.message }

func containsKind(kinds []string, want string) bool {
	for _, kind := range kinds {
		if kind == want {
			return true
		}
	}
	return false
}

func loadBenchmarkManifest(t *testing.T) benchmarkManifest {
	t.Helper()
	path := filepath.Join(repositoryRoot(t), "contracts", "ai", "pilot-benchmark", "v1", "evaluation.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var manifest benchmarkManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", ".."))
}
