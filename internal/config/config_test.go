package config

import (
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	for _, name := range []string{"SIGNA_API_ADDR", "SIGNA_SHUTDOWN_TIMEOUT", "SIGNA_WORKER_INTERVAL"} {
		t.Setenv(name, "")
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got.APIAddr != defaultAPIAddr {
		t.Errorf("APIAddr = %q, want %q", got.APIAddr, defaultAPIAddr)
	}
	if got.ShutdownTimeout != defaultShutdownTimeout {
		t.Errorf("ShutdownTimeout = %s, want %s", got.ShutdownTimeout, defaultShutdownTimeout)
	}
	if got.WorkerInterval != defaultWorkerInterval {
		t.Errorf("WorkerInterval = %s, want %s", got.WorkerInterval, defaultWorkerInterval)
	}
}

func TestLoadEnvironment(t *testing.T) {
	t.Setenv("SIGNA_API_ADDR", "127.0.0.1:9090")
	t.Setenv("SIGNA_SHUTDOWN_TIMEOUT", "2s")
	t.Setenv("SIGNA_WORKER_INTERVAL", "250ms")

	got, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	want := Config{APIAddr: "127.0.0.1:9090", ShutdownTimeout: 2 * time.Second, WorkerInterval: 250 * time.Millisecond}
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
