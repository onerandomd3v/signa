package config

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// RoutingConfig contains the explicit configuration for a routing provider.
// It is loaded separately because routing is optional for the API process.
type RoutingConfig struct {
	Provider string
	BaseURL  string
	Timeout  time.Duration
}

// LoadRoutingConfig reads the routing provider settings. A base URL and
// timeout are intentionally required so network behavior is never implicit.
func LoadRoutingConfig() (RoutingConfig, error) {
	provider := strings.TrimSpace(os.Getenv("SIGNA_ROUTING_PROVIDER"))
	if provider == "" {
		provider = "osrm"
	}
	baseURL := strings.TrimSpace(os.Getenv("SIGNA_ROUTING_BASE_URL"))
	if baseURL == "" {
		return RoutingConfig{}, fmt.Errorf("SIGNA_ROUTING_BASE_URL is required when routing is configured")
	}
	timeout, err := requiredRoutingDuration("SIGNA_ROUTING_TIMEOUT")
	if err != nil {
		return RoutingConfig{}, err
	}
	return RoutingConfig{Provider: provider, BaseURL: baseURL, Timeout: timeout}, nil
}

func requiredRoutingDuration(name string) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return 0, fmt.Errorf("%s is required when routing is configured", name)
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("%s must be a duration greater than zero", name)
	}
	return duration, nil
}
