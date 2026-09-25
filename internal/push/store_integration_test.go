//go:build integration

package push

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresStoreUpsertsAndScopesSubscriptions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	databaseURL := os.Getenv("SIGNA_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgres://signa:signa_local@localhost:5432/signa?sslmode=disable"
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	databaseName := fmt.Sprintf("signa_push_test_%d", time.Now().UnixNano())
	maintenanceURL := *parsed
	maintenanceURL.Path = "/postgres"
	maintenance, err := pgx.Connect(ctx, maintenanceURL.String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = maintenance.Exec(context.Background(), "DROP DATABASE IF EXISTS "+databaseName)
		_ = maintenance.Close(context.Background())
	}()
	if _, err := maintenance.Exec(ctx, "CREATE DATABASE "+databaseName); err != nil {
		t.Fatal(err)
	}
	testURL := *parsed
	testURL.Path = "/" + databaseName
	runPushGoose(t, ctx, testURL.String(), "up")
	pool, err := pgxpool.New(ctx, testURL.String())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	store := NewStore(pool)
	ownerID := uuid.New()
	otherID := uuid.New()
	input := SubscriptionInput{Endpoint: "https://push.example.test/send/one", P256DH: integrationP256DH(), Auth: integrationAuth()}
	first, created, err := store.Upsert(ctx, ownerID, input)
	if err != nil || !created {
		t.Fatalf("first upsert = %+v/%v/%v, want created", first, created, err)
	}
	input.Auth = base64.RawURLEncoding.EncodeToString([]byte("updated-auth-key"))
	second, created, err := store.Upsert(ctx, ownerID, input)
	if err != nil || created || second.ID != first.ID {
		t.Fatalf("duplicate upsert = %+v/%v/%v, want same row update", second, created, err)
	}
	ownerSubscriptions, err := store.ListForDelivery(ctx, ownerID)
	if err != nil || len(ownerSubscriptions) != 1 || ownerSubscriptions[0].UserID != ownerID {
		t.Fatalf("owner subscriptions = %+v/%v", ownerSubscriptions, err)
	}
	otherSubscriptions, err := store.ListForDelivery(ctx, otherID)
	if err != nil || len(otherSubscriptions) != 0 {
		t.Fatalf("other user subscriptions = %+v/%v", otherSubscriptions, err)
	}
	if err := store.Delete(ctx, otherID, first.ID); err != ErrSubscriptionNotFound {
		t.Fatalf("cross-user delete error = %v, want ErrSubscriptionNotFound", err)
	}
	if err := store.Delete(ctx, ownerID, first.ID); err != nil {
		t.Fatal(err)
	}
}

func integrationP256DH() string {
	return base64.RawURLEncoding.EncodeToString(append([]byte{4}, make([]byte, 64)...))
}

func integrationAuth() string {
	return base64.RawURLEncoding.EncodeToString(make([]byte, 16))
}

func runPushGoose(t *testing.T, ctx context.Context, databaseURL, command string) {
	t.Helper()
	goBinary := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goBinary += ".exe"
	}
	goose := exec.CommandContext(ctx, goBinary, "run", "github.com/pressly/goose/v3/cmd/goose@v3.27.0", "-dir", "migrations", "postgres", databaseURL, command)
	_, file, _, _ := runtime.Caller(0)
	goose.Dir = filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	if output, err := goose.CombinedOutput(); err != nil {
		t.Fatalf("goose %s: %v\n%s", command, err, output)
	}
}
