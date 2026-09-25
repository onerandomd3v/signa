//go:build integration

package webpush

import (
	"context"
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
	"github.com/onerandomd3v/signa/internal/delivery"
	"github.com/onerandomd3v/signa/internal/push"
)

func TestPostgresAdapterRetriesOnlyUnresolvedSubscription(t *testing.T) {
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
	databaseName := fmt.Sprintf("signa_webpush_test_%d", time.Now().UnixNano())
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
	runWebPushGoose(t, ctx, testURL.String(), "up")
	pool, err := pgxpool.New(ctx, testURL.String())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	incidentID, alertID, deliveryID, userID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO incidents (id, status, confidence_state, severity) VALUES ($1, 'OPEN', 'EMERGING', 'HIGH')`, incidentID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO alerts (id, incident_id, alert_type, confidence_snapshot, severity_snapshot, status_snapshot, priority_snapshot, freshness_snapshot, message, eligibility_policy_version, eligibility_reasons, as_of, idempotency_key, request_fingerprint) VALUES ($1, $2, 'IMMEDIATE', 'EMERGING', 'HIGH', 'OPEN', 'P1', 'FRESH', 'safe', 'test', '[]', now(), $3, 'fingerprint')`, alertID, incidentID, uuid.New().String()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO deliveries (id, alert_id, user_id, priority, channel, idempotency_key, payload) VALUES ($1, $2, $3, 'P1', 'WEB_PUSH', $4, '{"safe":"payload"}')`, deliveryID, alertID, userID, uuid.New().String()); err != nil {
		t.Fatal(err)
	}

	store := push.NewStore(pool)
	first, _, err := store.Upsert(ctx, userID, push.SubscriptionInput{Endpoint: "https://push.example.test/one", P256DH: integrationP256DH(), Auth: integrationAuth()})
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := store.Upsert(ctx, userID, push.SubscriptionInput{Endpoint: "https://push.example.test/two", P256DH: integrationP256DH(), Auth: integrationAuth()})
	if err != nil {
		t.Fatal(err)
	}
	sender := &integrationSender{results: map[uuid.UUID][]SendResult{
		first.ID:  {{StatusCode: 201, Response: "first accepted"}},
		second.ID: {{StatusCode: 503, Response: "retry"}, {StatusCode: 201, Response: "second accepted"}},
	}}
	adapter := NewAdapter(store, sender, Config{})
	attempt := delivery.Attempt{DeliveryID: deliveryID.String(), UserID: userID, Channel: Channel, Payload: []byte(`{"safe":"payload"}`)}

	if _, err := adapter.Deliver(ctx, attempt); err == nil {
		t.Fatal("first delivery error = nil, want transient error")
	}
	if _, err := adapter.Deliver(ctx, attempt); err != nil {
		t.Fatalf("second delivery error = %v, want success", err)
	}
	if sender.calls[first.ID] != 1 || sender.calls[second.ID] != 2 {
		t.Fatalf("calls = %+v, want first=1 second=2", sender.calls)
	}
	var succeeded int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM web_push_delivery_results WHERE delivery_id = $1 AND state = 'SUCCEEDED'`, deliveryID).Scan(&succeeded); err != nil {
		t.Fatal(err)
	}
	if succeeded != 2 {
		t.Fatalf("durable succeeded results = %d, want 2", succeeded)
	}
}

type integrationSender struct {
	results map[uuid.UUID][]SendResult
	calls   map[uuid.UUID]int
}

func (s *integrationSender) Send(_ context.Context, subscription push.Subscription, _ []byte, _ string) (SendResult, error) {
	if s.calls == nil {
		s.calls = make(map[uuid.UUID]int)
	}
	s.calls[subscription.ID]++
	results := s.results[subscription.ID]
	result := results[0]
	s.results[subscription.ID] = results[1:]
	return result, nil
}

func runWebPushGoose(t *testing.T, ctx context.Context, databaseURL, command string) {
	t.Helper()
	goBinary := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goBinary += ".exe"
	}
	goose := exec.CommandContext(ctx, goBinary, "run", "github.com/pressly/goose/v3/cmd/goose@v3.27.0", "-dir", "migrations", "postgres", databaseURL, command)
	goose.Dir = filepath.Clean(filepath.Join("internal", "webpush", "..", ".."))
	if output, err := goose.CombinedOutput(); err != nil {
		t.Fatalf("goose %s: %v\n%s", command, err, output)
	}
}

func integrationP256DH() string {
	return "BAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
}

func integrationAuth() string {
	return "AAAAAAAAAAAAAAAAAAAAAA"
}
