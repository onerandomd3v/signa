//go:build integration

package postgres

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestPostgresPing(t *testing.T) {
	databaseURL := os.Getenv("SIGNA_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgres://signa:signa_local@localhost:5432/signa?sslmode=disable"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := Ping(ctx, databaseURL); err != nil {
		t.Fatalf("PostgreSQL ping failed: %v", err)
	}
}
