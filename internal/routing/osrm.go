package routing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxResponseBytes = 1 << 20

type OSRMConfig struct {
	BaseURL string
	Timeout time.Duration
	Client  *http.Client
}

type OSRMProvider struct {
	baseURL string
	client  *http.Client
	timeout time.Duration
}

func NewOSRMProvider(config OSRMConfig) (*OSRMProvider, error) {
	baseURL, err := validateBaseURL(config.BaseURL)
	if err != nil {
		return nil, err
	}
	if config.Timeout <= 0 {
		return nil, errors.Join(ErrInvalidRequest, errors.New("routing timeout must be greater than zero"))
	}
	client := config.Client
	if client == nil {
		client = http.DefaultClient
	}
	return &OSRMProvider{baseURL: strings.TrimRight(baseURL, "/"), client: client, timeout: config.Timeout}, nil
}

func (p *OSRMProvider) Route(ctx context.Context, request Request) (Route, error) {
	if err := request.validate(); err != nil {
		return Route{}, err
	}
	coordinates := make([]string, 0, len(request.Waypoints)+2)
	coordinates = append(coordinates, formatCoordinate(request.Origin))
	for _, waypoint := range request.Waypoints {
		coordinates = append(coordinates, formatCoordinate(waypoint))
	}
	coordinates = append(coordinates, formatCoordinate(request.Destination))
	endpoint := p.baseURL + "/route/v1/driving/" + strings.Join(coordinates, ";")
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return Route{}, errors.Join(ErrInvalidRequest, errors.New("routing URL is invalid"))
	}
	query := parsed.Query()
	query.Set("geometries", "geojson")
	query.Set("overview", "full")
	query.Set("steps", "false")
	parsed.RawQuery = query.Encode()

	requestContext, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	httpRequest, err := http.NewRequestWithContext(requestContext, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return Route{}, errors.Join(ErrInvalidRequest, errors.New("routing request could not be created"))
	}
	response, err := p.client.Do(httpRequest)
	if err != nil {
		if errors.Is(requestContext.Err(), context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			return Route{}, context.Canceled
		}
		if errors.Is(requestContext.Err(), context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return Route{}, context.DeadlineExceeded
		}
		return Route{}, ErrUpstream
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return Route{}, ErrUpstream
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(requestContext.Err(), context.Canceled) {
			return Route{}, context.Canceled
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(requestContext.Err(), context.DeadlineExceeded) {
			return Route{}, context.DeadlineExceeded
		}
		return Route{}, ErrInvalidResponse
	}
	if len(body) > maxResponseBytes {
		return Route{}, ErrInvalidResponse
	}
	var payload osrmResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return Route{}, ErrInvalidResponse
	}
	switch payload.Code {
	case "NoRoute":
		return Route{}, ErrNoRoute
	case "Ok":
		if len(payload.Routes) == 0 {
			return Route{}, ErrNoRoute
		}
	default:
		if payload.Code == "" {
			return Route{}, ErrInvalidResponse
		}
		return Route{}, ErrUpstream
	}
	first := payload.Routes[0]
	if math.IsNaN(first.Duration) || math.IsInf(first.Duration, 0) || first.Duration < 0 || math.IsNaN(first.DistanceMeters) || math.IsInf(first.DistanceMeters, 0) || first.DistanceMeters < 0 {
		return Route{}, ErrInvalidResponse
	}
	duration := time.Duration(first.Duration * float64(time.Second))
	route := Route{Geometry: first.Geometry, DistanceMeters: first.DistanceMeters, Duration: duration}
	if err := validateRoute(route); err != nil {
		return Route{}, err
	}
	return route, nil
}

type osrmResponse struct {
	Code   string      `json:"code"`
	Routes []osrmRoute `json:"routes"`
}

type osrmRoute struct {
	Geometry       GeoJSONLineString `json:"geometry"`
	DistanceMeters float64           `json:"distance"`
	Duration       float64           `json:"duration"`
}

func formatCoordinate(point Point) string {
	return fmt.Sprintf("%.7f,%.7f", point.Longitude, point.Latitude)
}

func validateBaseURL(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.Join(ErrInvalidRequest, errors.New("routing base URL must be an HTTP(S) URL without credentials or query parameters"))
	}
	return parsed.String(), nil
}
