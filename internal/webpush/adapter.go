package webpush

import (
	"context"
	"crypto/ecdh"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/google/uuid"
	"github.com/onerandomd3v/signa/internal/delivery"
	"github.com/onerandomd3v/signa/internal/push"
)

const Channel = "WEB_PUSH"

const (
	defaultRecipientClaimLease = 5 * time.Minute
	recipientClaimSafetyMargin = time.Minute
)

var (
	ErrNoSubscriptions       = errors.New("user has no active push subscriptions")
	ErrUnsupportedChannel    = errors.New("delivery channel is not WEB_PUSH")
	ErrInvalidProviderConfig = errors.New("web push provider configuration is invalid")
)

type Config struct {
	Subscriber      string
	VAPIDPublicKey  string
	VAPIDPrivateKey string
	TTL             int
	Timeout         time.Duration
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.Subscriber) == "" || strings.TrimSpace(c.VAPIDPublicKey) == "" || strings.TrimSpace(c.VAPIDPrivateKey) == "" {
		return fmt.Errorf("%w: subscriber and VAPID keys are required", ErrInvalidProviderConfig)
	}
	parsed, err := url.Parse(c.Subscriber)
	if err != nil || (parsed.Scheme != "mailto" && parsed.Scheme != "https") || (parsed.Scheme == "https" && parsed.Host == "") {
		return fmt.Errorf("%w: subscriber must be a mailto or HTTPS URL", ErrInvalidProviderConfig)
	}
	if strings.HasPrefix(c.Subscriber, "mailto:") && strings.TrimPrefix(c.Subscriber, "mailto:") == "" {
		return fmt.Errorf("%w: mailto subscriber must include an address", ErrInvalidProviderConfig)
	}
	if c.TTL <= 0 || c.TTL > 2_419_200 {
		return fmt.Errorf("%w: TTL must be between 1 and 2419200 seconds", ErrInvalidProviderConfig)
	}
	if c.Timeout <= 0 {
		return fmt.Errorf("%w: timeout must be greater than zero", ErrInvalidProviderConfig)
	}
	publicKey, err := decodeVAPIDKey(c.VAPIDPublicKey)
	if err != nil || len(publicKey) != 65 || publicKey[0] != 4 {
		return fmt.Errorf("%w: VAPID public key is invalid", ErrInvalidProviderConfig)
	}
	privateKey, err := decodeVAPIDKey(c.VAPIDPrivateKey)
	if err != nil || len(privateKey) != 32 || allZero(privateKey) {
		return fmt.Errorf("%w: VAPID private key is invalid", ErrInvalidProviderConfig)
	}
	privateECDHKey, err := ecdh.P256().NewPrivateKey(privateKey)
	if err != nil {
		return fmt.Errorf("%w: VAPID private key is invalid", ErrInvalidProviderConfig)
	}
	expectedPublicKey := privateECDHKey.PublicKey().Bytes()
	if len(expectedPublicKey) != len(publicKey) || subtle.ConstantTimeCompare(expectedPublicKey, publicKey) != 1 {
		return fmt.Errorf("%w: VAPID key pair does not match", ErrInvalidProviderConfig)
	}
	return nil
}

func decodeVAPIDKey(value string) ([]byte, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err == nil {
		return decoded, nil
	}
	return base64.URLEncoding.DecodeString(value)
}

func allZero(value []byte) bool {
	for _, item := range value {
		if item != 0 {
			return false
		}
	}
	return true
}

type SendResult struct {
	StatusCode int
	Response   string
}

type Sender interface {
	Send(context.Context, push.Subscription, []byte, string) (SendResult, error)
}

type Adapter struct {
	store               push.DeliveryStore
	sender              Sender
	recipientClaimLease time.Duration
}

func NewAdapter(store push.DeliveryStore, sender Sender, config Config) *Adapter {
	return &Adapter{store: store, sender: sender, recipientClaimLease: recipientClaimLeaseFor(config.Timeout)}
}

func NewConfiguredAdapter(store push.DeliveryStore, config Config, client webpush.HTTPClient) (*Adapter, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if client == nil {
		client = newSafeHTTPClient(config.Timeout, nil, nil, nil)
	} else if httpClient, ok := client.(*http.Client); ok {
		client = newSafeHTTPClient(config.Timeout, nil, nil, httpClient.Transport)
	}
	return NewAdapter(store, &librarySender{config: config, client: client}, config), nil
}

func (a *Adapter) Deliver(ctx context.Context, attempt delivery.Attempt) (delivery.ProviderResult, error) {
	if a == nil || a.store == nil || a.sender == nil {
		return delivery.ProviderResult{}, delivery.Transient(errors.New("web push adapter dependencies are required"))
	}
	if attempt.Channel != Channel {
		return delivery.ProviderResult{}, delivery.Permanent(ErrUnsupportedChannel)
	}
	if attempt.UserID == uuid.Nil {
		return delivery.ProviderResult{}, delivery.Permanent(errors.New("web push delivery user is required"))
	}
	deliveryID, err := uuid.Parse(attempt.DeliveryID)
	if err != nil {
		return delivery.ProviderResult{}, delivery.Permanent(fmt.Errorf("web push delivery ID is invalid: %w", err))
	}
	subscriptions, err := a.store.ListForDelivery(ctx, attempt.UserID)
	if err != nil {
		return delivery.ProviderResult{}, delivery.Transient(fmt.Errorf("list web push subscriptions: %w", err))
	}
	if len(subscriptions) == 0 {
		return delivery.ProviderResult{}, delivery.Permanent(ErrNoSubscriptions)
	}
	if err := a.store.EnsureDeliveryResults(ctx, deliveryID, attempt.UserID, subscriptions); err != nil {
		return delivery.ProviderResult{}, delivery.Transient(fmt.Errorf("initialize web push delivery results: %w", err))
	}
	results, err := a.store.LoadDeliveryResults(ctx, deliveryID)
	if err != nil {
		return delivery.ProviderResult{}, delivery.Transient(fmt.Errorf("load web push delivery results: %w", err))
	}

	var (
		succeeded    int
		terminal     int
		lastResponse string
		transientErr error
		permanentErr error
	)
	for _, subscription := range subscriptions {
		stored, ok := results[subscription.ID]
		if ok {
			switch stored.State {
			case push.DeliveryResultSucceeded:
				succeeded++
				terminal++
				lastResponse = stored.ProviderResponse
				continue
			case push.DeliveryResultExpired, push.DeliveryResultPermanent:
				terminal++
				lastResponse = stored.ProviderResponse
				permanentErr = fmt.Errorf("web push subscription result is %s", strings.ToLower(string(stored.State)))
				continue
			}
		}
		claimToken, claimed, err := a.store.ClaimDeliveryResult(ctx, deliveryID, subscription.ID, a.recipientClaimLease)
		if err != nil {
			transientErr = fmt.Errorf("claim web push delivery result: %w", err)
			continue
		}
		if !claimed {
			transientErr = errors.New("web push delivery recipient is already being processed")
			continue
		}
		result, sendErr := a.sender.Send(ctx, subscription, attempt.Payload, attempt.OperationKey)
		if sendErr != nil {
			if completeErr := a.store.CompleteDeliveryResult(ctx, deliveryID, subscription.ID, claimToken, push.DeliveryResultRetryable, "", sendErr.Error()); completeErr != nil {
				transientErr = fmt.Errorf("record web push retryable result: %w", completeErr)
			} else {
				transientErr = fmt.Errorf("send web push notification: %w", sendErr)
			}
			continue
		}
		lastResponse = result.Response
		switch {
		case result.StatusCode >= 200 && result.StatusCode < 300:
			if err := a.store.CompleteDeliveryResult(ctx, deliveryID, subscription.ID, claimToken, push.DeliveryResultSucceeded, result.Response, ""); err != nil {
				transientErr = fmt.Errorf("record web push success: %w", err)
			} else {
				succeeded++
				terminal++
			}
		case result.StatusCode == http.StatusNotFound || result.StatusCode == http.StatusGone:
			if err := a.store.CompleteExpiredDeliveryResult(ctx, deliveryID, attempt.UserID, subscription.ID, claimToken, result.Response, fmt.Sprintf("web push subscription expired: status %d", result.StatusCode)); err != nil {
				transientErr = fmt.Errorf("record expired web push subscription: %w", err)
			} else {
				terminal++
				permanentErr = fmt.Errorf("web push subscription expired: status %d", result.StatusCode)
			}
		case result.StatusCode >= 400 && result.StatusCode < 500 && result.StatusCode != http.StatusRequestTimeout && result.StatusCode != http.StatusTooManyRequests:
			if err := a.store.CompleteDeliveryResult(ctx, deliveryID, subscription.ID, claimToken, push.DeliveryResultPermanent, result.Response, fmt.Sprintf("web push provider rejected subscription: status %d", result.StatusCode)); err != nil {
				transientErr = fmt.Errorf("record web push permanent result: %w", err)
			} else {
				terminal++
				permanentErr = fmt.Errorf("web push provider rejected subscription: status %d", result.StatusCode)
			}
		default:
			if err := a.store.CompleteDeliveryResult(ctx, deliveryID, subscription.ID, claimToken, push.DeliveryResultRetryable, result.Response, fmt.Sprintf("web push provider returned status %d", result.StatusCode)); err != nil {
				transientErr = fmt.Errorf("record web push retryable result: %w", err)
			} else {
				transientErr = fmt.Errorf("web push provider returned status %d", result.StatusCode)
			}
		}
	}
	if transientErr != nil {
		return delivery.ProviderResult{Response: lastResponse}, delivery.Transient(transientErr)
	}
	if succeeded > 0 {
		return delivery.ProviderResult{Response: lastResponse}, nil
	}
	if permanentErr != nil && terminal == len(subscriptions) {
		return delivery.ProviderResult{Response: lastResponse}, delivery.Permanent(permanentErr)
	}
	return delivery.ProviderResult{Response: lastResponse}, delivery.Transient(errors.New("web push delivery has unresolved recipients"))
}

type librarySender struct {
	config Config
	client webpush.HTTPClient
}

func (s *librarySender) Send(ctx context.Context, subscription push.Subscription, payload []byte, operationKey string) (SendResult, error) {
	options := &webpush.Options{
		Subscriber:      s.config.Subscriber,
		VAPIDPublicKey:  s.config.VAPIDPublicKey,
		VAPIDPrivateKey: s.config.VAPIDPrivateKey,
		TTL:             s.config.TTL,
		Topic:           operationTopic(operationKey),
		HTTPClient:      s.client,
	}
	response, err := webpush.SendNotificationWithContext(ctx, payload, &webpush.Subscription{Endpoint: subscription.Endpoint, Keys: webpush.Keys{P256dh: subscription.P256DH, Auth: subscription.Auth}}, options)
	if err != nil {
		return SendResult{}, err
	}
	defer func() { _ = response.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 512))
	return SendResult{StatusCode: response.StatusCode, Response: strings.TrimSpace(string(body))}, nil
}

func operationTopic(operationKey string) string {
	digest := sha256.Sum256([]byte(operationKey))
	return base64.RawURLEncoding.EncodeToString(digest[:24])
}

func recipientClaimLeaseFor(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return defaultRecipientClaimLease
	}
	if timeout > time.Duration(1<<63-1)-recipientClaimSafetyMargin {
		return time.Duration(1<<63 - 1)
	}
	return timeout + recipientClaimSafetyMargin
}
