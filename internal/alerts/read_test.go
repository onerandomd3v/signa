package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onerandomd3v/signa/internal/priority"
)

type readFakeRow struct {
	values []any
	err    error
}

func (r readFakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	for i := range dest {
		reflect.ValueOf(dest[i]).Elem().Set(reflect.ValueOf(r.values[i]))
	}
	return nil
}

type readFakeDB struct {
	row   pgx.Row
	query string
	args  []any
}

func (db *readFakeDB) QueryRow(_ context.Context, query string, args ...any) pgx.Row {
	db.query = query
	db.args = args
	return db.row
}

func TestReadAuthorizedReturnsSafeImmutableSnapshot(t *testing.T) {
	alertID := uuid.New()
	incidentID := uuid.New()
	userID := uuid.New()
	createdAt := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	asOf := createdAt.Add(-time.Minute)
	supersedes := uuid.New()
	db := &readFakeDB{row: readFakeRow{values: []any{
		alertID, incidentID, AlertTypeImmediate, "EMERGING", "HIGH", "RESOLVED", priority.P1, FreshnessStale,
		"older safe summary", asOf, createdAt, &supersedes,
	}}}

	got, err := readAuthorized(context.Background(), db, userID, alertID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != alertID || got.IncidentID != incidentID || got.PrioritySnapshot != priority.P1 || got.StatusSnapshot != "RESOLVED" || got.FreshnessSnapshot != FreshnessStale || got.SupersedesAlertID == nil || *got.SupersedesAlertID != supersedes {
		t.Fatalf("read snapshot = %+v", got)
	}
	if len(db.args) != 2 || db.args[0] != alertID || db.args[1] != userID {
		t.Fatalf("query args = %#v, want alert and authenticated user IDs", db.args)
	}

	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	body := string(encoded)
	for _, forbidden := range []string{"provider_response", "subscription", "endpoint", "reporter", "coordinates", "request_fingerprint", "eligibility_reasons", "idempotency_key"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("safe alert response contains forbidden field %q: %s", forbidden, body)
		}
	}
}

func TestReadAuthorizedMapsMissingVisibilityToPrivacySafeError(t *testing.T) {
	db := &readFakeDB{row: readFakeRow{err: pgx.ErrNoRows}}
	_, err := readAuthorized(context.Background(), db, uuid.New(), uuid.New())
	if !errors.Is(err, ErrAlertNotVisible) {
		t.Fatalf("error = %v, want ErrAlertNotVisible", err)
	}
}
