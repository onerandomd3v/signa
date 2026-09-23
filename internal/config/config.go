package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

const (
	defaultAPIAddr                   = ":8080"
	defaultDatabaseURL               = "postgres://signa:signa_local@localhost:5432/signa?sslmode=disable"
	defaultRedisAddr                 = "localhost:6379"
	defaultShutdownTimeout           = 10 * time.Second
	defaultWorkerInterval            = 500 * time.Millisecond
	defaultReportRatePerMinute       = 6
	defaultReportRateBurst           = 3
	defaultGlobalReportRatePerMinute = 120
	defaultGlobalReportRateBurst     = 30
	defaultOpenAIModel               = "gpt-4.1-mini"
	defaultOpenAIBaseURL             = "https://api.openai.com/v1"
	defaultAISchemaPath              = "contracts/ai/extraction/v0/schema.json"
	defaultAIGenerationSchemaPath    = "contracts/ai/extraction/v0/openai.schema.json"
	defaultAIConsumerGroup           = "signa-ai-text-extraction"
	defaultAIPollInterval            = time.Second
)

// Config contains the runtime settings needed by the foundation processes.
type Config struct {
	APIAddr                   string
	DatabaseURL               string
	RedisAddr                 string
	ShutdownTimeout           time.Duration
	WorkerInterval            time.Duration
	ReportRatePerMinute       int
	ReportRateBurst           int
	GlobalReportRatePerMinute int
	GlobalReportRateBurst     int
	OpenAIAPIKey              string
	OpenAIModel               string
	OpenAIBaseURL             string
	AISchemaPath              string
	AIGenerationSchemaPath    string
	AIConsumerGroup           string
	AIConsumerName            string
	AIPollInterval            time.Duration
	WebAllowedOrigins         []string
	ObjectStorageEndpoint     string
	ObjectStorageRegion       string
	ObjectStorageBucket       string
	ObjectStorageAccessKeyID  string
	ObjectStorageSecret       string
}

// Load reads configuration from environment variables and applies local defaults.
func Load() (Config, error) {
	shutdownTimeout, err := durationFromEnv("SIGNA_SHUTDOWN_TIMEOUT", defaultShutdownTimeout)
	if err != nil {
		return Config{}, err
	}
	workerInterval, err := durationFromEnv("SIGNA_WORKER_INTERVAL", defaultWorkerInterval)
	if err != nil {
		return Config{}, err
	}
	reportRatePerMinute, err := intFromEnv("SIGNA_REPORT_RATE_PER_MINUTE", defaultReportRatePerMinute)
	if err != nil {
		return Config{}, err
	}
	reportRateBurst, err := intFromEnv("SIGNA_REPORT_RATE_BURST", defaultReportRateBurst)
	if err != nil {
		return Config{}, err
	}
	globalReportRatePerMinute, err := intFromEnv("SIGNA_REPORT_GLOBAL_RATE_PER_MINUTE", defaultGlobalReportRatePerMinute)
	if err != nil {
		return Config{}, err
	}
	globalReportRateBurst, err := intFromEnv("SIGNA_REPORT_GLOBAL_RATE_BURST", defaultGlobalReportRateBurst)
	if err != nil {
		return Config{}, err
	}
	aiPollInterval, err := durationFromEnv("SIGNA_AI_POLL_INTERVAL", defaultAIPollInterval)
	if err != nil {
		return Config{}, err
	}
	webAllowedOrigins, err := webAllowedOriginsFromEnv()
	if err != nil {
		return Config{}, err
	}

	apiAddr := os.Getenv("SIGNA_API_ADDR")
	if apiAddr == "" {
		apiAddr = defaultAPIAddr
	}
	databaseURL := os.Getenv("SIGNA_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = defaultDatabaseURL
	}
	redisAddr := os.Getenv("SIGNA_REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = defaultRedisAddr
	}
	openAIModel := os.Getenv("SIGNA_OPENAI_MODEL")
	if openAIModel == "" {
		openAIModel = defaultOpenAIModel
	}
	openAIBaseURL := os.Getenv("SIGNA_OPENAI_BASE_URL")
	if openAIBaseURL == "" {
		openAIBaseURL = defaultOpenAIBaseURL
	}
	aiSchemaPath := os.Getenv("SIGNA_AI_SCHEMA_PATH")
	if aiSchemaPath == "" {
		aiSchemaPath = defaultAISchemaPath
	}
	aiGenerationSchemaPath := os.Getenv("SIGNA_AI_GENERATION_SCHEMA_PATH")
	if aiGenerationSchemaPath == "" {
		aiGenerationSchemaPath = defaultAIGenerationSchemaPath
	}
	aiConsumerGroup := os.Getenv("SIGNA_AI_CONSUMER_GROUP")
	if aiConsumerGroup == "" {
		aiConsumerGroup = defaultAIConsumerGroup
	}

	return Config{
		APIAddr:                   apiAddr,
		DatabaseURL:               databaseURL,
		RedisAddr:                 redisAddr,
		ShutdownTimeout:           shutdownTimeout,
		WorkerInterval:            workerInterval,
		ReportRatePerMinute:       reportRatePerMinute,
		ReportRateBurst:           reportRateBurst,
		GlobalReportRatePerMinute: globalReportRatePerMinute,
		GlobalReportRateBurst:     globalReportRateBurst,
		OpenAIAPIKey:              os.Getenv("SIGNA_OPENAI_API_KEY"),
		OpenAIModel:               openAIModel,
		OpenAIBaseURL:             openAIBaseURL,
		AISchemaPath:              aiSchemaPath,
		AIGenerationSchemaPath:    aiGenerationSchemaPath,
		AIConsumerGroup:           aiConsumerGroup,
		AIConsumerName:            os.Getenv("SIGNA_AI_CONSUMER_NAME"),
		AIPollInterval:            aiPollInterval,
		WebAllowedOrigins:         webAllowedOrigins,
		ObjectStorageEndpoint:     os.Getenv("SIGNA_OBJECT_STORAGE_ENDPOINT"),
		ObjectStorageRegion:       os.Getenv("SIGNA_OBJECT_STORAGE_REGION"),
		ObjectStorageBucket:       os.Getenv("SIGNA_OBJECT_STORAGE_BUCKET"),
		ObjectStorageAccessKeyID:  os.Getenv("SIGNA_OBJECT_STORAGE_ACCESS_KEY_ID"),
		ObjectStorageSecret:       os.Getenv("SIGNA_OBJECT_STORAGE_SECRET_ACCESS_KEY"),
	}, nil
}

func intFromEnv(name string, fallback int) (int, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return parsed, nil
}

func durationFromEnv(name string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}

	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	if duration <= 0 {
		return 0, fmt.Errorf("%s must be greater than zero", name)
	}
	return duration, nil
}
