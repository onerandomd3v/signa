package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"unicode"
)

const defaultWebAllowedOrigin = "http://localhost:3000"

func webAllowedOriginsFromEnv() ([]string, error) {
	value := strings.TrimSpace(os.Getenv("SIGNA_WEB_ALLOWED_ORIGINS"))
	if value == "" {
		return []string{defaultWebAllowedOrigin}, nil
	}

	origins := make([]string, 0)
	seen := make(map[string]struct{})
	for _, candidate := range strings.Split(value, ",") {
		origin := strings.TrimSpace(candidate)
		if origin == "" {
			return nil, fmt.Errorf("SIGNA_WEB_ALLOWED_ORIGINS contains an empty origin")
		}
		canonicalOrigin, err := normalizeWebOrigin(origin)
		if err != nil {
			return nil, fmt.Errorf("SIGNA_WEB_ALLOWED_ORIGINS: %w", err)
		}
		if _, exists := seen[canonicalOrigin]; exists {
			continue
		}
		seen[canonicalOrigin] = struct{}{}
		origins = append(origins, canonicalOrigin)
	}
	return origins, nil
}

func normalizeWebOrigin(origin string) (string, error) {
	if origin == "*" || strings.Contains(origin, "*") {
		return "", fmt.Errorf("wildcard origins are not allowed")
	}
	if strings.IndexFunc(origin, unicode.IsSpace) >= 0 {
		return "", fmt.Errorf("origin %q contains whitespace", origin)
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return "", fmt.Errorf("origin %q is invalid: %w", origin, err)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("origin %q must use http or https", origin)
	}
	if parsed.Host == "" || parsed.Hostname() == "" {
		return "", fmt.Errorf("origin %q must include a host", origin)
	}
	if parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" {
		return "", fmt.Errorf("origin %q must contain only scheme and authority", origin)
	}
	port := parsed.Port()
	if port == "" && strings.Contains(parsed.Host, ":") && !strings.HasPrefix(parsed.Host, "[") {
		return "", fmt.Errorf("origin %q has an invalid port", origin)
	}
	if port != "" {
		portNumber, err := strconv.ParseUint(port, 10, 16)
		if err != nil {
			return "", fmt.Errorf("origin %q has an invalid port", origin)
		}
		if (scheme == "http" && portNumber != 80) || (scheme == "https" && portNumber != 443) {
			port = ":" + port
		} else {
			port = ""
		}
	}
	host := strings.ToLower(parsed.Hostname())
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return scheme + "://" + host + port, nil
}
