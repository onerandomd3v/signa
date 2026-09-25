package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"
)

var ErrInvalidCookieOptions = errors.New("invalid session cookie options")

type CookieOptions struct {
	Name     string
	Path     string
	Secure   bool
	SameSite http.SameSite
}

func (o CookieOptions) normalized() (CookieOptions, error) {
	if strings.TrimSpace(o.Name) == "" || strings.ContainsAny(o.Name, "=;\r\n") {
		return CookieOptions{}, ErrInvalidCookieOptions
	}
	if o.Path == "" {
		o.Path = "/"
	}
	if o.Path[0] != '/' || strings.ContainsAny(o.Path, "\r\n") {
		return CookieOptions{}, ErrInvalidCookieOptions
	}
	if o.SameSite != http.SameSiteDefaultMode && o.SameSite != http.SameSiteLaxMode && o.SameSite != http.SameSiteStrictMode && o.SameSite != http.SameSiteNoneMode {
		return CookieOptions{}, ErrInvalidCookieOptions
	}
	if o.SameSite == http.SameSiteNoneMode && !o.Secure {
		return CookieOptions{}, ErrInvalidCookieOptions
	}
	return o, nil
}

func NewSessionCookie(options CookieOptions, secret string, expiresAt time.Time) (*http.Cookie, error) {
	options, err := options.normalized()
	if err != nil {
		return nil, err
	}
	if _, err := decodeSecret(secret); err != nil {
		return nil, ErrUnauthenticated
	}
	return &http.Cookie{
		Name:     options.Name,
		Value:    secret,
		Path:     options.Path,
		Expires:  expiresAt.UTC(),
		HttpOnly: true,
		Secure:   options.Secure,
		SameSite: options.SameSite,
	}, nil
}

func ClearSessionCookie(options CookieOptions) (*http.Cookie, error) {
	options, err := options.normalized()
	if err != nil {
		return nil, err
	}
	return &http.Cookie{
		Name:     options.Name,
		Value:    "",
		Path:     options.Path,
		Expires:  time.Unix(1, 0).UTC(),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   options.Secure,
		SameSite: options.SameSite,
	}, nil
}

func NewSessionSecret() (string, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(secret), nil
}

func hashSecret(secret string) ([]byte, error) {
	decoded, err := decodeSecret(secret)
	if err != nil {
		return nil, ErrUnauthenticated
	}
	hash := sha256.Sum256(decoded)
	return hash[:], nil
}

func decodeSecret(secret string) ([]byte, error) {
	if secret == "" || strings.ContainsAny(secret, ";,\r\n") {
		return nil, ErrUnauthenticated
	}
	decoded, err := base64.RawURLEncoding.DecodeString(secret)
	if err != nil || len(decoded) != 32 {
		return nil, ErrUnauthenticated
	}
	return decoded, nil
}
