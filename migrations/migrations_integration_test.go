//go:build integration

package migrations

import (
	"context"
	"encoding/json"
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
)

const defaultDatabaseURL = "postgres://signa:signa_local@localhost:5432/signa?sslmode=disable"

func TestReportAndOutboxMigration(t *testing.T) {
	databaseURL := os.Getenv("SIGNA_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = defaultDatabaseURL
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	parsedDatabaseURL, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatalf("parse PostgreSQL URL: %v", err)
	}

	testDatabaseName := fmt.Sprintf("signa_migration_test_%d", time.Now().UnixNano())
	maintenanceURL := *parsedDatabaseURL
	maintenanceURL.Path = "/postgres"
	maintenanceConnection, err := pgx.Connect(ctx, maintenanceURL.String())
	if err != nil {
		t.Fatalf("connect to PostgreSQL maintenance database: %v", err)
	}
	defer func() {
		_, _ = maintenanceConnection.Exec(context.Background(), `DROP DATABASE IF EXISTS `+testDatabaseName)
		_ = maintenanceConnection.Close(context.Background())
	}()

	if _, err := maintenanceConnection.Exec(ctx, `CREATE DATABASE `+testDatabaseName); err != nil {
		t.Fatalf("create isolated migration database: %v", err)
	}

	testDatabaseURL := *parsedDatabaseURL
	testDatabaseURL.Path = "/" + testDatabaseName
	testSchema := "public"
	runGoose(t, ctx, testDatabaseURL.String(), "up")
	connection, err := pgx.Connect(ctx, testDatabaseURL.String())
	if err != nil {
		t.Fatalf("connect to isolated PostgreSQL database: %v", err)
	}
	defer func() { _ = connection.Close(context.Background()) }()
	assertTableExists(t, ctx, connection, testSchema, "report_media")
	assertTableExists(t, ctx, connection, testSchema, "incidents")
	assertTableExists(t, ctx, connection, testSchema, "incident_reports")
	assertTableExists(t, ctx, connection, testSchema, "incident_state_history")
	assertTableExists(t, ctx, connection, testSchema, "report_ai_extractions")
	assertTableExists(t, ctx, connection, testSchema, "user_locations")
	assertTableExists(t, ctx, connection, testSchema, "user_location_deletions")

	t.Run("report columns", func(t *testing.T) {
		rows, err := connection.Query(ctx, `
			SELECT column_name, is_nullable, data_type, udt_name
			FROM information_schema.columns
			WHERE table_schema = $1 AND table_name = 'reports'
			ORDER BY ordinal_position
		`, testSchema)
		if err != nil {
			t.Fatalf("query report columns: %v", err)
		}
		defer rows.Close()

		type columnMetadata struct {
			nullable string
			dataType string
			udtName  string
		}
		columns := map[string]columnMetadata{}
		for rows.Next() {
			var name, nullable, dataType, udtName string
			if err := rows.Scan(&name, &nullable, &dataType, &udtName); err != nil {
				t.Fatalf("scan report column: %v", err)
			}
			columns[name] = columnMetadata{nullable: nullable, dataType: dataType, udtName: udtName}
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("iterate report columns: %v", err)
		}

		required := []string{
			"id", "reporter_id", "incident_id", "raw_text", "normalized_text",
			"source_type", "eyewitness_claim", "claimed_location", "observed_at",
			"submitted_at", "device_location", "location_accuracy", "language",
			"evidence_state", "created_at", "idempotency_key", "request_fingerprint",
		}
		for _, name := range required {
			if _, ok := columns[name]; !ok {
				t.Errorf("missing report column %q", name)
			}
		}
		for _, name := range []string{
			"reporter_id", "incident_id", "normalized_text", "source_type",
			"eyewitness_claim", "claimed_location", "observed_at", "device_location",
			"location_accuracy", "language", "evidence_state", "idempotency_key",
			"request_fingerprint",
		} {
			if columns[name].nullable != "YES" {
				t.Errorf("%s nullable = %q, want YES", name, columns[name].nullable)
			}
		}
		for name, want := range map[string]string{
			"id":                "uuid",
			"reporter_id":       "uuid",
			"incident_id":       "uuid",
			"observed_at":       "timestamp with time zone",
			"submitted_at":      "timestamp with time zone",
			"location_accuracy": "double precision",
		} {
			if got := columns[name].dataType; got != want {
				t.Errorf("%s data type = %q, want %q", name, got, want)
			}
		}
	})

	t.Run("spatial columns and indexes", func(t *testing.T) {
		rows, err := connection.Query(ctx, `
			SELECT a.attname, format_type(a.atttypid, a.atttypmod)
			FROM pg_attribute AS a
			JOIN pg_class AS c ON c.oid = a.attrelid
			JOIN pg_namespace AS n ON n.oid = c.relnamespace
			WHERE n.nspname = $1
			  AND c.relname = 'reports'
			  AND a.attname IN ('claimed_location', 'device_location')
			  AND a.attnum > 0
			  AND NOT a.attisdropped
		`, testSchema)
		if err != nil {
			t.Fatalf("query spatial columns: %v", err)
		}
		defer rows.Close()

		locations := map[string]string{}
		for rows.Next() {
			var name, columnType string
			if err := rows.Scan(&name, &columnType); err != nil {
				t.Fatalf("scan spatial column: %v", err)
			}
			locations[name] = columnType
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("iterate spatial columns: %v", err)
		}
		for _, name := range []string{"claimed_location", "device_location"} {
			if got := locations[name]; got != "geography(Point,4326)" {
				t.Errorf("%s type = %q, want geography(Point,4326)", name, got)
			}
		}

		rows, err = connection.Query(ctx, `
			SELECT indexname, indexdef
			FROM pg_indexes
			WHERE schemaname = $1
			  AND tablename IN ('reports', 'outbox_events')
		`, testSchema)
		if err != nil {
			t.Fatalf("query indexes: %v", err)
		}
		defer rows.Close()

		indexes := map[string]string{}
		for rows.Next() {
			var name, definition string
			if err := rows.Scan(&name, &definition); err != nil {
				t.Fatalf("scan index: %v", err)
			}
			indexes[name] = definition
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("iterate indexes: %v", err)
		}
		for _, name := range []string{
			"reports_claimed_location_gist_idx",
			"reports_device_location_gist_idx",
			"reports_incident_id_idx",
			"reports_idempotency_key_unique_idx",
			"outbox_events_unpublished_idx",
		} {
			if _, ok := indexes[name]; !ok {
				t.Errorf("missing index %q", name)
			}
		}
		if !strings.Contains(strings.ToLower(indexes["reports_claimed_location_gist_idx"]), "gist") {
			t.Error("claimed location index is not a GiST index")
		}
		if !strings.Contains(strings.ToLower(indexes["reports_device_location_gist_idx"]), "gist") {
			t.Error("device location index is not a GiST index")
		}
		if definition := strings.ToLower(indexes["reports_idempotency_key_unique_idx"]); !strings.Contains(definition, "unique") || !strings.Contains(definition, "idempotency_key is not null") {
			t.Errorf("idempotency index definition = %q, want unique non-null index", definition)
		}
		if definition := strings.ToLower(indexes["outbox_events_unpublished_idx"]); !strings.Contains(definition, "created_at, id") || !strings.Contains(definition, "published_at is null") {
			t.Errorf("unpublished outbox index definition = %q, want creation ordering and unpublished predicate", definition)
		}
	})

	t.Run("restricted user location schema and indexes", func(t *testing.T) {
		var locationType string
		if err := connection.QueryRow(ctx, `
			SELECT format_type(a.atttypid, a.atttypmod)
			FROM pg_attribute AS a
			JOIN pg_class AS c ON c.oid = a.attrelid
			JOIN pg_namespace AS n ON n.oid = c.relnamespace
			WHERE n.nspname = $1 AND c.relname = 'user_locations'
			  AND a.attname = 'location' AND a.attnum > 0 AND NOT a.attisdropped
		`, testSchema).Scan(&locationType); err != nil {
			t.Fatal(err)
		}
		if locationType != "geography(Point,4326)" {
			t.Fatalf("user location type = %q, want geography(Point,4326)", locationType)
		}
		rows, err := connection.Query(ctx, `
			SELECT indexname, indexdef
			FROM pg_indexes
			WHERE schemaname = $1 AND tablename IN ('user_locations', 'incidents')
		`, testSchema)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		indexes := map[string]string{}
		for rows.Next() {
			var name, definition string
			if err := rows.Scan(&name, &definition); err != nil {
				t.Fatal(err)
			}
			indexes[name] = strings.ToLower(definition)
		}
		for _, name := range []string{"user_locations_location_gist_idx", "incidents_center_point_gist_idx", "incidents_affected_geometry_gist_idx"} {
			if !strings.Contains(indexes[name], "gist") {
				t.Errorf("index %q = %q, want GiST index", name, indexes[name])
			}
		}
	})

	t.Run("unpublished outbox record", func(t *testing.T) {
		rows, err := connection.Query(ctx, `
			SELECT column_name
			FROM information_schema.columns
			WHERE table_schema = $1 AND table_name = 'outbox_events'
		`, testSchema)
		if err != nil {
			t.Fatalf("query outbox columns: %v", err)
		}
		defer rows.Close()
		columns := map[string]bool{}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				t.Fatalf("scan outbox column: %v", err)
			}
			columns[name] = true
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("iterate outbox columns: %v", err)
		}
		for _, name := range []string{
			"id", "event_type", "aggregate_type", "aggregate_id", "payload",
			"created_at", "published_at", "retry_attempts", "last_error",
		} {
			if !columns[name] {
				t.Errorf("missing outbox column %q", name)
			}
		}

		payload := map[string]string{"report_id": "00000000-0000-0000-0000-000000000001"}
		payloadJSON, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}

		var eventID string
		err = connection.QueryRow(ctx, `
			INSERT INTO outbox_events (event_type, aggregate_type, aggregate_id, payload)
			VALUES ('report.created', 'report', $1, $2::jsonb)
			RETURNING id
		`, "00000000-0000-0000-0000-000000000001", payloadJSON).Scan(&eventID)
		if err != nil {
			t.Fatalf("insert outbox event: %v", err)
		}

		var publishedAt *time.Time
		var gotPayload []byte
		err = connection.QueryRow(ctx, `
			SELECT published_at, payload
			FROM outbox_events
			WHERE id = $1 AND published_at IS NULL
		`, eventID).Scan(&publishedAt, &gotPayload)
		if err != nil {
			t.Fatalf("query unpublished outbox event: %v", err)
		}
		if publishedAt != nil {
			t.Fatal("new outbox event has a publication timestamp")
		}
		var gotPayloadMap map[string]string
		if err := json.Unmarshal(gotPayload, &gotPayloadMap); err != nil {
			t.Fatalf("decode stored payload: %v", err)
		}
		if gotPayloadMap["report_id"] != payload["report_id"] {
			t.Fatalf("payload report_id = %q, want %q", gotPayloadMap["report_id"], payload["report_id"])
		}
	})

	if err := connection.Close(ctx); err != nil {
		t.Fatal(err)
	}
	connection, err = pgx.Connect(ctx, testDatabaseURL.String())
	if err != nil {
		t.Fatal(err)
	}
	runGoose(t, ctx, testDatabaseURL.String(), "down")
	runGoose(t, ctx, testDatabaseURL.String(), "down")
	runGoose(t, ctx, testDatabaseURL.String(), "down")
	runGoose(t, ctx, testDatabaseURL.String(), "down")
	runGoose(t, ctx, testDatabaseURL.String(), "down")
	runGoose(t, ctx, testDatabaseURL.String(), "down")
	runGoose(t, ctx, testDatabaseURL.String(), "down")
	runGoose(t, ctx, testDatabaseURL.String(), "down")
	runGoose(t, ctx, testDatabaseURL.String(), "down")
	runGoose(t, ctx, testDatabaseURL.String(), "down")
	assertTableMissing(t, ctx, connection, testSchema, "reports")
	assertTableMissing(t, ctx, connection, testSchema, "outbox_events")
	assertTableMissing(t, ctx, connection, testSchema, "report_media")
	assertTableMissing(t, ctx, connection, testSchema, "report_ai_extractions")
	assertTableMissing(t, ctx, connection, testSchema, "user_locations")
	assertTableMissing(t, ctx, connection, testSchema, "user_location_deletions")
	runGoose(t, ctx, testDatabaseURL.String(), "up")
	runGoose(t, ctx, testDatabaseURL.String(), "up")
	runGoose(t, ctx, testDatabaseURL.String(), "up")
	runGoose(t, ctx, testDatabaseURL.String(), "up")
	runGoose(t, ctx, testDatabaseURL.String(), "up")
	runGoose(t, ctx, testDatabaseURL.String(), "up")
	runGoose(t, ctx, testDatabaseURL.String(), "up")
	runGoose(t, ctx, testDatabaseURL.String(), "up")
	runGoose(t, ctx, testDatabaseURL.String(), "up")
	runGoose(t, ctx, testDatabaseURL.String(), "up")
	assertTableExists(t, ctx, connection, testSchema, "reports")
	assertTableExists(t, ctx, connection, testSchema, "outbox_events")
	assertTableExists(t, ctx, connection, testSchema, "report_media")
	assertTableExists(t, ctx, connection, testSchema, "report_ai_extractions")
	assertTableExists(t, ctx, connection, testSchema, "user_locations")
	assertTableExists(t, ctx, connection, testSchema, "user_location_deletions")
}

func runGoose(t *testing.T, ctx context.Context, databaseURL string, command string) {
	runGooseCommand(t, ctx, databaseURL, command)
}

func runGooseTo(t *testing.T, ctx context.Context, databaseURL string, version string) {
	runGooseCommand(t, ctx, databaseURL, "down-to", version)
}

func runGooseCommand(t *testing.T, ctx context.Context, databaseURL string, command string, args ...string) {
	t.Helper()
	goBinary := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goBinary += ".exe"
	}
	gooseArgs := []string{"run", "github.com/pressly/goose/v3/cmd/goose@v3.27.0", "-dir", ".", "postgres", databaseURL, command}
	gooseArgs = append(gooseArgs, args...)
	goose := exec.CommandContext(ctx, goBinary, gooseArgs...)
	goose.Dir = "."
	output, err := goose.CombinedOutput()
	if err != nil {
		t.Fatalf("goose %s: %v\n%s", command, err, output)
	}
}

func assertTableExists(t *testing.T, ctx context.Context, connection *pgx.Conn, schema string, table string) {
	t.Helper()
	var exists bool
	if err := connection.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, schema+"."+table).Scan(&exists); err != nil {
		t.Fatalf("check table %s: %v", table, err)
	}
	if !exists {
		t.Fatalf("table %s does not exist after migration up", table)
	}
}

func assertTableMissing(t *testing.T, ctx context.Context, connection *pgx.Conn, schema string, table string) {
	t.Helper()
	var exists bool
	if err := connection.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, schema+"."+table).Scan(&exists); err != nil {
		t.Fatalf("check table %s: %v", table, err)
	}
	if exists {
		t.Fatalf("table %s still exists after migration down", table)
	}
}
