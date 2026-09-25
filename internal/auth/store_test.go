package auth

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type fakeRow struct {
	values []any
	err    error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	for i := range dest {
		reflect.ValueOf(dest[i]).Elem().Set(reflect.ValueOf(r.values[i]))
	}
	return nil
}

type fakeDB struct {
	row       pgx.Row
	queries   []string
	arguments [][]any
}

func (db *fakeDB) QueryRow(_ context.Context, query string, args ...any) pgx.Row {
	db.queries = append(db.queries, query)
	db.arguments = append(db.arguments, args)
	return db.row
}

func (db *fakeDB) Exec(_ context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	db.queries = append(db.queries, query)
	db.arguments = append(db.arguments, args)
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}

func TestPostgresStoreLookupHashesSecretAndResolvesPrincipal(t *testing.T) {
	secret, err := NewSessionSecret()
	if err != nil {
		t.Fatal(err)
	}
	userID := uuid.New()
	db := &fakeDB{row: fakeRow{values: []any{userID}}}
	principal, err := NewPostgresStore(db).Lookup(context.Background(), secret)
	if err != nil {
		t.Fatal(err)
	}
	if principal.UserID != userID {
		t.Fatalf("user id = %s, want %s", principal.UserID, userID)
	}
	if strings.Contains(string(db.arguments[0][0].([]byte)), secret) {
		t.Fatal("raw session secret was passed to lookup")
	}
	if len(db.arguments[0][0].([]byte)) != 32 {
		t.Fatalf("token hash length = %d, want 32", len(db.arguments[0][0].([]byte)))
	}
}

func TestPostgresStoreRejectsMalformedExpiredOrRevokedLookup(t *testing.T) {
	db := &fakeDB{row: fakeRow{err: errors.New("no rows")}}
	_, err := NewPostgresStore(db).Lookup(context.Background(), "malformed")
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("lookup error = %v, want unauthenticated", err)
	}
	if len(db.queries) != 0 {
		t.Fatal("malformed credential reached the database")
	}
}

func TestPostgresStorePreservesOperationalLookupFailure(t *testing.T) {
	db := &fakeDB{row: fakeRow{err: errors.New("connection refused")}}
	_, err := NewPostgresStore(db).Lookup(context.Background(), func() string {
		secret, secretErr := NewSessionSecret()
		if secretErr != nil {
			t.Fatal(secretErr)
		}
		return secret
	}())
	if !errors.Is(err, ErrSessionStoreUnavailable) {
		t.Fatalf("lookup error = %v, want ErrSessionStoreUnavailable", err)
	}
}

func TestPostgresStoreCreatesAndRevokesOpaqueSession(t *testing.T) {
	db := &fakeDB{}
	store := NewPostgresStore(db)
	userID := uuid.New()
	created, err := store.Create(context.Background(), userID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if created.Secret == "" || created.ExpiresAt.Before(time.Now()) {
		t.Fatalf("created session = %#v", created)
	}
	if strings.Contains(db.queries[0], created.Secret) {
		t.Fatal("raw session secret was included in insert SQL")
	}
	if string(db.arguments[0][2].([]byte)) == created.Secret {
		t.Fatal("raw session secret was stored")
	}
	if err := store.Revoke(context.Background(), created.Secret); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(db.queries[1], "revoked_at") {
		t.Fatalf("revoke query = %q", db.queries[1])
	}
}
