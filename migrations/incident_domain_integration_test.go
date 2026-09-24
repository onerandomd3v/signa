//go:build integration

package migrations

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestIncidentDomainMigration(t *testing.T) {
	databaseURL := os.Getenv("SIGNA_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = defaultDatabaseURL
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	parsedDatabaseURL, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatalf("parse PostgreSQL URL: %v", err)
	}
	testDatabaseName := fmt.Sprintf("signa_incident_migration_test_%d", time.Now().UnixNano())
	maintenanceURL := *parsedDatabaseURL
	maintenanceURL.Path = "/postgres"
	maintenance, err := pgx.Connect(ctx, maintenanceURL.String())
	if err != nil {
		t.Fatalf("connect to PostgreSQL maintenance database: %v", err)
	}
	defer func() {
		_, _ = maintenance.Exec(context.Background(), `DROP DATABASE IF EXISTS `+testDatabaseName)
		_ = maintenance.Close(context.Background())
	}()
	if _, err := maintenance.Exec(ctx, `CREATE DATABASE `+testDatabaseName); err != nil {
		t.Fatalf("create isolated migration database: %v", err)
	}

	testDatabaseURL := *parsedDatabaseURL
	testDatabaseURL.Path = "/" + testDatabaseName
	runGoose(t, ctx, testDatabaseURL.String(), "up")
	connection, err := pgx.Connect(ctx, testDatabaseURL.String())
	if err != nil {
		t.Fatalf("connect to isolated migration database: %v", err)
	}
	defer func() { _ = connection.Close(context.Background()) }()

	for _, table := range []string{"incidents", "incident_reports", "incident_state_history"} {
		assertTableExists(t, ctx, connection, "public", table)
	}

	t.Run("schema and spatial types", func(t *testing.T) {
		rows, err := connection.Query(ctx, `
			SELECT table_name, column_name, is_nullable, format_type(a.atttypid, a.atttypmod)
			FROM information_schema.columns AS c
			JOIN pg_attribute AS a ON a.attname = c.column_name
			JOIN pg_class AS pc ON pc.oid = a.attrelid AND pc.relname = c.table_name
			JOIN pg_namespace AS pn ON pn.oid = pc.relnamespace AND pn.nspname = c.table_schema
			WHERE c.table_schema = 'public'
			  AND c.table_name IN ('incidents', 'incident_reports', 'incident_state_history')
			  AND a.attnum > 0 AND NOT a.attisdropped
		`)
		if err != nil {
			t.Fatalf("query incident columns: %v", err)
		}
		defer rows.Close()
		type columnMetadata struct {
			nullable string
			dataType string
		}
		columns := map[string]columnMetadata{}
		for rows.Next() {
			var tableName, columnName, nullable, dataType string
			if err := rows.Scan(&tableName, &columnName, &nullable, &dataType); err != nil {
				t.Fatalf("scan incident column: %v", err)
			}
			columns[tableName+"."+columnName] = columnMetadata{nullable: nullable, dataType: dataType}
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("iterate incident columns: %v", err)
		}
		required := map[string]columnMetadata{
			"incidents.id": {"NO", "uuid"}, "incidents.event_type": {"YES", "text"},
			"incidents.status": {"NO", "text"}, "incidents.confidence_state": {"NO", "text"},
			"incidents.severity": {"YES", "text"}, "incidents.center_point": {"YES", "geography(Point,4326)"},
			"incidents.affected_geometry": {"YES", "geography(Geometry,4326)"},
			"incidents.started_at":        {"YES", "timestamp with time zone"}, "incidents.last_signal_at": {"YES", "timestamp with time zone"},
			"incidents.expires_at": {"YES", "timestamp with time zone"}, "incidents.resolved_at": {"YES", "timestamp with time zone"},
			"incidents.created_at": {"NO", "timestamp with time zone"}, "incidents.updated_at": {"NO", "timestamp with time zone"},
			"incident_reports.incident_id": {"NO", "uuid"}, "incident_reports.report_id": {"NO", "uuid"},
			"incident_reports.similarity": {"YES", "double precision"}, "incident_reports.independence_weight": {"YES", "double precision"},
			"incident_reports.independence_state":  {"YES", "text"},
			"incident_reports.contradiction_state": {"YES", "text"}, "incident_reports.attached_at": {"NO", "timestamp with time zone"},
			"incident_state_history.id": {"NO", "uuid"}, "incident_state_history.incident_id": {"NO", "uuid"},
			"incident_state_history.changed_field": {"NO", "text"}, "incident_state_history.previous_value": {"YES", "text"},
			"incident_state_history.new_value": {"NO", "text"}, "incident_state_history.reason": {"YES", "text"},
			"incident_state_history.actor_type": {"YES", "text"}, "incident_state_history.actor_id": {"YES", "text"},
			"incident_state_history.rule_version": {"YES", "text"}, "incident_state_history.metadata": {"NO", "jsonb"},
			"incident_state_history.transitioned_at": {"NO", "timestamp with time zone"},
		}
		for name, want := range required {
			got, ok := columns[name]
			if !ok {
				t.Errorf("missing incident column %q", name)
				continue
			}
			if got != want {
				t.Errorf("%s metadata = %#v, want %#v", name, got, want)
			}
		}
	})

	t.Run("links, nullable report relationship, and history", func(t *testing.T) {
		var incidentID, reportID, secondReportID string
		if err := connection.QueryRow(ctx, `
			INSERT INTO incidents (event_type, status, confidence_state, center_point, affected_geometry)
			VALUES ('road_blockage', 'UNDECIDED', 'UNDECIDED', ST_SetSRID(ST_MakePoint(3.4, 6.5), 4326)::geography, ST_Buffer(ST_SetSRID(ST_MakePoint(3.4, 6.5), 4326)::geography, 100))
			RETURNING id
		`).Scan(&incidentID); err != nil {
			t.Fatalf("insert incident: %v", err)
		}
		if err := connection.QueryRow(ctx, `INSERT INTO reports (raw_text) VALUES ('standalone report') RETURNING id`).Scan(&reportID); err != nil {
			t.Fatalf("insert standalone report: %v", err)
		}
		if err := connection.QueryRow(ctx, `INSERT INTO reports (raw_text) VALUES ('attached report') RETURNING id`).Scan(&secondReportID); err != nil {
			t.Fatalf("insert second report: %v", err)
		}
		if _, err := connection.Exec(ctx, `UPDATE reports SET incident_id = $1 WHERE id = $2`, incidentID, secondReportID); err != nil {
			t.Fatalf("attach report through nullable incident_id: %v", err)
		}
		if _, err := connection.Exec(ctx, `
			INSERT INTO incident_reports (incident_id, report_id, similarity, independence_weight, contradiction_state)
			VALUES ($1, $2, $3, $4, $5), ($1, $6, NULL, NULL, NULL)
		`, incidentID, reportID, 0.75, 0.5, "UNKNOWN", secondReportID); err != nil {
			t.Fatalf("insert incident report links: %v", err)
		}
		if _, err := connection.Exec(ctx, `INSERT INTO incident_reports (incident_id, report_id) VALUES ($1, $2)`, incidentID, reportID); err == nil {
			t.Fatal("duplicate incident report link succeeded")
		}
		if _, err := connection.Exec(ctx, `INSERT INTO incident_reports (incident_id, report_id) VALUES ('00000000-0000-0000-0000-000000000001', $1)`, reportID); err == nil {
			t.Fatal("invalid incident foreign key succeeded")
		}
		if _, err := connection.Exec(ctx, `INSERT INTO incident_reports (incident_id, report_id) VALUES ($1, '00000000-0000-0000-0000-000000000001')`, incidentID); err == nil {
			t.Fatal("invalid report foreign key succeeded")
		}

		var defaultMetadata []byte
		if err := connection.QueryRow(ctx, `
			INSERT INTO incident_state_history (incident_id, changed_field, new_value, transitioned_at)
			VALUES ($1, 'status', 'UNDECIDED', '2026-09-23T10:00:00Z') RETURNING metadata
		`, incidentID).Scan(&defaultMetadata); err != nil {
			t.Fatalf("insert default state history: %v", err)
		}
		if string(defaultMetadata) != `{}` {
			t.Fatalf("default history metadata = %s, want {}", defaultMetadata)
		}
		if _, err := connection.Exec(ctx, `
			INSERT INTO incident_state_history (incident_id, changed_field, previous_value, new_value, reason, actor_type, actor_id, rule_version, metadata, transitioned_at)
			VALUES ($1, 'confidence_state', 'UNDECIDED', 'EMERGING', 'new signal', 'service', 'incident-worker', 'v1', '{"source":"test"}', '2026-09-23T10:01:00Z')
		`, incidentID); err != nil {
			t.Fatalf("insert second state history: %v", err)
		}
		var firstField, secondField string
		if err := connection.QueryRow(ctx, `SELECT changed_field FROM incident_state_history WHERE incident_id = $1 ORDER BY transitioned_at, id LIMIT 1`, incidentID).Scan(&firstField); err != nil {
			t.Fatalf("query first history row: %v", err)
		}
		if err := connection.QueryRow(ctx, `SELECT changed_field FROM incident_state_history WHERE incident_id = $1 ORDER BY transitioned_at, id OFFSET 1 LIMIT 1`, incidentID).Scan(&secondField); err != nil {
			t.Fatalf("query second history row: %v", err)
		}
		if firstField != "status" || secondField != "confidence_state" {
			t.Fatalf("history order = %q, %q; want status, confidence_state", firstField, secondField)
		}

		var linkCount, historyCount, reportCount int
		if err := connection.QueryRow(ctx, `SELECT count(*) FROM incident_reports WHERE incident_id = $1`, incidentID).Scan(&linkCount); err != nil {
			t.Fatalf("count incident links: %v", err)
		}
		if linkCount != 2 {
			t.Fatalf("incident link count = %d, want 2", linkCount)
		}
		if err := connection.QueryRow(ctx, `SELECT count(*) FROM incident_state_history WHERE incident_id = $1`, incidentID).Scan(&historyCount); err != nil {
			t.Fatalf("count state history: %v", err)
		}
		if historyCount != 2 {
			t.Fatalf("state history count = %d, want 2", historyCount)
		}
		if err := connection.QueryRow(ctx, `SELECT count(*) FROM reports`).Scan(&reportCount); err != nil {
			t.Fatalf("count reports: %v", err)
		}
		if reportCount != 2 {
			t.Fatalf("report count = %d, want 2", reportCount)
		}

		if _, err := connection.Exec(ctx, `DELETE FROM incidents WHERE id = $1`, incidentID); err == nil {
			t.Fatal("incident with audit history was deleted")
		}
		var nullableIncidentID *string
		if err := connection.QueryRow(ctx, `SELECT incident_id FROM reports WHERE id = $1`, secondReportID).Scan(&nullableIncidentID); err != nil {
			t.Fatalf("read report incident after rejected deletion: %v", err)
		}
		if nullableIncidentID == nil || *nullableIncidentID != incidentID {
			t.Fatalf("report incident_id after rejected deletion = %v, want %q", nullableIncidentID, incidentID)
		}
		if err := connection.QueryRow(ctx, `SELECT count(*) FROM incident_state_history WHERE incident_id = $1`, incidentID).Scan(&historyCount); err != nil {
			t.Fatalf("count history after rejected deletion: %v", err)
		}
		if historyCount != 2 {
			t.Fatalf("state history after rejected deletion = %d, want 2", historyCount)
		}

		var cleanupIncidentID, cleanupReportID string
		if err := connection.QueryRow(ctx, `INSERT INTO incidents (status, confidence_state) VALUES ('UNDECIDED', 'UNDECIDED') RETURNING id`).Scan(&cleanupIncidentID); err != nil {
			t.Fatalf("insert cleanup incident: %v", err)
		}
		if err := connection.QueryRow(ctx, `INSERT INTO reports (raw_text) VALUES ('cleanup report') RETURNING id`).Scan(&cleanupReportID); err != nil {
			t.Fatalf("insert cleanup report: %v", err)
		}
		if _, err := connection.Exec(ctx, `UPDATE reports SET incident_id = $1 WHERE id = $2`, cleanupIncidentID, cleanupReportID); err != nil {
			t.Fatalf("attach cleanup report: %v", err)
		}
		if _, err := connection.Exec(ctx, `INSERT INTO incident_reports (incident_id, report_id) VALUES ($1, $2)`, cleanupIncidentID, cleanupReportID); err != nil {
			t.Fatalf("insert cleanup incident link: %v", err)
		}
		if _, err := connection.Exec(ctx, `DELETE FROM incidents WHERE id = $1`, cleanupIncidentID); err != nil {
			t.Fatalf("delete incident without history: %v", err)
		}
		if err := connection.QueryRow(ctx, `SELECT incident_id FROM reports WHERE id = $1`, cleanupReportID).Scan(&nullableIncidentID); err != nil {
			t.Fatalf("read cleanup report incident: %v", err)
		}
		if nullableIncidentID != nil {
			t.Fatalf("cleanup report incident_id = %q after incident deletion, want NULL", *nullableIncidentID)
		}
		if err := connection.QueryRow(ctx, `SELECT count(*) FROM incident_reports WHERE incident_id = $1`, cleanupIncidentID).Scan(&linkCount); err != nil {
			t.Fatalf("count cleanup links after incident deletion: %v", err)
		}
		if linkCount != 0 {
			t.Fatalf("cleanup incident links after deletion = %d, want 0", linkCount)
		}
		if err := connection.QueryRow(ctx, `SELECT count(*) FROM reports`).Scan(&reportCount); err != nil {
			t.Fatalf("count reports after cleanup incident deletion: %v", err)
		}
		if reportCount != 3 {
			t.Fatalf("reports after cleanup incident deletion = %d, want 3", reportCount)
		}
	})

	// Rolling back only the incident migration must leave the previous report schema healthy.
	runGoose(t, ctx, testDatabaseURL.String(), "down")
	runGoose(t, ctx, testDatabaseURL.String(), "down")
	runGoose(t, ctx, testDatabaseURL.String(), "down")
	runGoose(t, ctx, testDatabaseURL.String(), "down")
	assertTableMissing(t, ctx, connection, "public", "incidents")
	assertTableExists(t, ctx, connection, "public", "reports")
	runGoose(t, ctx, testDatabaseURL.String(), "up")
	runGoose(t, ctx, testDatabaseURL.String(), "up")
	runGoose(t, ctx, testDatabaseURL.String(), "up")
	runGoose(t, ctx, testDatabaseURL.String(), "up")
	assertTableExists(t, ctx, connection, "public", "incidents")
}
