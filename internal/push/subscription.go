package push

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrInvalidSubscription  = errors.New("push subscription is invalid")
	ErrSubscriptionConflict = errors.New("push subscription belongs to another user")
	ErrSubscriptionNotFound = errors.New("push subscription was not found")
)

type SubscriptionInput struct {
	Endpoint string
	P256DH   string
	Auth     string
}

type Subscription struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Endpoint  string
	P256DH    string
	Auth      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type SubscriptionWriter interface {
	Upsert(context.Context, uuid.UUID, SubscriptionInput) (Subscription, bool, error)
	Delete(context.Context, uuid.UUID, uuid.UUID) error
}

type DeliveryStore interface {
	ListForDelivery(context.Context, uuid.UUID) ([]Subscription, error)
	RemoveExpired(context.Context, uuid.UUID, uuid.UUID) error
}

type SubscriptionStore interface {
	SubscriptionWriter
	DeliveryStore
}

func ValidateSubscription(input SubscriptionInput) error {
	input.Endpoint = strings.TrimSpace(input.Endpoint)
	input.P256DH = strings.TrimSpace(input.P256DH)
	input.Auth = strings.TrimSpace(input.Auth)
	parsed, err := url.Parse(input.Endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("%w: endpoint must be an HTTPS URL", ErrInvalidSubscription)
	}
	publicKey, err := decodeKey(input.P256DH)
	if err != nil || len(publicKey) != 65 || publicKey[0] != 4 {
		return fmt.Errorf("%w: p256dh must be an uncompressed P-256 public key", ErrInvalidSubscription)
	}
	authKey, err := decodeKey(input.Auth)
	if err != nil || len(authKey) != 16 {
		return fmt.Errorf("%w: auth must be a 16-byte key", ErrInvalidSubscription)
	}
	return nil
}

func decodeKey(value string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(value)
}

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) Upsert(ctx context.Context, userID uuid.UUID, input SubscriptionInput) (Subscription, bool, error) {
	if s == nil || s.pool == nil {
		return Subscription{}, false, errors.New("push subscription database is required")
	}
	if userID == uuid.Nil {
		return Subscription{}, false, errors.New("push subscription user is required")
	}
	input.Endpoint = strings.TrimSpace(input.Endpoint)
	input.P256DH = strings.TrimSpace(input.P256DH)
	input.Auth = strings.TrimSpace(input.Auth)
	if err := ValidateSubscription(input); err != nil {
		return Subscription{}, false, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Subscription{}, false, fmt.Errorf("begin push subscription upsert: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var subscription Subscription
	var createdAt, updatedAt time.Time
	var created bool
	err = tx.QueryRow(ctx, `
		INSERT INTO push_subscriptions (user_id, endpoint, p256dh, auth)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (endpoint) DO UPDATE SET
			p256dh = EXCLUDED.p256dh,
			auth = EXCLUDED.auth,
			updated_at = now()
		WHERE push_subscriptions.user_id = EXCLUDED.user_id
		RETURNING id, user_id, endpoint, p256dh, auth, created_at, updated_at, (xmax = 0)
	`, userID, input.Endpoint, input.P256DH, input.Auth).Scan(
		&subscription.ID, &subscription.UserID, &subscription.Endpoint,
		&subscription.P256DH, &subscription.Auth, &createdAt, &updatedAt, &created,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Subscription{}, false, ErrSubscriptionConflict
	}
	if err != nil {
		return Subscription{}, false, fmt.Errorf("upsert push subscription: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Subscription{}, false, fmt.Errorf("commit push subscription upsert: %w", err)
	}
	subscription.CreatedAt = createdAt.UTC()
	subscription.UpdatedAt = updatedAt.UTC()
	return subscription, created, nil
}

func (s *Store) Delete(ctx context.Context, userID, subscriptionID uuid.UUID) error {
	if s == nil || s.pool == nil {
		return errors.New("push subscription database is required")
	}
	if userID == uuid.Nil || subscriptionID == uuid.Nil {
		return ErrSubscriptionNotFound
	}
	result, err := s.pool.Exec(ctx, `DELETE FROM push_subscriptions WHERE id = $1 AND user_id = $2`, subscriptionID, userID)
	if err != nil {
		return fmt.Errorf("delete push subscription: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrSubscriptionNotFound
	}
	return nil
}

func (s *Store) ListForDelivery(ctx context.Context, userID uuid.UUID) ([]Subscription, error) {
	if s == nil || s.pool == nil {
		return nil, errors.New("push subscription database is required")
	}
	if userID == uuid.Nil {
		return nil, errors.New("push subscription user is required")
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_id, endpoint, p256dh, auth, created_at, updated_at
		FROM push_subscriptions
		WHERE user_id = $1
		ORDER BY id
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list push subscriptions: %w", err)
	}
	defer rows.Close()
	var subscriptions []Subscription
	for rows.Next() {
		var item Subscription
		if err := rows.Scan(&item.ID, &item.UserID, &item.Endpoint, &item.P256DH, &item.Auth, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan push subscription: %w", err)
		}
		item.CreatedAt = item.CreatedAt.UTC()
		item.UpdatedAt = item.UpdatedAt.UTC()
		subscriptions = append(subscriptions, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate push subscriptions: %w", err)
	}
	return subscriptions, nil
}

func (s *Store) RemoveExpired(ctx context.Context, userID, subscriptionID uuid.UUID) error {
	if s == nil || s.pool == nil {
		return errors.New("push subscription database is required")
	}
	if userID == uuid.Nil || subscriptionID == uuid.Nil {
		return nil
	}
	_, err := s.pool.Exec(ctx, `DELETE FROM push_subscriptions WHERE id = $1 AND user_id = $2`, subscriptionID, userID)
	if err != nil {
		return fmt.Errorf("remove expired push subscription: %w", err)
	}
	return nil
}
