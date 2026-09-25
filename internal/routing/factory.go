package routing

import (
	"errors"
	"net/http"
	"strings"
	"time"
)

type Config struct {
	Provider string
	BaseURL  string
	Timeout  time.Duration
}

func NewProvider(config Config, client *http.Client) (Provider, error) {
	switch strings.ToLower(strings.TrimSpace(config.Provider)) {
	case "osrm":
		return NewOSRMProvider(OSRMConfig{BaseURL: config.BaseURL, Timeout: config.Timeout, Client: client})
	default:
		return nil, errors.Join(ErrInvalidRequest, errors.New("unknown routing provider"))
	}
}
