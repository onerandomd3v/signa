//go:build integration

package auth

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestPostgresStoreSessionLifecycleIntegration(t *testing.T) {
	databaseURL := os.Getenv("SIGNA_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgres://signa:signa_local@localhost:5432/signa?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Skipf("PostgreSQL unavailable: %v", err)
	}
	defer db.Close(context.Background())
	_, err = db.Exec(ctx, `
		CREATE TEMP TABLE auth_sessions (
			id UUID PRIMARY KEY,
			user_id UUID NOT NULL,
			token_hash BYTEA NOT NULL UNIQUE,
			expires_at TIMESTAMPTZ NOT NULL,
			revoked_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`)
	if err != nil {
		t.Fatal(err)
	}

	store := NewPostgresStore(db)
	userID := uuid.New()
	created, err := store.Create(ctx, userID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	var storedHash []byte
	if err := db.QueryRow(ctx, `SELECT token_hash FROM auth_sessions WHERE user_id = $1`, userID).Scan(&storedHash); err != nil {
		t.Fatal(err)
	}
	if string(storedHash) == created.Secret || len(storedHash) != 32 {
		t.Fatal("database stored an invalid reusable session secret")
	}
	if got, err := store.Lookup(ctx, created.Secret); err != nil || got.UserID != userID {
		t.Fatalf("active lookup = %#v, %v", got, err)
	}
	if _, err := db.Exec(ctx, `UPDATE auth_sessions SET expires_at = now() - interval '1 second' WHERE user_id = $1`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Lookup(ctx, created.Secret); err == nil {
		t.Fatal("expired session was accepted")
	}

	created, err = store.Create(ctx, userID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Revoke(ctx, created.Secret); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Lookup(ctx, created.Secret); err == nil {
		t.Fatal("revoked session was accepted")
	}
	if _, err := db.Exec(ctx, `INSERT INTO auth_sessions (id, user_id, token_hash, expires_at) VALUES ($1, $2, $3, now() + interval '1 hour')`, uuid.New(), userID, storedHash); err == nil {
		t.Fatal("duplicate token hash was accepted")
	}
}
