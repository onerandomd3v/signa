//go:build integration

package redis

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestRedisPing(t *testing.T) {
	address := os.Getenv("SIGNA_REDIS_ADDR")
	if address == "" {
		address = "localhost:6379"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := Ping(ctx, address); err != nil {
		t.Fatalf("Redis ping failed: %v", err)
	}
}
