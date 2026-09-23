//go:build integration

package outbox

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	goRedis "github.com/redis/go-redis/v9"
)

type failingRedis struct{}

func (failingRedis) XAdd(context.Context, *goRedis.XAddArgs) *goRedis.StringCmd {
	return goRedis.NewStringResult("", fmt.Errorf("temporary redis failure"))
}

func TestPublisherIntegrationAndConsumerGroupProof(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	databaseURL := os.Getenv("SIGNA_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgres://signa:signa_local@localhost:5432/signa?sslmode=disable"
	}
	testURL, maintenance, databaseName := createOutboxTestDatabase(t, ctx, databaseURL)
	defer func() {
		_, _ = maintenance.Exec(context.Background(), `DROP DATABASE IF EXISTS `+databaseName)
		_ = maintenance.Close(context.Background())
	}()
	runOutboxGoose(t, ctx, testURL, "up")

	pool, err := pgxpool.New(ctx, testURL)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	defer pool.Close()
	redisAddr := os.Getenv("SIGNA_REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}
	redisClient := goRedis.NewClient(&goRedis.Options{Addr: redisAddr})
	defer redisClient.Close()
	if err := redisClient.Ping(ctx).Err(); err != nil {
		t.Fatalf("ping redis: %v", err)
	}

	stream := fmt.Sprintf("signa:test:report-events:%d", time.Now().UnixNano())
	group := fmt.Sprintf("signa:test:group:%d", time.Now().UnixNano())
	defer redisClient.Del(context.Background(), stream)
	defer redisClient.XGroupDestroy(context.Background(), stream, group)
	publisher := NewPublisher(pool, redisClient, nil)
	publisher.stream = stream

	eventID := insertOutboxEvent(t, ctx, pool)
	if err := publisher.PublishCycle(ctx); err != nil {
		t.Fatalf("publish cycle: %v", err)
	}
	var publishedAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT published_at FROM outbox_events WHERE id = $1`, eventID).Scan(&publishedAt); err != nil {
		t.Fatal(err)
	}
	if publishedAt == nil {
		t.Fatal("published_at is nil after Redis success")
	}

	if err := redisClient.XGroupCreateMkStream(ctx, stream, group, "0").Err(); err != nil {
		t.Fatalf("create consumer group: %v", err)
	}
	entries, err := redisClient.XReadGroup(ctx, &goRedis.XReadGroupArgs{Group: group, Consumer: "consumer-1", Streams: []string{stream, ">"}, Count: 1, Block: time.Second}).Result()
	if err != nil || len(entries) != 1 || len(entries[0].Messages) != 1 {
		t.Fatalf("XREADGROUP = %+v, error=%v", entries, err)
	}
	fields := entries[0].Messages[0].Values
	if fields["event_id"] != eventID || fields["event_name"] != ReportCreatedV1 || fields["aggregate_type"] != "report" {
		t.Fatalf("stream fields = %+v", fields)
	}
	if err := redisClient.XAck(ctx, stream, group, entries[0].Messages[0].ID).Err(); err != nil {
		t.Fatalf("XACK: %v", err)
	}
	pending, err := redisClient.XPending(ctx, stream, group).Result()
	if err != nil || pending.Count != 0 {
		t.Fatalf("pending = %+v, error=%v", pending, err)
	}

	failureID := insertOutboxEvent(t, ctx, pool)
	failingPublisher := NewPublisher(pool, failingRedis{}, nil)
	failingPublisher.stream = stream
	if err := failingPublisher.PublishCycle(ctx); err == nil {
		t.Fatal("failure cycle error = nil")
	}
	var retryAttempts int
	var lastError string
	if err := pool.QueryRow(ctx, `SELECT retry_attempts, last_error FROM outbox_events WHERE id = $1`, failureID).Scan(&retryAttempts, &lastError); err != nil {
		t.Fatal(err)
	}
	if retryAttempts != 1 || !strings.Contains(lastError, "temporary redis failure") {
		t.Fatalf("retry metadata = (%d, %q)", retryAttempts, lastError)
	}

	if err := publisher.PublishCycle(ctx); err != nil {
		t.Fatalf("retry cycle: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE outbox_events SET published_at = NULL WHERE id = $1`, failureID); err != nil {
		t.Fatal(err)
	}
	if err := publisher.PublishCycle(ctx); err != nil {
		t.Fatalf("duplicate publish cycle: %v", err)
	}
	messages, err := redisClient.XRange(ctx, stream, "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, message := range messages {
		if message.Values["event_id"] == failureID {
			seen++
		}
	}
	if seen != 2 {
		t.Fatalf("duplicate event count = %d, want 2", seen)
	}
}

func insertOutboxEvent(t *testing.T, ctx context.Context, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	err := pool.QueryRow(ctx, `INSERT INTO outbox_events (event_type, aggregate_type, aggregate_id, payload) VALUES ('report.created', 'report', gen_random_uuid(), '{"report_id":"safe-test-payload"}') RETURNING id::text`).Scan(&id)
	if err != nil {
		t.Fatalf("insert outbox event: %v", err)
	}
	return id
}

func createOutboxTestDatabase(t *testing.T, ctx context.Context, databaseURL string) (string, *pgx.Conn, string) {
	t.Helper()
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	databaseName := fmt.Sprintf("signa_outbox_test_%d", time.Now().UnixNano())
	maintenanceURL := *parsed
	maintenanceURL.Path = "/postgres"
	maintenance, err := pgx.Connect(ctx, maintenanceURL.String())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := maintenance.Exec(ctx, `CREATE DATABASE `+databaseName); err != nil {
		_ = maintenance.Close(context.Background())
		t.Fatal(err)
	}
	testURL := *parsed
	testURL.Path = "/" + databaseName
	return testURL.String(), maintenance, databaseName
}

func runOutboxGoose(t *testing.T, ctx context.Context, databaseURL, command string) {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	goose := exec.CommandContext(ctx, "go", "run", "github.com/pressly/goose/v3/cmd/goose@v3.27.0", "-dir", "migrations", "postgres", databaseURL, command)
	goose.Dir = repoRoot
	if output, err := goose.CombinedOutput(); err != nil {
		t.Fatalf("goose %s: %v\n%s", command, err, output)
	}
}
