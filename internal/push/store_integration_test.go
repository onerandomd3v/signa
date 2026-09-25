//go:build integration

package push

import (
	"context"
	"encoding/base64"
	"errors"
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

func TestPostgresDeliveryResultClaimsFenceStaleCompletions(t *testing.T) {
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
	databaseName := fmt.Sprintf("signa_push_claim_test_%d", time.Now().UnixNano())
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

	incidentID, alertID, userID, subscriptionID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	deliveryA, deliveryB := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO incidents (id, status, confidence_state, severity) VALUES ($1, 'OPEN', 'EMERGING', 'HIGH')`, incidentID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO alerts (id, incident_id, alert_type, confidence_snapshot, severity_snapshot, status_snapshot, priority_snapshot, freshness_snapshot, message, eligibility_policy_version, eligibility_reasons, as_of, idempotency_key, request_fingerprint) VALUES ($1, $2, 'IMMEDIATE', 'EMERGING', 'HIGH', 'OPEN', 'P1', 'FRESH', 'safe', 'test', '[]', now(), $3, 'fingerprint')`, alertID, incidentID, uuid.New().String()); err != nil {
		t.Fatal(err)
	}
	for _, deliveryID := range []uuid.UUID{deliveryA, deliveryB} {
		if _, err := pool.Exec(ctx, `INSERT INTO deliveries (id, alert_id, user_id, priority, channel, idempotency_key, payload) VALUES ($1, $2, $3, 'P1', 'WEB_PUSH', $4, '{"safe":"payload"}')`, deliveryID, alertID, userID, uuid.New().String()); err != nil {
			t.Fatal(err)
		}
	}
	store := NewStore(pool)
	subscription := Subscription{ID: subscriptionID, UserID: userID}
	if _, err := pool.Exec(ctx, `INSERT INTO push_subscriptions (id, user_id, endpoint, p256dh, auth) VALUES ($1, $2, 'https://push.example.test/claim', $3, $4)`, subscriptionID, userID, integrationP256DH(), integrationAuth()); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureDeliveryResults(ctx, deliveryA, userID, []Subscription{subscription}); err != nil {
		t.Fatal(err)
	}
	claimA, claimed, err := store.ClaimDeliveryResult(ctx, deliveryA, subscriptionID, 10*time.Millisecond)
	if err != nil || !claimed || claimA == uuid.Nil {
		t.Fatalf("claim A = %s/%v/%v, want active claim", claimA, claimed, err)
	}
	time.Sleep(25 * time.Millisecond)
	claimB, claimed, err := store.ClaimDeliveryResult(ctx, deliveryA, subscriptionID, time.Minute)
	if err != nil || !claimed || claimB == uuid.Nil || claimA == claimB {
		t.Fatalf("claim B = %s/%v/%v, want distinct reclaimed claim", claimB, claimed, err)
	}
	if err := store.CompleteDeliveryResult(ctx, deliveryA, subscriptionID, claimA, DeliveryResultSucceeded, "stale success", ""); !errors.Is(err, ErrDeliveryResultClaimLost) {
		t.Fatalf("stale success error = %v, want claim lost", err)
	}
	if err := store.CompleteDeliveryResult(ctx, deliveryA, subscriptionID, claimB, DeliveryResultSucceeded, "accepted", ""); err != nil {
		t.Fatal(err)
	}
	if claim, claimed, err := store.ClaimDeliveryResult(ctx, deliveryA, subscriptionID, time.Minute); err != nil || claimed || claim != uuid.Nil {
		t.Fatalf("future claim after success = %s/%v/%v, want no claim", claim, claimed, err)
	}

	if err := store.EnsureDeliveryResults(ctx, deliveryB, userID, []Subscription{subscription}); err != nil {
		t.Fatal(err)
	}
	claimC, claimed, err := store.ClaimDeliveryResult(ctx, deliveryB, subscriptionID, 10*time.Millisecond)
	if err != nil || !claimed || claimC == uuid.Nil {
		t.Fatalf("claim C = %s/%v/%v, want active claim", claimC, claimed, err)
	}
	time.Sleep(25 * time.Millisecond)
	claimD, claimed, err := store.ClaimDeliveryResult(ctx, deliveryB, subscriptionID, time.Minute)
	if err != nil || !claimed || claimD == uuid.Nil || claimC == claimD {
		t.Fatalf("claim D = %s/%v/%v, want distinct reclaimed claim", claimD, claimed, err)
	}
	if err := store.CompleteDeliveryResult(ctx, deliveryB, subscriptionID, claimC, DeliveryResultRetryable, "", "stale failure"); !errors.Is(err, ErrDeliveryResultClaimLost) {
		t.Fatalf("stale failure error = %v, want claim lost", err)
	}
	if err := store.CompleteDeliveryResult(ctx, deliveryB, subscriptionID, claimD, DeliveryResultSucceeded, "accepted", ""); err != nil {
		t.Fatal(err)
	}
	var state DeliveryResultState
	if err := pool.QueryRow(ctx, `SELECT state FROM web_push_delivery_results WHERE delivery_id = $1 AND subscription_id = $2`, deliveryB, subscriptionID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != DeliveryResultSucceeded {
		t.Fatalf("final state = %q, want succeeded", state)
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
