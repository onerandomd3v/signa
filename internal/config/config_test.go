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
