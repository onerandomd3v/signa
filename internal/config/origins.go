package config

import (
	"fmt"
	"net/url"
	"os"
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
		if err := validateWebOrigin(origin); err != nil {
			return nil, fmt.Errorf("SIGNA_WEB_ALLOWED_ORIGINS: %w", err)
		}
		if _, exists := seen[origin]; exists {
			continue
		}
		seen[origin] = struct{}{}
		origins = append(origins, origin)
	}
	return origins, nil
}

func validateWebOrigin(origin string) error {
	if origin == "*" || strings.Contains(origin, "*") {
		return fmt.Errorf("wildcard origins are not allowed")
	}
	if strings.IndexFunc(origin, unicode.IsSpace) >= 0 {
		return fmt.Errorf("origin %q contains whitespace", origin)
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return fmt.Errorf("origin %q is invalid: %w", origin, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("origin %q must use http or https", origin)
	}
	if parsed.Host == "" || parsed.Hostname() == "" {
		return fmt.Errorf("origin %q must include a host", origin)
	}
	if parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" {
		return fmt.Errorf("origin %q must contain only scheme and authority", origin)
	}
	if parsed.Port() == "" && strings.Contains(parsed.Host, ":") && !strings.HasPrefix(parsed.Host, "[") {
		return fmt.Errorf("origin %q has an invalid port", origin)
	}
	return nil
}
