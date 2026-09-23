package config

import (
	"fmt"
	"os"
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
	}, nil
}

func intFromEnv(name string, fallback int) (int, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	var parsed int
	if _, err := fmt.Sscan(value, &parsed); err != nil || parsed <= 0 {
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
