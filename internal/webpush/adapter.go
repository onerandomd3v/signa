package webpush

import (
	"context"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
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
	curve := elliptic.P256()
	x, y := curve.ScalarBaseMult(privateKey)
	expectedPublicKey := elliptic.Marshal(curve, x, y)
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
	store  push.DeliveryStore
	sender Sender
}

func NewAdapter(store push.DeliveryStore, sender Sender, _ Config) *Adapter {
	return &Adapter{store: store, sender: sender}
}

func NewConfiguredAdapter(store push.DeliveryStore, config Config, client webpush.HTTPClient) (*Adapter, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if client == nil {
		client = &http.Client{Timeout: config.Timeout}
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
	subscriptions, err := a.store.ListForDelivery(ctx, attempt.UserID)
	if err != nil {
		return delivery.ProviderResult{}, delivery.Transient(fmt.Errorf("list web push subscriptions: %w", err))
	}
	if len(subscriptions) == 0 {
		return delivery.ProviderResult{}, delivery.Permanent(ErrNoSubscriptions)
	}

	var (
		succeeded    int
		lastResponse string
		transientErr error
		permanentErr error
	)
	for _, subscription := range subscriptions {
		result, sendErr := a.sender.Send(ctx, subscription, attempt.Payload, attempt.OperationKey)
		if sendErr != nil {
			transientErr = fmt.Errorf("send web push notification: %w", sendErr)
			continue
		}
		lastResponse = result.Response
		switch {
		case result.StatusCode >= 200 && result.StatusCode < 300:
			succeeded++
		case result.StatusCode == http.StatusNotFound || result.StatusCode == http.StatusGone:
			if err := a.store.RemoveExpired(ctx, attempt.UserID, subscription.ID); err != nil {
				transientErr = fmt.Errorf("remove expired web push subscription: %w", err)
			} else {
				permanentErr = fmt.Errorf("web push subscription expired: status %d", result.StatusCode)
			}
		case result.StatusCode >= 400 && result.StatusCode < 500 && result.StatusCode != http.StatusRequestTimeout && result.StatusCode != http.StatusTooManyRequests:
			if err := a.store.RemoveExpired(ctx, attempt.UserID, subscription.ID); err != nil {
				transientErr = fmt.Errorf("remove invalid web push subscription: %w", err)
			} else {
				permanentErr = fmt.Errorf("web push subscription rejected: status %d", result.StatusCode)
			}
		default:
			transientErr = fmt.Errorf("web push provider returned status %d", result.StatusCode)
		}
	}
	if transientErr != nil {
		return delivery.ProviderResult{Response: lastResponse}, delivery.Transient(transientErr)
	}
	if succeeded > 0 {
		return delivery.ProviderResult{Response: lastResponse}, nil
	}
	if permanentErr != nil {
		return delivery.ProviderResult{Response: lastResponse}, delivery.Permanent(permanentErr)
	}
	return delivery.ProviderResult{Response: lastResponse}, delivery.Permanent(ErrNoSubscriptions)
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
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 512))
	return SendResult{StatusCode: response.StatusCode, Response: strings.TrimSpace(string(body))}, nil
}

func operationTopic(operationKey string) string {
	digest := sha256.Sum256([]byte(operationKey))
	return hex.EncodeToString(digest[:])
}
