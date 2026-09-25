package auth

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
)

const DefaultCookieName = "signa_session"

var ErrUnauthenticated = errors.New("unauthenticated")
var ErrSessionStoreUnavailable = errors.New("session store unavailable")

type Principal struct {
	UserID uuid.UUID
}

type SessionStore interface {
	Lookup(ctx context.Context, secret string) (Principal, error)
}

type contextKey struct{}

func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, contextKey{}, principal)
}

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(contextKey{}).(Principal)
	return principal, ok
}

func RequirePrincipal(store SessionStore) func(http.Handler) http.Handler {
	return RequirePrincipalWithLogger(store, nil)
}

func RequirePrincipalWithCookieName(store SessionStore, cookieName string) func(http.Handler) http.Handler {
	return requirePrincipal(store, cookieName, nil)
}

func RequirePrincipalWithLogger(store SessionStore, logger *slog.Logger) func(http.Handler) http.Handler {
	return requirePrincipal(store, DefaultCookieName, logger)
}

func requirePrincipal(store SessionStore, cookieName string, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			cookie, err := request.Cookie(cookieName)
			if err != nil || cookie.Value == "" {
				writeError(writer, http.StatusUnauthorized, "unauthenticated", "authentication is required")
				return
			}
			principal, err := store.Lookup(request.Context(), cookie.Value)
			if err != nil {
				if errors.Is(err, ErrUnauthenticated) {
					writeError(writer, http.StatusUnauthorized, "unauthenticated", "authentication is required")
					return
				}
				if logger != nil {
					logger.Error("session lookup failed", "error", err)
				}
				writeError(writer, http.StatusServiceUnavailable, "authentication_unavailable", "authentication service is unavailable")
				return
			}
			next.ServeHTTP(writer, request.WithContext(WithPrincipal(request.Context(), principal)))
		})
	}
}

func writeError(writer http.ResponseWriter, status int, code, message string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}
