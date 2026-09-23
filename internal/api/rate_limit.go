package api

import (
	"math"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const (
	defaultRatePerMinute       = 6
	defaultRateBurst           = 3
	defaultGlobalRatePerMinute = 120
	defaultGlobalRateBurst     = 30
	defaultIdleAfter           = 10 * time.Minute
)

type RateLimitConfig struct {
	PerClientRatePerMinute int
	PerClientBurst         int
	GlobalRatePerMinute    int
	GlobalBurst            int
}

func DefaultRateLimitConfig() RateLimitConfig {
	return RateLimitConfig{
		PerClientRatePerMinute: defaultRatePerMinute,
		PerClientBurst:         defaultRateBurst,
		GlobalRatePerMinute:    defaultGlobalRatePerMinute,
		GlobalBurst:            defaultGlobalRateBurst,
	}
}

type ClientKeyFunc func(*http.Request) string

type RateLimiterOptions struct {
	Now       func() time.Time
	ClientKey ClientKeyFunc
	IdleAfter time.Duration
}

type RateLimiter struct {
	mu        sync.Mutex
	config    RateLimitConfig
	global    tokenBucket
	clients   map[string]*clientBucket
	now       func() time.Time
	clientKey ClientKeyFunc
	idleAfter time.Duration
}

type clientBucket struct {
	bucket   tokenBucket
	lastSeen time.Time
}

type tokenBucket struct {
	tokens     float64
	lastRefill time.Time
}

func NewRateLimiter(config RateLimitConfig, options RateLimiterOptions) *RateLimiter {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	clientKey := options.ClientKey
	if clientKey == nil {
		clientKey = RemoteAddrClientKey
	}
	idleAfter := options.IdleAfter
	if idleAfter <= 0 {
		idleAfter = defaultIdleAfter
	}
	current := now()
	return &RateLimiter{
		config:    config,
		global:    newTokenBucket(config.GlobalBurst, current),
		clients:   make(map[string]*clientBucket),
		now:       now,
		clientKey: clientKey,
		idleAfter: idleAfter,
	}
}

func (l *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		allowed, retryAfter := l.Allow(l.clientKey(request))
		if !allowed {
			writer.Header().Set("Retry-After", retryAfter)
			writeError(writer, http.StatusTooManyRequests, "rate_limited", "too many requests")
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func (l *RateLimiter) Allow(clientKey string) (bool, string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.cleanup(now)
	if allowed, retryAfter := l.global.allow(now, l.config.GlobalRatePerMinute, l.config.GlobalBurst); !allowed {
		return false, retryAfter
	}
	client, exists := l.clients[clientKey]
	if !exists {
		client = &clientBucket{bucket: newTokenBucket(l.config.PerClientBurst, now)}
		l.clients[clientKey] = client
	}
	client.lastSeen = now
	return client.bucket.allow(now, l.config.PerClientRatePerMinute, l.config.PerClientBurst)
}

func (l *RateLimiter) ClientCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.clients)
}

func (l *RateLimiter) cleanup(now time.Time) {
	for key, client := range l.clients {
		if now.Sub(client.lastSeen) >= l.idleAfter {
			delete(l.clients, key)
		}
	}
}

func (b *tokenBucket) allow(now time.Time, ratePerMinute, burst int) (bool, string) {
	if ratePerMinute <= 0 || burst <= 0 {
		return false, "1"
	}
	elapsed := now.Sub(b.lastRefill).Seconds()
	if elapsed > 0 {
		b.tokens = math.Min(float64(burst), b.tokens+elapsed*float64(ratePerMinute)/60)
		b.lastRefill = now
	}
	if b.tokens >= 1 {
		b.tokens--
		return true, ""
	}
	seconds := math.Ceil((1 - b.tokens) * 60 / float64(ratePerMinute))
	if seconds < 1 {
		seconds = 1
	}
	return false, formatRetryAfter(seconds)
}

func newTokenBucket(burst int, now time.Time) tokenBucket {
	return tokenBucket{tokens: float64(burst), lastRefill: now}
}

func formatRetryAfter(seconds float64) string {
	return strconv.FormatInt(int64(seconds), 10)
}

func RemoteAddrClientKey(request *http.Request) string {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err == nil {
		return host
	}
	return request.RemoteAddr
}
