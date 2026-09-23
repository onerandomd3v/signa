package api

import (
	"net/http"
	"strings"
)

const defaultWebOrigin = "http://localhost:3000"

var defaultCORSOrigins = []string{defaultWebOrigin}

type corsMiddleware struct {
	allowed map[string]struct{}
}

func newCORSMiddleware(origins []string) corsMiddleware {
	allowed := make(map[string]struct{}, len(origins))
	for _, origin := range origins {
		allowed[origin] = struct{}{}
	}
	return corsMiddleware{allowed: allowed}
}

func (c corsMiddleware) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		origin := request.Header.Get("Origin")
		if origin == "" {
			next.ServeHTTP(writer, request)
			return
		}

		writer.Header().Add("Vary", "Origin")
		_, allowed := c.allowed[origin]
		if !allowed {
			next.ServeHTTP(writer, request)
			return
		}

		writer.Header().Set("Access-Control-Allow-Origin", origin)
		writer.Header().Set("Access-Control-Expose-Headers", "Retry-After")
		if request.Method == http.MethodOptions {
			if !validPreflight(request) {
				writer.WriteHeader(http.StatusForbidden)
				return
			}
			writer.Header().Set("Access-Control-Allow-Methods", http.MethodPost)
			writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Idempotency-Key")
			writer.Header().Set("Access-Control-Max-Age", "600")
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func validPreflight(request *http.Request) bool {
	requestedMethod := strings.TrimSpace(request.Header.Get("Access-Control-Request-Method"))
	if requestedMethod != http.MethodPost {
		return false
	}
	for _, requestedHeader := range strings.Split(request.Header.Get("Access-Control-Request-Headers"), ",") {
		header := strings.TrimSpace(requestedHeader)
		if header == "" {
			continue
		}
		if !strings.EqualFold(header, "Content-Type") && !strings.EqualFold(header, "Idempotency-Key") {
			return false
		}
	}
	return true
}
