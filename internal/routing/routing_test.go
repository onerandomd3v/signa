package routing

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOSRMProviderRouteValidatesAndPreservesGeoJSONOrdering(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/route/v1/driving/3.3792000,6.5244000;3.4000000,6.5000000;3.5000000,6.6000000" {
			t.Errorf("path = %q, want explicit longitude/latitude ordering", got)
		}
		if r.URL.Query().Get("geometries") != "geojson" {
			t.Errorf("geometries = %q", r.URL.Query().Get("geometries"))
		}
		_, _ = io.WriteString(w, `{"code":"Ok","routes":[{"geometry":{"type":"LineString","coordinates":[[3.3792,6.5244],[3.5,6.6]]},"distance":1234.5,"duration":65.25}]}`)
	}))
	defer server.Close()

	provider, err := NewOSRMProvider(OSRMConfig{BaseURL: server.URL, Timeout: time.Second, Client: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	route, err := provider.Route(context.Background(), Request{
		Origin: Point{Latitude: 6.5244, Longitude: 3.3792}, Destination: Point{Latitude: 6.6, Longitude: 3.5},
		Waypoints: []Point{{Latitude: 6.5, Longitude: 3.4}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if route.Geometry.Type != "LineString" || route.Geometry.Coordinates[0][0] != 3.3792 || route.Geometry.Coordinates[0][1] != 6.5244 || route.DistanceMeters != 1234.5 || route.Duration != 65*time.Second+250*time.Millisecond {
		t.Fatalf("route = %+v", route)
	}
}

func TestRequestValidation(t *testing.T) {
	valid := Request{Origin: Point{Latitude: 0, Longitude: 0}, Destination: Point{Latitude: 1, Longitude: 1}}
	for _, point := range []Point{{Latitude: 91}, {Longitude: 181}, {Latitude: math.NaN()}, {Longitude: math.Inf(1)}} {
		testRequest := valid
		testRequest.Origin = point
		if err := testRequest.validate(); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("point %+v error = %v", point, err)
		}
	}
	tooMany := valid
	tooMany.Waypoints = make([]Point, MaxWaypoints+1)
	if err := tooMany.validate(); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("waypoint error = %v", err)
	}
}

func TestGeoJSONLineStringValidation(t *testing.T) {
	valid := GeoJSONLineString{Type: "LineString", Coordinates: [][]float64{{3.3, 6.5}, {3.4, 6.6}}}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid geometry rejected: %v", err)
	}
	for _, geometry := range []GeoJSONLineString{
		{Type: "Point", Coordinates: [][]float64{{3.3, 6.5}, {3.4, 6.6}}},
		{Type: "LineString", Coordinates: [][]float64{{3.3, 6.5}}},
		{Type: "LineString", Coordinates: [][]float64{{181, 6.5}, {3.4, 6.6}}},
		{Type: "LineString", Coordinates: [][]float64{{3.3, 91}, {3.4, 6.6}}},
		{Type: "LineString", Coordinates: [][]float64{{math.NaN(), 6.5}, {3.4, 6.6}}},
	} {
		if err := geometry.Validate(); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("geometry %+v error = %v, want ErrInvalidRequest", geometry, err)
		}
	}
}

func TestOSRMProviderRejectsInvalidGeometryAndMetrics(t *testing.T) {
	cases := []string{
		`{"code":"Ok","routes":[{"geometry":{"type":"Point","coordinates":[[1,2],[3,4]]},"distance":1,"duration":1}]}`,
		`{"code":"Ok","routes":[{"geometry":{"type":"LineString","coordinates":[[181,2],[3,4]]},"distance":1,"duration":1}]}`,
		`{"code":"Ok","routes":[{"geometry":{"type":"LineString","coordinates":[[1,2],[3,4]]},"distance":-1,"duration":1}]}`,
		`{"code":"Ok","routes":[{"geometry":{"type":"LineString","coordinates":[[1,2],[3,4]]},"distance":1,"duration":-1}]}`,
	}
	for index, body := range cases {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, body) }))
			defer server.Close()
			provider, err := NewOSRMProvider(OSRMConfig{BaseURL: server.URL, Timeout: time.Second, Client: server.Client()})
			if err != nil {
				t.Fatal(err)
			}
			_, err = provider.Route(context.Background(), validRequest())
			if !errors.Is(err, ErrInvalidResponse) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestOSRMProviderErrorsAndCancellation(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{name: "4xx", status: http.StatusBadRequest, body: `{"secret":"must not escape"}`, want: ErrUpstream},
		{name: "5xx", status: http.StatusBadGateway, body: "upstream details", want: ErrUpstream},
		{name: "malformed", status: http.StatusOK, body: "{", want: ErrInvalidResponse},
		{name: "no route", status: http.StatusOK, body: `{"code":"NoRoute","routes":[]}`, want: ErrNoRoute},
		{name: "missing code", status: http.StatusOK, body: `{"routes":[{"geometry":{"type":"LineString","coordinates":[[3.3,6.5],[3.4,6.6]]},"distance":10,"duration":5}]}`, want: ErrInvalidResponse},
		{name: "unexpected code without routes", status: http.StatusOK, body: `{"code":"InvalidQuery","routes":[]}`, want: ErrUpstream},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			provider, err := NewOSRMProvider(OSRMConfig{BaseURL: server.URL, Timeout: time.Second, Client: server.Client()})
			if err != nil {
				t.Fatal(err)
			}
			_, err = provider.Route(context.Background(), validRequest())
			if !errors.Is(err, test.want) || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "upstream details") {
				t.Fatalf("error = %v, want safe category %v", err, test.want)
			}
		})
	}

	delayServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer delayServer.Close()
	provider, err := NewOSRMProvider(OSRMConfig{BaseURL: delayServer.URL, Timeout: 20 * time.Millisecond, Client: delayServer.Client()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.Route(context.Background(), validRequest())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout error = %v", err)
	}
	bodyStallServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer bodyStallServer.Close()
	bodyStallProvider, err := NewOSRMProvider(OSRMConfig{BaseURL: bodyStallServer.URL, Timeout: 20 * time.Millisecond, Client: bodyStallServer.Client()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = bodyStallProvider.Route(context.Background(), validRequest())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("body-read timeout error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = provider.Route(ctx, validRequest())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error = %v", err)
	}
}

func TestProviderFactoryAndConfigurationValidation(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	if _, err := NewProvider(Config{Provider: "unknown", BaseURL: server.URL, Timeout: time.Second}, server.Client()); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("unknown provider error = %v", err)
	}
	for _, config := range []OSRMConfig{{BaseURL: "", Timeout: time.Second}, {BaseURL: server.URL, Timeout: 0}, {BaseURL: "ftp://routing.test", Timeout: time.Second}, {BaseURL: "http://user:pass@routing.test", Timeout: time.Second}} {
		if _, err := NewOSRMProvider(config); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("config %+v error = %v", config, err)
		}
	}
}

func TestRoutingDoesNotLogSensitiveCoordinates(t *testing.T) {
	var output strings.Builder
	logger := slog.New(slog.NewTextHandler(&output, nil))
	logger.Error("routing failed", "provider", "osrm", "category", "upstream")
	if strings.Contains(output.String(), "6.5244") || strings.Contains(output.String(), "3.3792") {
		t.Fatal("routing log contains sensitive coordinates")
	}
	if strings.Contains(ErrUpstream.Error(), "route") && strings.Contains(ErrUpstream.Error(), "3.") {
		t.Fatal("upstream error contains coordinates")
	}
}

func validRequest() Request {
	return Request{Origin: Point{Latitude: 6.5244, Longitude: 3.3792}, Destination: Point{Latitude: 6.6, Longitude: 3.5}}
}
