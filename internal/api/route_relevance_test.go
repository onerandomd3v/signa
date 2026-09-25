package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onerandomd3v/signa/internal/auth"
	"github.com/onerandomd3v/signa/internal/config"
	"github.com/onerandomd3v/signa/internal/geospatial"
	"github.com/onerandomd3v/signa/internal/incidents"
	"github.com/onerandomd3v/signa/internal/routing"
)

type routeRelevanceServiceFunc func(context.Context, routing.Request) (RouteRelevanceResult, error)

func (f routeRelevanceServiceFunc) Evaluate(ctx context.Context, request routing.Request) (RouteRelevanceResult, error) {
	return f(ctx, request)
}

func TestRouteRelevanceHandlerAcceptsWaypointsAndReturnsRequestScopedGeometry(t *testing.T) {
	incidentID := uuid.New()
	var got routing.Request
	service := routeRelevanceServiceFunc(func(_ context.Context, request routing.Request) (RouteRelevanceResult, error) {
		got = request
		return RouteRelevanceResult{
			Classification: geospatial.RouteRelevanceRelevant,
			Geometry: routing.GeoJSONLineString{
				Type:        "LineString",
				Coordinates: [][]float64{{3.3, 6.5}, {3.4, 6.6}},
			},
			Incidents: []incidents.PublicIncident{{ID: incidentID, EventType: stringPointer("road_blockage")}},
		}, nil
	})

	record := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/route-relevance", strings.NewReader(`{"origin":{"latitude":6.5,"longitude":3.3},"destination":{"latitude":6.6,"longitude":3.4},"waypoints":[{"latitude":6.55,"longitude":3.35}]}`))
	request.Header.Set("Content-Type", "application/json")
	routeRelevanceHandler(nil, service).ServeHTTP(record, request)

	if record.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", record.Code, record.Body.String())
	}
	if len(got.Waypoints) != 1 || got.Waypoints[0].Latitude != 6.55 || got.Waypoints[0].Longitude != 3.35 {
		t.Fatalf("request = %#v, want waypoint forwarded to service", got)
	}
	if strings.Contains(record.Body.String(), "reporter") || strings.Contains(record.Body.String(), "center_point") {
		t.Fatalf("response exposed private incident fields: %s", record.Body.String())
	}
	var response RouteRelevanceResponse
	if err := json.Unmarshal(record.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Classification != geospatial.RouteRelevanceRelevant || response.Geometry.Type != "LineString" || len(response.Incidents) != 1 {
		t.Fatalf("response = %#v, want relevant geometry and incident reference", response)
	}
}

func TestRouteRelevanceHandlerPreservesUnknownAndDoesNotMapDependencyFailureToNotRelevant(t *testing.T) {
	tests := []struct {
		name       string
		service    routeRelevanceService
		wantStatus int
		wantClass  geospatial.RouteRelevance
	}{
		{name: "unknown successful evaluation", service: routeRelevanceServiceFunc(func(context.Context, routing.Request) (RouteRelevanceResult, error) {
			return RouteRelevanceResult{Classification: geospatial.RouteRelevanceUnknown, Geometry: routing.GeoJSONLineString{Type: "LineString", Coordinates: [][]float64{{3.3, 6.5}, {3.4, 6.6}}}}, nil
		}), wantStatus: http.StatusOK, wantClass: geospatial.RouteRelevanceUnknown},
		{name: "not relevant successful evaluation", service: routeRelevanceServiceFunc(func(context.Context, routing.Request) (RouteRelevanceResult, error) {
			return RouteRelevanceResult{Classification: geospatial.RouteRelevanceNotRelevant, Geometry: routing.GeoJSONLineString{Type: "LineString", Coordinates: [][]float64{{3.3, 6.5}, {3.4, 6.6}}}}, nil
		}), wantStatus: http.StatusOK, wantClass: geospatial.RouteRelevanceNotRelevant},
		{name: "provider failure", service: routeRelevanceServiceFunc(func(context.Context, routing.Request) (RouteRelevanceResult, error) {
			return RouteRelevanceResult{}, ErrRouteDependencyUnavailable
		}), wantStatus: http.StatusServiceUnavailable},
		{name: "provider timeout", service: routeRelevanceServiceFunc(func(context.Context, routing.Request) (RouteRelevanceResult, error) {
			return RouteRelevanceResult{}, ErrRouteDependencyUnavailable
		}), wantStatus: http.StatusServiceUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/v1/route-relevance", strings.NewReader(`{"origin":{"latitude":6.5,"longitude":3.3},"destination":{"latitude":6.6,"longitude":3.4}}`))
			request.Header.Set("Content-Type", "application/json")
			routeRelevanceHandler(nil, test.service).ServeHTTP(record, request)
			if record.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d: %s", record.Code, test.wantStatus, record.Body.String())
			}
			if test.wantClass != "" {
				var response RouteRelevanceResponse
				if err := json.Unmarshal(record.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				if response.Classification != test.wantClass {
					t.Fatalf("classification = %q, want %q", response.Classification, test.wantClass)
				}
			}
		})
	}
}

func TestRouteRelevanceHandlerRejectsInvalidInput(t *testing.T) {
	called := false
	service := routeRelevanceServiceFunc(func(context.Context, routing.Request) (RouteRelevanceResult, error) {
		called = true
		return RouteRelevanceResult{}, nil
	})
	record := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/route-relevance", strings.NewReader(`{"origin":{"latitude":91,"longitude":3.3},"destination":{"latitude":6.6,"longitude":3.4}}`))
	request.Header.Set("Content-Type", "application/json")
	routeRelevanceHandler(nil, service).ServeHTTP(record, request)
	if record.Code != http.StatusBadRequest || called {
		t.Fatalf("status = %d, called = %v, want 400 without service call", record.Code, called)
	}
}

func TestRouteRelevanceHandlerRejectsOmittedEndpoints(t *testing.T) {
	called := false
	service := routeRelevanceServiceFunc(func(context.Context, routing.Request) (RouteRelevanceResult, error) {
		called = true
		return RouteRelevanceResult{}, nil
	})
	record := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/route-relevance", strings.NewReader(`{"origin":{"latitude":6.5,"longitude":3.3}}`))
	request.Header.Set("Content-Type", "application/json")
	routeRelevanceHandler(nil, service).ServeHTTP(record, request)
	if record.Code != http.StatusBadRequest || called {
		t.Fatalf("status = %d, called = %v, want 400 without service call", record.Code, called)
	}
}

func TestRouteRelevanceHandlerMapsIncidentFailureToServiceUnavailable(t *testing.T) {
	record := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/route-relevance", strings.NewReader(`{"origin":{"latitude":6.5,"longitude":3.3},"destination":{"latitude":6.6,"longitude":3.4}}`))
	request.Header.Set("Content-Type", "application/json")
	service := routeRelevanceServiceFunc(func(context.Context, routing.Request) (RouteRelevanceResult, error) {
		return RouteRelevanceResult{}, errors.Join(ErrIncidentDependencyUnavailable, errors.New("database unavailable"))
	})
	routeRelevanceHandler(nil, service).ServeHTTP(record, request)
	if record.Code != http.StatusServiceUnavailable || strings.Contains(record.Body.String(), "database unavailable") {
		t.Fatalf("response = %d %s, want sanitized 503", record.Code, record.Body.String())
	}
}

type routeProviderFunc func(context.Context, routing.Request) (routing.Route, error)

func (f routeProviderFunc) Route(ctx context.Context, request routing.Request) (routing.Route, error) {
	return f(ctx, request)
}

type routeIncidentReaderFunc func(context.Context, routing.GeoJSONLineString, config.PublicIncidentGeometryPolicy) (geospatial.RouteRelevance, []incidents.PublicIncident, error)

func (f routeIncidentReaderFunc) FindPublicRouteRelevance(ctx context.Context, geometry routing.GeoJSONLineString, policy config.PublicIncidentGeometryPolicy) (geospatial.RouteRelevance, []incidents.PublicIncident, error) {
	return f(ctx, geometry, policy)
}

func TestRouteRelevanceServerUsesCanonicalCookieAuthAndDedicatedRateLimit(t *testing.T) {
	userID := uuid.New()
	providerCalls := 0
	server := NewHandlerWithMediaAndCORSAndPublicIncidentsAndAuth(nil, RateLimitConfig{
		PerClientRouteRatePerMinute: 1,
		PerClientRouteBurst:         1,
		GlobalRouteRatePerMinute:    120,
		GlobalRouteBurst:            30,
	}, nil, nil, nil, nil, config.PublicIncidentGeometryPolicy{}, AuthConfig{
		Store: sessionStoreForAPITest{secret: "cookie-secret", principal: auth.Principal{UserID: userID}},
		RouteRelevance: RouteRelevanceConfig{
			Provider: routeProviderFunc(func(context.Context, routing.Request) (routing.Route, error) {
				providerCalls++
				return routing.Route{Geometry: routing.GeoJSONLineString{Type: "LineString", Coordinates: [][]float64{{3.3, 6.5}, {3.4, 6.6}}}}, nil
			}),
			Reader: routeIncidentReaderFunc(func(context.Context, routing.GeoJSONLineString, config.PublicIncidentGeometryPolicy) (geospatial.RouteRelevance, []incidents.PublicIncident, error) {
				return geospatial.RouteRelevanceNotRelevant, nil, nil
			}),
		},
	})
	body := `{"origin":{"latitude":6.5,"longitude":3.3},"destination":{"latitude":6.6,"longitude":3.4}}`
	unauthenticated := httptest.NewRequest(http.MethodPost, "/v1/route-relevance", strings.NewReader(body))
	unauthenticated.Header.Set("Content-Type", "application/json")
	unauthenticatedResponse := httptest.NewRecorder()
	server.ServeHTTP(unauthenticatedResponse, unauthenticated)
	if unauthenticatedResponse.Code != http.StatusUnauthorized || providerCalls != 0 {
		t.Fatalf("unauthenticated status = %d, calls = %d, want 401 and no provider call", unauthenticatedResponse.Code, providerCalls)
	}

	for _, test := range []struct {
		name        string
		origin      string
		contentType string
		wantStatus  int
	}{
		{name: "disallowed origin", origin: "https://evil.example", contentType: "application/json", wantStatus: http.StatusForbidden},
		{name: "non-json body", contentType: "text/plain", wantStatus: http.StatusUnsupportedMediaType},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/v1/route-relevance", strings.NewReader(body))
			request.Header.Set("Content-Type", test.contentType)
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			request.AddCookie(&http.Cookie{Name: auth.DefaultCookieName, Value: "cookie-secret"})
			response := httptest.NewRecorder()
			server.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d: %s", response.Code, test.wantStatus, response.Body.String())
			}
		})
	}

	for i, wantStatus := range []int{http.StatusOK, http.StatusTooManyRequests} {
		request := httptest.NewRequest(http.MethodPost, "/v1/route-relevance", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.AddCookie(&http.Cookie{Name: auth.DefaultCookieName, Value: "cookie-secret"})
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		if response.Code != wantStatus {
			t.Fatalf("request %d status = %d, want %d: %s", i+1, response.Code, wantStatus, response.Body.String())
		}
	}
	if providerCalls != 1 {
		t.Fatalf("provider calls = %d, want one successful request", providerCalls)
	}
}
