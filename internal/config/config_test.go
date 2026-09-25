package config

import (
	"reflect"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	for _, name := range []string{"SIGNA_API_ADDR", "SIGNA_SHUTDOWN_TIMEOUT", "SIGNA_WORKER_INTERVAL", "SIGNA_REPORT_RATE_PER_MINUTE", "SIGNA_REPORT_RATE_BURST", "SIGNA_REPORT_GLOBAL_RATE_PER_MINUTE", "SIGNA_REPORT_GLOBAL_RATE_BURST", "SIGNA_OPENAI_API_KEY", "SIGNA_OPENAI_MODEL", "SIGNA_OPENAI_BASE_URL", "SIGNA_AI_SCHEMA_PATH", "SIGNA_AI_GENERATION_SCHEMA_PATH", "SIGNA_AI_CONSUMER_GROUP", "SIGNA_AI_CONSUMER_NAME", "SIGNA_AI_POLL_INTERVAL", "SIGNA_WEB_ALLOWED_ORIGINS"} {
		t.Setenv(name, "")
	}
	t.Setenv("SIGNA_DATABASE_URL", "")
	t.Setenv("SIGNA_REDIS_ADDR", "")

	got, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got.APIAddr != defaultAPIAddr {
		t.Errorf("APIAddr = %q, want %q", got.APIAddr, defaultAPIAddr)
	}
	if got.DatabaseURL != defaultDatabaseURL {
		t.Errorf("DatabaseURL = %q, want %q", got.DatabaseURL, defaultDatabaseURL)
	}
	if got.RedisAddr != defaultRedisAddr {
		t.Errorf("RedisAddr = %q, want %q", got.RedisAddr, defaultRedisAddr)
	}
	if got.ShutdownTimeout != defaultShutdownTimeout {
		t.Errorf("ShutdownTimeout = %s, want %s", got.ShutdownTimeout, defaultShutdownTimeout)
	}
	if got.WorkerInterval != defaultWorkerInterval {
		t.Errorf("WorkerInterval = %s, want %s", got.WorkerInterval, defaultWorkerInterval)
	}
	if got.OpenAIModel != defaultOpenAIModel || got.OpenAIBaseURL != defaultOpenAIBaseURL || got.AISchemaPath != defaultAISchemaPath || got.AIGenerationSchemaPath != defaultAIGenerationSchemaPath || got.AIConsumerGroup != defaultAIConsumerGroup || got.AIPollInterval != defaultAIPollInterval {
		t.Errorf("AI defaults = %+v", got)
	}
	if !reflect.DeepEqual(got.WebAllowedOrigins, []string{defaultWebAllowedOrigin}) {
		t.Fatalf("WebAllowedOrigins = %#v, want %#v", got.WebAllowedOrigins, []string{defaultWebAllowedOrigin})
	}
}

func TestLoadEnvironment(t *testing.T) {
	t.Setenv("SIGNA_API_ADDR", "127.0.0.1:9090")
	t.Setenv("SIGNA_DATABASE_URL", "postgres://user:password@localhost:5432/example?sslmode=disable")
	t.Setenv("SIGNA_REDIS_ADDR", "127.0.0.1:6380")
	t.Setenv("SIGNA_SHUTDOWN_TIMEOUT", "2s")
	t.Setenv("SIGNA_WORKER_INTERVAL", "250ms")
	t.Setenv("SIGNA_REPORT_RATE_PER_MINUTE", "10")
	t.Setenv("SIGNA_REPORT_RATE_BURST", "4")
	t.Setenv("SIGNA_REPORT_GLOBAL_RATE_PER_MINUTE", "200")
	t.Setenv("SIGNA_REPORT_GLOBAL_RATE_BURST", "40")
	t.Setenv("SIGNA_OPENAI_API_KEY", "test-key")
	t.Setenv("SIGNA_OPENAI_MODEL", "test-model")
	t.Setenv("SIGNA_OPENAI_BASE_URL", "http://localhost:9999/v1")
	t.Setenv("SIGNA_AI_SCHEMA_PATH", "schema.json")
	t.Setenv("SIGNA_AI_GENERATION_SCHEMA_PATH", "openai-schema.json")
	t.Setenv("SIGNA_AI_CONSUMER_GROUP", "test-group")
	t.Setenv("SIGNA_AI_CONSUMER_NAME", "test-consumer")
	t.Setenv("SIGNA_AI_POLL_INTERVAL", "750ms")
	t.Setenv("SIGNA_WEB_ALLOWED_ORIGINS", " http://localhost:3000, https://staging.signa.test, http://localhost:3000 ")

	got, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	want := Config{
		APIAddr:                   "127.0.0.1:9090",
		DatabaseURL:               "postgres://user:password@localhost:5432/example?sslmode=disable",
		RedisAddr:                 "127.0.0.1:6380",
		ShutdownTimeout:           2 * time.Second,
		WorkerInterval:            250 * time.Millisecond,
		ReportRatePerMinute:       10,
		ReportRateBurst:           4,
		GlobalReportRatePerMinute: 200,
		GlobalReportRateBurst:     40,
		OpenAIAPIKey:              "test-key",
		OpenAIModel:               "test-model",
		OpenAIBaseURL:             "http://localhost:9999/v1",
		AISchemaPath:              "schema.json",
		AIGenerationSchemaPath:    "openai-schema.json",
		AIConsumerGroup:           "test-group",
		AIConsumerName:            "test-consumer",
		AIPollInterval:            750 * time.Millisecond,
		WebAllowedOrigins:         []string{"http://localhost:3000", "https://staging.signa.test"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Load() = %+v, want %+v", got, want)
	}
}

func TestLoadRejectsInvalidDuration(t *testing.T) {
	t.Setenv("SIGNA_SHUTDOWN_TIMEOUT", "not-a-duration")
	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want an invalid-duration error")
	}
}

func TestLoadConfidencePolicyRequiresStrictlyIncreasingThresholds(t *testing.T) {
	t.Setenv("SIGNA_CONFIDENCE_EMERGING_MIN_EVIDENCE", "1")
	t.Setenv("SIGNA_CONFIDENCE_CORROBORATED_MIN_EVIDENCE", "2")
	t.Setenv("SIGNA_CONFIDENCE_HIGH_MIN_EVIDENCE", "3")
	got, err := LoadConfidencePolicy()
	if err != nil {
		t.Fatal(err)
	}
	if got.EmergingMinEvidence != 1 || got.CorroboratedMinEvidence != 2 || got.HighMinEvidence != 3 {
		t.Fatalf("policy = %+v", got)
	}
	t.Setenv("SIGNA_CONFIDENCE_HIGH_MIN_EVIDENCE", "2")
	if _, err := LoadConfidencePolicy(); err == nil {
		t.Fatal("expected non-increasing threshold error")
	}
}

func TestLoadCoordinationSyncWindowRequiresExplicitDuration(t *testing.T) {
	t.Setenv("SIGNA_COORDINATION_SYNC_WINDOW", "5m")
	got, err := LoadCoordinationSyncWindow()
	if err != nil || got != 5*time.Minute {
		t.Fatalf("window = %s, err = %v", got, err)
	}
	t.Setenv("SIGNA_COORDINATION_SYNC_WINDOW", "500ms")
	if _, err := LoadCoordinationSyncWindow(); err == nil {
		t.Fatal("expected sub-second coordination window error")
	}
}

func TestLoadIncidentLifecyclePolicyRequiresExplicitIncreasingDurations(t *testing.T) {
	t.Setenv("SIGNA_INCIDENT_RESOLVING_AFTER", "1h")
	t.Setenv("SIGNA_INCIDENT_RESOLVED_AFTER", "2h")
	t.Setenv("SIGNA_INCIDENT_EXPIRED_AFTER", "3h")
	t.Setenv("SIGNA_INCIDENT_LIFECYCLE_SWEEP_INTERVAL", "30s")
	got, err := LoadIncidentLifecyclePolicy()
	if err != nil {
		t.Fatal(err)
	}
	if got.ResolvingAfter != time.Hour || got.ResolvedAfter != 2*time.Hour || got.ExpiredAfter != 3*time.Hour || got.SweepInterval != 30*time.Second {
		t.Fatalf("policy = %+v", got)
	}
	t.Setenv("SIGNA_INCIDENT_EXPIRED_AFTER", "2h")
	if _, err := LoadIncidentLifecyclePolicy(); err == nil {
		t.Fatal("expected non-increasing lifecycle duration error")
	}
}

func TestLoadDoesNotRequireWorkerOnlyLifecyclePolicy(t *testing.T) {
	for _, name := range []string{"SIGNA_INCIDENT_RESOLVING_AFTER", "SIGNA_INCIDENT_RESOLVED_AFTER", "SIGNA_INCIDENT_EXPIRED_AFTER", "SIGNA_INCIDENT_LIFECYCLE_SWEEP_INTERVAL"} {
		t.Setenv(name, "")
	}
	if _, err := Load(); err != nil {
		t.Fatal(err)
	}
}

func TestLoadRejectsMalformedRateLimit(t *testing.T) {
	t.Setenv("SIGNA_REPORT_RATE_PER_MINUTE", "6oops")
	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want malformed rate-limit error")
	}
}

func TestLoadRejectsInvalidAIPollInterval(t *testing.T) {
	t.Setenv("SIGNA_AI_POLL_INTERVAL", "not-a-duration")
	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want an invalid-AI-poll-interval error")
	}
}

func TestLoadPublicIncidentGeometryPolicyRequiresPositiveValues(t *testing.T) {
	t.Setenv("SIGNA_PUBLIC_INCIDENT_GRID_METERS", "100")
	t.Setenv("SIGNA_PUBLIC_INCIDENT_MIN_RADIUS_METERS", "250")
	t.Setenv("SIGNA_PUBLIC_INCIDENT_SIMPLIFY_METERS", "25")

	got, err := LoadPublicIncidentGeometryPolicy()
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != PublicIncidentGeometryPolicyVersion || got.GridMeters != 100 || got.MinRadiusMeters != 250 || got.SimplifyMeters != 25 {
		t.Fatalf("policy = %+v", got)
	}

	for _, test := range []struct {
		name  string
		value string
	}{
		{name: "zero grid", value: "0"},
		{name: "negative minimum radius", value: "-1"},
		{name: "invalid simplify", value: "not-a-number"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("SIGNA_PUBLIC_INCIDENT_GRID_METERS", "100")
			t.Setenv("SIGNA_PUBLIC_INCIDENT_MIN_RADIUS_METERS", "250")
			t.Setenv("SIGNA_PUBLIC_INCIDENT_SIMPLIFY_METERS", "25")
			switch test.name {
			case "zero grid":
				t.Setenv("SIGNA_PUBLIC_INCIDENT_GRID_METERS", test.value)
			case "negative minimum radius":
				t.Setenv("SIGNA_PUBLIC_INCIDENT_MIN_RADIUS_METERS", test.value)
			case "invalid simplify":
				t.Setenv("SIGNA_PUBLIC_INCIDENT_SIMPLIFY_METERS", test.value)
			}
			if _, err := LoadPublicIncidentGeometryPolicy(); err == nil {
				t.Fatal("LoadPublicIncidentGeometryPolicy() error = nil")
			}
		})
	}
}

func TestLoadRejectsMalformedWebOrigin(t *testing.T) {
	for _, value := range []string{"*", "http://localhost:3000/path", "ftp://localhost:3000", "http://", "http://localhost:3000,,https://example.test"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("SIGNA_WEB_ALLOWED_ORIGINS", value)
			if _, err := Load(); err == nil {
				t.Fatalf("Load() error = nil for %q", value)
			}
		})
	}
}

func TestLoadCanonicalizesWebOrigins(t *testing.T) {
	t.Setenv("SIGNA_WEB_ALLOWED_ORIGINS", " HTTPS://EXAMPLE.com:443, http://Example.com:80, https://example.com:8443 ")

	got, err := webAllowedOriginsFromEnv()
	if err != nil {
		t.Fatalf("webAllowedOriginsFromEnv() error = %v", err)
	}
	want := []string{"https://example.com", "http://example.com", "https://example.com:8443"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("webAllowedOriginsFromEnv() = %#v, want %#v", got, want)
	}
}

func TestLoadRejectsOutOfRangeWebOriginPort(t *testing.T) {
	t.Setenv("SIGNA_WEB_ALLOWED_ORIGINS", "https://example.com:99999")
	if _, err := webAllowedOriginsFromEnv(); err == nil {
		t.Fatal("webAllowedOriginsFromEnv() error = nil, want invalid port error")
	}
}
func TestLoadPriorityPolicyRequiresExplicitValidConfiguration(t *testing.T) {
	setPriorityEnv(t, "100", "500", "10m", "30m")

	got, err := LoadPriorityPolicy()
	if err != nil {
		t.Fatal(err)
	}
	if got.P1RadiusMeters != 100 || got.P2RadiusMeters != 500 || got.LocationMaxAge != 10*time.Minute || got.IncidentMaxAge != 30*time.Minute {
		t.Fatalf("priority policy = %+v", got)
	}

	for _, tc := range []struct {
		name string
		p1   string
		p2   string
		loc  string
		inc  string
	}{
		{"missing p1", "", "500", "10m", "30m"},
		{"missing p2", "100", "", "10m", "30m"},
		{"missing location age", "100", "500", "", "30m"},
		{"missing incident age", "100", "500", "10m", ""},
		{"non increasing radii", "500", "100", "10m", "30m"},
		{"equal radii", "100", "100", "10m", "30m"},
		{"non positive location age", "100", "500", "0s", "30m"},
		{"non positive incident age", "100", "500", "10m", "0s"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setPriorityEnv(t, tc.p1, tc.p2, tc.loc, tc.inc)
			if _, err := LoadPriorityPolicy(); err == nil {
				t.Fatal("LoadPriorityPolicy() error = nil")
			}
		})
	}
}

func setPriorityEnv(t *testing.T, p1, p2, locationAge, incidentAge string) {
	t.Helper()
	t.Setenv("SIGNA_PRIORITY_P1_RADIUS_METERS", p1)
	t.Setenv("SIGNA_PRIORITY_P2_RADIUS_METERS", p2)
	t.Setenv("SIGNA_PRIORITY_LOCATION_MAX_AGE", locationAge)
	t.Setenv("SIGNA_PRIORITY_INCIDENT_MAX_AGE", incidentAge)
}
