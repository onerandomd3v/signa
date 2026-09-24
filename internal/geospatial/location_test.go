package geospatial

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPointValidation(t *testing.T) {
	tests := []struct {
		name  string
		point Point
		want  bool
	}{
		{name: "valid", point: Point{Latitude: 6.5244, Longitude: 3.3792}, want: true},
		{name: "latitude lower boundary", point: Point{Latitude: -90, Longitude: 0}, want: true},
		{name: "longitude upper boundary", point: Point{Latitude: 0, Longitude: 180}, want: true},
		{name: "latitude out of range", point: Point{Latitude: 90.1, Longitude: 0}},
		{name: "longitude out of range", point: Point{Latitude: 0, Longitude: -180.1}},
		{name: "nan latitude", point: Point{Latitude: math.NaN(), Longitude: 0}},
		{name: "infinite longitude", point: Point{Latitude: 0, Longitude: math.Inf(1)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.point.Validate()
			if (err == nil) != test.want {
				t.Fatalf("Validate() error = %v, want valid = %v", err, test.want)
			}
			if err != nil && !errors.Is(err, ErrInvalidPoint) {
				t.Fatalf("Validate() error = %v, want ErrInvalidPoint", err)
			}
		})
	}
}

func TestRestrictedUserLocationValidation(t *testing.T) {
	userID := uuid.New()
	observedAt := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	validAccuracy := 12.5
	valid := RestrictedUserLocation{
		UserID:         userID,
		Point:          Point{Latitude: 6.5244, Longitude: 3.3792},
		AccuracyMeters: &validAccuracy,
		ObservedAt:     observedAt,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid location rejected: %v", err)
	}
	for name, location := range map[string]RestrictedUserLocation{
		"nil user":      {Point: valid.Point, ObservedAt: observedAt},
		"zero observed": {UserID: userID, Point: valid.Point},
		"negative accuracy": func() RestrictedUserLocation {
			accuracy := -1.0
			valid.AccuracyMeters = &accuracy
			return valid
		}(),
		"nan accuracy": func() RestrictedUserLocation {
			accuracy := math.NaN()
			valid.AccuracyMeters = &accuracy
			return valid
		}(),
		"infinite accuracy": func() RestrictedUserLocation {
			accuracy := math.Inf(1)
			valid.AccuracyMeters = &accuracy
			return valid
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			if err := location.Validate(); !errors.Is(err, ErrInvalidLocation) {
				t.Fatalf("Validate() error = %v, want ErrInvalidLocation", err)
			}
		})
	}
}

func TestProximityValidation(t *testing.T) {
	point := Point{Latitude: 6.5, Longitude: 3.3}
	for name, radius := range map[string]float64{"zero": 0, "negative": -1, "nan": math.NaN(), "infinite": math.Inf(1)} {
		t.Run(name, func(t *testing.T) {
			if err := validateProximity(point, radius, 0); !errors.Is(err, ErrInvalidRadius) {
				t.Fatalf("validateProximity() error = %v, want ErrInvalidRadius", err)
			}
		})
	}
	if err := validateProximity(point, 100, maxProximityLimit+1); !errors.Is(err, ErrInvalidLimit) {
		t.Fatalf("validateProximity() error = %v, want ErrInvalidLimit", err)
	}
	if err := (RestrictedUserLocation{UserID: uuid.New(), Point: point, ObservedAt: time.Now()}).Validate(); err != nil {
		t.Fatal(err)
	}
	validAsOf := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	validQuery := ProximityQuery{Target: point, RadiusMeters: 100, AsOf: validAsOf, MaxAge: time.Hour}
	if err := validQuery.Validate(); err != nil {
		t.Fatalf("valid proximity query rejected: %v", err)
	}
	for name, query := range map[string]ProximityQuery{
		"zero as_of":       {Target: point, RadiusMeters: 100, MaxAge: time.Hour},
		"zero max age":     {Target: point, RadiusMeters: 100, AsOf: validAsOf},
		"negative max age": {Target: point, RadiusMeters: 100, AsOf: validAsOf, MaxAge: -time.Second},
	} {
		t.Run(name, func(t *testing.T) {
			err := query.Validate()
			if name == "zero as_of" && !errors.Is(err, ErrInvalidAsOf) {
				t.Fatalf("Validate() error = %v, want ErrInvalidAsOf", err)
			}
			if name != "zero as_of" && !errors.Is(err, ErrInvalidMaxAge) {
				t.Fatalf("Validate() error = %v, want ErrInvalidMaxAge", err)
			}
		})
	}
}
