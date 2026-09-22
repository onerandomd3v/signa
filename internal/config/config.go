package config

import (
	"fmt"
	"os"
	"time"
)

const (
	defaultAPIAddr         = ":8080"
	defaultShutdownTimeout = 10 * time.Second
	defaultWorkerInterval  = 10 * time.Second
)

// Config contains the runtime settings needed by the foundation processes.
type Config struct {
	APIAddr         string
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

	return Config{
		APIAddr:         apiAddr,
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
