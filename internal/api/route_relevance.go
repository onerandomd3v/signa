package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"

	"github.com/onerandomd3v/signa/internal/config"
	"github.com/onerandomd3v/signa/internal/geospatial"
	"github.com/onerandomd3v/signa/internal/incidents"
	"github.com/onerandomd3v/signa/internal/routing"
)

var (
	ErrRouteDependencyUnavailable    = errors.New("route dependency unavailable")
	ErrIncidentDependencyUnavailable = errors.New("incident dependency unavailable")
)

type routeRelevanceRequest struct {
	Origin      *routePoint  `json:"origin"`
	Destination *routePoint  `json:"destination"`
	Waypoints   []routePoint `json:"waypoints,omitempty"`
}

type routePoint struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type RouteRelevanceResult struct {
	Classification geospatial.RouteRelevance
	Geometry       routing.GeoJSONLineString
	Incidents      []incidents.PublicIncident
}

type RouteRelevanceResponse struct {
	Classification geospatial.RouteRelevance  `json:"classification"`
	Geometry       routing.GeoJSONLineString  `json:"route_geometry"`
	Incidents      []incidents.PublicIncident `json:"incidents"`
}

type routeRelevanceService interface {
	Evaluate(context.Context, routing.Request) (RouteRelevanceResult, error)
}

type routeRelevanceServiceImpl struct {
	provider routing.Provider
	reader   incidents.RouteRelevanceReader
	policy   config.PublicIncidentGeometryPolicy
}

func newRouteRelevanceService(provider routing.Provider, reader incidents.RouteRelevanceReader, policy config.PublicIncidentGeometryPolicy) routeRelevanceService {
	return &routeRelevanceServiceImpl{provider: provider, reader: reader, policy: policy}
}

func (s *routeRelevanceServiceImpl) Evaluate(ctx context.Context, request routing.Request) (RouteRelevanceResult, error) {
	if s == nil || s.provider == nil || s.reader == nil {
		return RouteRelevanceResult{}, ErrRouteDependencyUnavailable
	}
	route, err := s.provider.Route(ctx, request)
	if err != nil {
		if errors.Is(err, routing.ErrInvalidRequest) {
			return RouteRelevanceResult{}, err
		}
		return RouteRelevanceResult{}, fmt.Errorf("%w: %v", ErrRouteDependencyUnavailable, safeDependencyError(err))
	}
	classification, publicIncidents, err := s.reader.FindPublicRouteRelevance(ctx, route.Geometry, s.policy)
	if err != nil {
		return RouteRelevanceResult{}, fmt.Errorf("%w: %v", ErrIncidentDependencyUnavailable, safeDependencyError(err))
	}
	return RouteRelevanceResult{Classification: classification, Geometry: route.Geometry, Incidents: publicIncidents}, nil
}

func routeRelevanceHandler(logger *slog.Logger, service routeRelevanceService) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var input routeRelevanceRequest
		decoder := json.NewDecoder(io.LimitReader(request.Body, 64<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			writeError(writer, http.StatusBadRequest, "invalid_route_request", "origin, destination, and optional waypoints are required")
			return
		}
		if err := ensureJSONBodyIsComplete(decoder); err != nil {
			writeError(writer, http.StatusBadRequest, "invalid_route_request", "origin, destination, and optional waypoints are required")
			return
		}
		routeRequest, err := input.routingRequest()
		if err != nil {
			writeError(writer, http.StatusBadRequest, "invalid_route_request", "origin, destination, and optional waypoints are required")
			return
		}
		if err := routeRequest.Validate(); err != nil {
			writeError(writer, http.StatusBadRequest, "invalid_route_request", "origin, destination, and optional waypoints are invalid")
			return
		}
		if service == nil {
			writeError(writer, http.StatusServiceUnavailable, "route_unavailable", "route relevance is temporarily unavailable")
			return
		}
		result, err := service.Evaluate(request.Context(), routeRequest)
		if err != nil {
			switch {
			case errors.Is(err, routing.ErrInvalidRequest):
				writeError(writer, http.StatusBadRequest, "invalid_route_request", "origin, destination, and optional waypoints are invalid")
			case errors.Is(err, ErrRouteDependencyUnavailable):
				writeError(writer, http.StatusServiceUnavailable, "route_unavailable", "route relevance is temporarily unavailable")
			case errors.Is(err, ErrIncidentDependencyUnavailable):
				writeError(writer, http.StatusServiceUnavailable, "incident_data_unavailable", "route relevance is temporarily unavailable")
			default:
				if logger != nil {
					logger.Error("route relevance evaluation failed", "error", safeDependencyError(err))
				}
				writeError(writer, http.StatusInternalServerError, "internal_error", "internal server error")
			}
			return
		}
		if result.Classification != geospatial.RouteRelevanceRelevant && result.Classification != geospatial.RouteRelevanceNotRelevant && result.Classification != geospatial.RouteRelevanceUnknown {
			writeError(writer, http.StatusInternalServerError, "internal_error", "internal server error")
			return
		}
		if err := result.Geometry.Validate(); err != nil {
			writeError(writer, http.StatusServiceUnavailable, "route_unavailable", "route relevance is temporarily unavailable")
			return
		}
		writeJSON(writer, http.StatusOK, RouteRelevanceResponse(result))
	})
}

func (r routeRelevanceRequest) routingRequest() (routing.Request, error) {
	if r.Origin == nil || r.Destination == nil {
		return routing.Request{}, routing.ErrInvalidRequest
	}
	waypoints := make([]routing.Point, 0, len(r.Waypoints))
	for _, waypoint := range r.Waypoints {
		waypoints = append(waypoints, routing.Point{Latitude: waypoint.Latitude, Longitude: waypoint.Longitude})
	}
	return routing.Request{
		Origin:      routing.Point{Latitude: r.Origin.Latitude, Longitude: r.Origin.Longitude},
		Destination: routing.Point{Latitude: r.Destination.Latitude, Longitude: r.Destination.Longitude},
		Waypoints:   waypoints,
	}, nil
}

func routeRelevanceRequestGuard(cors corsMiddleware) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			origin := request.Header.Get("Origin")
			if origin != "" && !cors.allows(origin) {
				writeError(writer, http.StatusForbidden, "origin_not_allowed", "origin is not allowed")
				return
			}
			contentType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
			if err != nil || !strings.EqualFold(contentType, "application/json") {
				writeError(writer, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")
				return
			}
			next.ServeHTTP(writer, request)
		})
	}
}

func ensureJSONBodyIsComplete(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON value")
	}
	return nil
}

func safeDependencyError(err error) error {
	if err == nil {
		return nil
	}
	return errors.New("dependency request failed")
}
