package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/onerandomd3v/signa/internal/auth"
	"github.com/onerandomd3v/signa/internal/push"
)

type pushSubscriptionRequest struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256DH string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

type pushSubscriptionResponse struct {
	ID        string `json:"id"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func pushSubscriptionCreateHandler(store push.SubscriptionWriter) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		principal, ok := auth.PrincipalFromContext(request.Context())
		if !ok {
			writeError(writer, http.StatusUnauthorized, "unauthorized", "authentication is required")
			return
		}
		userID := principal.UserID
		if store == nil {
			writeError(writer, http.StatusServiceUnavailable, "push_unavailable", "push subscriptions are unavailable")
			return
		}
		var body pushSubscriptionRequest
		if err := decodeJSON(request, &body); err != nil {
			writeError(writer, http.StatusBadRequest, "invalid_request", "request body must be valid JSON")
			return
		}
		item, created, err := store.Upsert(request.Context(), userID, push.SubscriptionInput{
			Endpoint: body.Endpoint,
			P256DH:   body.Keys.P256DH,
			Auth:     body.Keys.Auth,
		})
		if err != nil {
			switch {
			case errors.Is(err, push.ErrInvalidSubscription):
				writeError(writer, http.StatusBadRequest, "invalid_subscription", "push subscription is invalid")
			case errors.Is(err, push.ErrSubscriptionConflict):
				writeError(writer, http.StatusConflict, "subscription_conflict", "push subscription is already registered")
			default:
				writeError(writer, http.StatusInternalServerError, "internal_error", "push subscription could not be saved")
			}
			return
		}
		status := http.StatusOK
		if created {
			status = http.StatusCreated
		}
		writeJSON(writer, status, pushSubscriptionResponse{ID: item.ID.String(), CreatedAt: item.CreatedAt.UTC().Format(time.RFC3339Nano), UpdatedAt: item.UpdatedAt.UTC().Format(time.RFC3339Nano)})
	})
}

func pushSubscriptionDeleteHandler(store push.SubscriptionWriter) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		principal, ok := auth.PrincipalFromContext(request.Context())
		if !ok {
			writeError(writer, http.StatusUnauthorized, "unauthorized", "authentication is required")
			return
		}
		userID := principal.UserID
		subscriptionID, err := uuid.Parse(strings.TrimSpace(chi.URLParam(request, "subscription_id")))
		if err != nil {
			writeError(writer, http.StatusNotFound, "subscription_not_found", "push subscription was not found")
			return
		}
		if store == nil {
			writeError(writer, http.StatusServiceUnavailable, "push_unavailable", "push subscriptions are unavailable")
			return
		}
		if err := store.Delete(request.Context(), userID, subscriptionID); err != nil {
			if errors.Is(err, push.ErrSubscriptionNotFound) {
				writeError(writer, http.StatusNotFound, "subscription_not_found", "push subscription was not found")
				return
			}
			writeError(writer, http.StatusInternalServerError, "internal_error", "push subscription could not be removed")
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	})
}
