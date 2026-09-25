package push

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrInvalidSubscription     = errors.New("push subscription is invalid")
	ErrSubscriptionConflict    = errors.New("push subscription belongs to another user")
	ErrSubscriptionNotFound    = errors.New("push subscription was not found")
	ErrDeliveryResultNotFound  = errors.New("push delivery result was not found")
	ErrDeliveryResultClaimLost = errors.New("push delivery result claim is no longer active")
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

type DeliveryResultState string

const (
	DeliveryResultPending   DeliveryResultState = "PENDING"
	DeliveryResultSucceeded DeliveryResultState = "SUCCEEDED"
	DeliveryResultExpired   DeliveryResultState = "EXPIRED"
	DeliveryResultPermanent DeliveryResultState = "PERMANENT"
	DeliveryResultRetryable DeliveryResultState = "RETRYABLE"
)

type DeliveryResult struct {
	DeliveryID       uuid.UUID
	SubscriptionID   uuid.UUID
	State            DeliveryResultState
	ProviderResponse string
	ErrorMessage     string
}

type SubscriptionWriter interface {
	Upsert(context.Context, uuid.UUID, SubscriptionInput) (Subscription, bool, error)
	Delete(context.Context, uuid.UUID, uuid.UUID) error
}

type DeliveryStore interface {
	ListForDelivery(context.Context, uuid.UUID) ([]Subscription, error)
	RemoveExpired(context.Context, uuid.UUID, uuid.UUID) error
	EnsureDeliveryResults(context.Context, uuid.UUID, uuid.UUID, []Subscription) error
	LoadDeliveryResults(context.Context, uuid.UUID) (map[uuid.UUID]DeliveryResult, error)
	ClaimDeliveryResult(context.Context, uuid.UUID, uuid.UUID, time.Duration) (uuid.UUID, bool, error)
	CompleteDeliveryResult(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, DeliveryResultState, string, string) error
	CompleteExpiredDeliveryResult(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID, string, string) error
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
	if isProhibitedLiteralIP(parsed.Hostname()) {
		return fmt.Errorf("%w: endpoint must target a public HTTPS destination", ErrInvalidSubscription)
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

func isProhibitedLiteralIP(host string) bool {
	if strings.Contains(host, "%") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && !isPublicIP(ip)
}

func isPublicIP(ip net.IP) bool {
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	for _, network := range nonPublicEndpointCIDRs {
		if network.Contains(ip) {
			return false
		}
	}
	return true
}

var nonPublicEndpointCIDRs = mustParseEndpointCIDRs([]string{
	"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4",
	"2001:2::/48", "2001:10::/28", "2001:db8::/32",
})

func mustParseEndpointCIDRs(values []string) []*net.IPNet {
	result := make([]*net.IPNet, 0, len(values))
	for _, value := range values {
		_, network, err := net.ParseCIDR(value)
		if err != nil {
			panic(err)
		}
		result = append(result, network)
	}
	return result
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

func (s *Store) EnsureDeliveryResults(ctx context.Context, deliveryID, userID uuid.UUID, subscriptions []Subscription) error {
	if s == nil || s.pool == nil {
		return errors.New("push subscription database is required")
	}
	if deliveryID == uuid.Nil || userID == uuid.Nil {
		return errors.New("push delivery result identity is required")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin push delivery result initialization: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, subscription := range subscriptions {
		if subscription.ID == uuid.Nil {
			return fmt.Errorf("push delivery result subscription identity is required")
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO web_push_delivery_results (delivery_id, subscription_id, state)
			SELECT $1, $2, 'PENDING'
			WHERE EXISTS (
				SELECT 1 FROM push_subscriptions WHERE id = $2 AND user_id = $3
			)
			ON CONFLICT (delivery_id, subscription_id) DO NOTHING
		`, deliveryID, subscription.ID, userID); err != nil {
			return fmt.Errorf("initialize push delivery result: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit push delivery result initialization: %w", err)
	}
	return nil
}

func (s *Store) LoadDeliveryResults(ctx context.Context, deliveryID uuid.UUID) (map[uuid.UUID]DeliveryResult, error) {
	if s == nil || s.pool == nil {
		return nil, errors.New("push subscription database is required")
	}
	if deliveryID == uuid.Nil {
		return nil, errors.New("push delivery result delivery identity is required")
	}
	rows, err := s.pool.Query(ctx, `
		SELECT delivery_id, subscription_id, state, COALESCE(provider_response, ''), COALESCE(error_message, '')
		FROM web_push_delivery_results
		WHERE delivery_id = $1
	`, deliveryID)
	if err != nil {
		return nil, fmt.Errorf("load push delivery results: %w", err)
	}
	defer rows.Close()
	results := make(map[uuid.UUID]DeliveryResult)
	for rows.Next() {
		var result DeliveryResult
		if err := rows.Scan(&result.DeliveryID, &result.SubscriptionID, &result.State, &result.ProviderResponse, &result.ErrorMessage); err != nil {
			return nil, fmt.Errorf("scan push delivery result: %w", err)
		}
		results[result.SubscriptionID] = result
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate push delivery results: %w", err)
	}
	return results, nil
}

func (s *Store) ClaimDeliveryResult(ctx context.Context, deliveryID, subscriptionID uuid.UUID, lease time.Duration) (uuid.UUID, bool, error) {
	if s == nil || s.pool == nil {
		return uuid.Nil, false, errors.New("push subscription database is required")
	}
	if deliveryID == uuid.Nil || subscriptionID == uuid.Nil {
		return uuid.Nil, false, errors.New("push delivery result identity is required")
	}
	if lease <= 0 {
		lease = 5 * time.Minute
	}
	claimToken := uuid.New()
	result, err := s.pool.Exec(ctx, `
		UPDATE web_push_delivery_results
		SET claimed_until = now() + ($3::bigint * interval '1 microsecond'), claim_token = $4
		WHERE delivery_id = $1
		  AND subscription_id = $2
		  AND state IN ('PENDING', 'RETRYABLE')
		  AND (claimed_until IS NULL OR claimed_until < now())
	`, deliveryID, subscriptionID, lease.Microseconds(), claimToken)
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("claim push delivery result: %w", err)
	}
	if result.RowsAffected() != 1 {
		return uuid.Nil, false, nil
	}
	return claimToken, true, nil
}

func (s *Store) CompleteDeliveryResult(ctx context.Context, deliveryID, subscriptionID, claimToken uuid.UUID, state DeliveryResultState, providerResponse, errorMessage string) error {
	if s == nil || s.pool == nil {
		return errors.New("push subscription database is required")
	}
	if claimToken == uuid.Nil {
		return ErrDeliveryResultClaimLost
	}
	if !validDeliveryResultState(state) || state == DeliveryResultPending {
		return fmt.Errorf("invalid push delivery result state %q", state)
	}
	result, err := s.pool.Exec(ctx, `
		UPDATE web_push_delivery_results
		SET state = $4,
		    provider_response = NULLIF($5, ''),
		    error_message = NULLIF($6, ''),
		    claimed_until = NULL,
		    claim_token = NULL,
		    completed_at = CASE WHEN $4 IN ('SUCCEEDED', 'EXPIRED', 'PERMANENT') THEN now() ELSE NULL END,
		    updated_at = now()
		WHERE delivery_id = $1 AND subscription_id = $2 AND claim_token = $3 AND claimed_until > now()
	`, deliveryID, subscriptionID, claimToken, state, providerResponse, errorMessage)
	if err != nil {
		return fmt.Errorf("complete push delivery result: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrDeliveryResultClaimLost
	}
	return nil
}

func (s *Store) CompleteExpiredDeliveryResult(ctx context.Context, deliveryID, userID, subscriptionID, claimToken uuid.UUID, providerResponse, errorMessage string) error {
	if s == nil || s.pool == nil {
		return errors.New("push subscription database is required")
	}
	if claimToken == uuid.Nil {
		return ErrDeliveryResultClaimLost
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin expired push delivery result: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := tx.Exec(ctx, `
		UPDATE web_push_delivery_results
		SET state = 'EXPIRED', provider_response = NULLIF($4, ''), error_message = NULLIF($5, ''), claimed_until = NULL, claim_token = NULL, completed_at = now(), updated_at = now()
		WHERE delivery_id = $1 AND subscription_id = $2 AND claim_token = $3 AND claimed_until > now()
	`, deliveryID, subscriptionID, claimToken, providerResponse, errorMessage)
	if err != nil {
		return fmt.Errorf("complete expired push delivery result: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrDeliveryResultClaimLost
	}
	if _, err := tx.Exec(ctx, `DELETE FROM push_subscriptions WHERE id = $1 AND user_id = $2`, subscriptionID, userID); err != nil {
		return fmt.Errorf("remove expired push subscription: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit expired push delivery result: %w", err)
	}
	return nil
}

func validDeliveryResultState(state DeliveryResultState) bool {
	switch state {
	case DeliveryResultSucceeded, DeliveryResultExpired, DeliveryResultPermanent, DeliveryResultRetryable:
		return true
	default:
		return false
	}
}
