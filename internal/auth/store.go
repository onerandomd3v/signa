package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type DB interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type PostgresStore struct {
	db DB
}

func NewPostgresStore(db DB) *PostgresStore {
	return &PostgresStore{db: db}
}

func (s *PostgresStore) Lookup(ctx context.Context, secret string) (Principal, error) {
	hash, err := hashSecret(secret)
	if err != nil {
		return Principal{}, ErrUnauthenticated
	}
	var userID uuid.UUID
	err = s.db.QueryRow(ctx, `
		SELECT user_id
		FROM auth_sessions
		WHERE token_hash = $1
		  AND revoked_at IS NULL
		  AND expires_at > now()
	`, hash).Scan(&userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Principal{}, ErrUnauthenticated
		}
		return Principal{}, fmt.Errorf("%w: %v", ErrSessionStoreUnavailable, err)
	}
	return Principal{UserID: userID}, nil
}

type RevocableSessionStore interface {
	SessionStore
	Revoke(ctx context.Context, secret string) error
}

type CreatedSession struct {
	Secret    string
	ExpiresAt time.Time
}

func (s *PostgresStore) Create(ctx context.Context, userID uuid.UUID, ttl time.Duration) (CreatedSession, error) {
	if userID == uuid.Nil || ttl <= 0 {
		return CreatedSession{}, errors.New("invalid session creation arguments")
	}
	secret, err := NewSessionSecret()
	if err != nil {
		return CreatedSession{}, err
	}
	expiresAt := time.Now().UTC().Add(ttl)
	hash, err := hashSecret(secret)
	if err != nil {
		return CreatedSession{}, err
	}
	if _, err := s.db.Exec(ctx, `
		INSERT INTO auth_sessions (id, user_id, token_hash, expires_at)
		VALUES ($1, $2, $3, $4)
	`, uuid.New(), userID, hash, expiresAt); err != nil {
		return CreatedSession{}, err
	}
	return CreatedSession{Secret: secret, ExpiresAt: expiresAt}, nil
}

func (s *PostgresStore) Revoke(ctx context.Context, secret string) error {
	hash, err := hashSecret(secret)
	if err != nil {
		return ErrUnauthenticated
	}
	_, err = s.db.Exec(ctx, `
		UPDATE auth_sessions
		SET revoked_at = now()
		WHERE token_hash = $1 AND revoked_at IS NULL
	`, hash)
	return err
}
