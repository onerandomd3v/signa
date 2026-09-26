package migrations

import (
	"os"
	"strings"
	"testing"
)

func TestLatencyIndexMigrationDropsBeforeConcurrentRecreation(t *testing.T) {
	contents, err := os.ReadFile("202609260017_create_latency_lookup_indexes.sql")
	if err != nil {
		t.Fatal(err)
	}
	source := strings.ReplaceAll(string(contents), "\r\n", "\n")
	if !strings.HasPrefix(source, "-- +goose NO TRANSACTION\n") {
		t.Fatal("latency index migration must opt out of transactions")
	}
	parts := strings.SplitN(source, "-- +goose Down", 2)
	if len(parts) != 2 {
		t.Fatal("latency index migration must define Down")
	}
	up := parts[0]
	down := parts[1]
	indexes := []struct {
		name       string
		definition string
	}{
		{"outbox_events_aggregate_event_idx", "ON outbox_events (aggregate_type, aggregate_id, event_type, created_at, id)"},
		{"deliveries_alert_created_idx", "ON deliveries (alert_id, created_at, id)"},
		{"reports_recent_created_idx", "ON reports (created_at DESC, id DESC)"},
	}
	for _, index := range indexes {
		t.Run(index.name, func(t *testing.T) {
			drop := "DROP INDEX CONCURRENTLY IF EXISTS " + index.name + ";"
			create := "CREATE INDEX CONCURRENTLY " + index.name
			dropAt, createAt := strings.Index(up, drop), strings.Index(up, create)
			if dropAt < 0 || createAt < 0 || dropAt >= createAt {
				t.Fatalf("Up must drop %s concurrently before recreating it", index.name)
			}
			if strings.Contains(up[createAt:strings.Index(up[createAt:], ";")+createAt], "IF NOT EXISTS") {
				t.Fatalf("Up must not leave a potentially invalid %s in place with IF NOT EXISTS", index.name)
			}
			if !strings.Contains(up[createAt:], index.definition) {
				t.Fatalf("Up definition for %s does not contain %q", index.name, index.definition)
			}
			if !strings.Contains(down, drop) {
				t.Fatalf("Down must drop %s concurrently if present", index.name)
			}
		})
	}
}

func TestAIProcessingMigrationContainsOnlyItsTable(t *testing.T) {
	contents, err := os.ReadFile("202609260016_create_report_ai_processing.sql")
	if err != nil {
		t.Fatal(err)
	}
	source := string(contents)
	if !strings.Contains(source, "CREATE TABLE report_ai_processing") || !strings.Contains(source, "DROP TABLE IF EXISTS report_ai_processing") {
		t.Fatal("AI processing migration must create and drop its durable table")
	}
	if strings.Contains(strings.ToUpper(source), "CREATE INDEX") || strings.Contains(strings.ToUpper(source), "DROP INDEX") {
		t.Fatal("AI processing table migration must not create or drop indexes on existing tables")
	}
}
