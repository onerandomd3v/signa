package geospatial

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
)

const (
	minLatitude       = -90
	maxLatitude       = 90
	minLongitude      = -180
	maxLongitude      = 180
	maxProximityLimit = 1000
)

var (
	ErrInvalidPoint    = errors.New("invalid geographic point")
	ErrInvalidLocation = errors.New("invalid user location")
	ErrInvalidRadius   = errors.New("invalid proximity radius")
	ErrInvalidLimit    = errors.New("invalid proximity limit")
)

// Point is an exact geographic point. It is restricted to internal use and
// must not be serialized into public responses, logs, or event payloads.
type Point struct {
	Latitude  float64
	Longitude float64
}

// RestrictedUserLocation is the current exact location snapshot for a user.
// It intentionally has no history and must remain inside restricted storage
// and internal proximity calculations.
type RestrictedUserLocation struct {
	UserID         uuid.UUID
	Point          Point
	AccuracyMeters *float64
	ObservedAt     time.Time
}

// ProximityResult contains only the data needed by downstream proximity
// policy. The stored exact point is deliberately not returned.
type ProximityResult struct {
	UserID         uuid.UUID `json:"user_id"`
	DistanceMeters float64   `json:"distance_meters"`
	ObservedAt     time.Time `json:"observed_at"`
}

func (p Point) Validate() error {
	if !finiteInRange(p.Latitude, minLatitude, maxLatitude) {
		return fmt.Errorf("%w: latitude must be between -90 and 90", ErrInvalidPoint)
	}
	if !finiteInRange(p.Longitude, minLongitude, maxLongitude) {
		return fmt.Errorf("%w: longitude must be between -180 and 180", ErrInvalidPoint)
	}
	return nil
}

func (l RestrictedUserLocation) Validate() error {
	if l.UserID == uuid.Nil {
		return fmt.Errorf("%w: user_id is required", ErrInvalidLocation)
	}
	if err := l.Point.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidLocation, err)
	}
	if l.AccuracyMeters != nil && (!finite(*l.AccuracyMeters) || *l.AccuracyMeters < 0) {
		return fmt.Errorf("%w: accuracy_meters must be finite and non-negative", ErrInvalidLocation)
	}
	if l.ObservedAt.IsZero() {
		return fmt.Errorf("%w: observed_at is required", ErrInvalidLocation)
	}
	return nil
}

func validateProximity(point Point, radiusMeters float64, limit int) error {
	if err := point.Validate(); err != nil {
		return err
	}
	if !finite(radiusMeters) || radiusMeters <= 0 {
		return fmt.Errorf("%w: radius must be finite and positive", ErrInvalidRadius)
	}
	if limit < 0 || limit > maxProximityLimit {
		return fmt.Errorf("%w: limit must be between 0 and %d", ErrInvalidLimit, maxProximityLimit)
	}
	return nil
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func finiteInRange(value, min, max float64) bool {
	return finite(value) && value >= min && value <= max
}
