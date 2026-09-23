package config

import (
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	for _, name := range []string{"SIGNA_API_ADDR", "SIGNA_SHUTDOWN_TIMEOUT", "SIGNA_WORKER_INTERVAL", "SIGNA_REPORT_RATE_PER_MINUTE", "SIGNA_REPORT_RATE_BURST", "SIGNA_REPORT_GLOBAL_RATE_PER_MINUTE", "SIGNA_REPORT_GLOBAL_RATE_BURST", "SIGNA_OPENAI_API_KEY", "SIGNA_OPENAI_MODEL", "SIGNA_OPENAI_BASE_URL", "SIGNA_AI_SCHEMA_PATH", "SIGNA_AI_GENERATION_SCHEMA_PATH", "SIGNA_AI_CONSUMER_GROUP", "SIGNA_AI_CONSUMER_NAME", "SIGNA_AI_POLL_INTERVAL"} {
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
	}
	if got != want {
		t.Errorf("Load() = %+v, want %+v", got, want)
	}
}

func TestLoadRejectsInvalidDuration(t *testing.T) {
	t.Setenv("SIGNA_SHUTDOWN_TIMEOUT", "not-a-duration")
	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want an invalid-duration error")
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
