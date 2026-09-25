package auth

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
)

const DefaultCookieName = "signa_session"

var ErrUnauthenticated = errors.New("unauthenticated")

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
	return RequirePrincipalWithCookieName(store, DefaultCookieName)
}

func RequirePrincipalWithCookieName(store SessionStore, cookieName string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			cookie, err := request.Cookie(cookieName)
			if err != nil || cookie.Value == "" {
				unauthorized(writer)
				return
			}
			principal, err := store.Lookup(request.Context(), cookie.Value)
			if err != nil {
				unauthorized(writer)
				return
			}
			next.ServeHTTP(writer, request.WithContext(WithPrincipal(request.Context(), principal)))
		})
	}
}

func unauthorized(writer http.ResponseWriter) {
	http.Error(writer, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
}
