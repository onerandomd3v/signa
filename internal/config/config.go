package config

import (
	"fmt"
	"os"
	"time"
)

const (
	defaultAPIAddr         = ":8080"
	defaultDatabaseURL     = "postgres://signa:signa_local@localhost:5432/signa?sslmode=disable"
	defaultRedisAddr       = "localhost:6379"
	defaultShutdownTimeout = 10 * time.Second
	defaultWorkerInterval  = 500 * time.Millisecond
)

// Config contains the runtime settings needed by the foundation processes.
type Config struct {
	APIAddr         string
	DatabaseURL     string
	RedisAddr       string
	ShutdownTimeout time.Duration
	WorkerInterval  time.Duration
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
		APIAddr:         apiAddr,
		DatabaseURL:     databaseURL,
		RedisAddr:       redisAddr,
		ShutdownTimeout: shutdownTimeout,
		WorkerInterval:  workerInterval,
	}, nil
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
