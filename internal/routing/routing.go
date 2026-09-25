package routing

import (
	"context"
	"errors"
	"time"
)

const MaxWaypoints = 25

var (
	ErrInvalidRequest  = errors.New("routing request is invalid")
	ErrInvalidResponse = errors.New("routing provider response is invalid")
	ErrNoRoute         = errors.New("routing provider returned no route")
	ErrUpstream        = errors.New("routing provider request failed")
)

type Point struct {
	Latitude  float64
	Longitude float64
}

type Request struct {
	Origin      Point
	Destination Point
	Waypoints   []Point
}

type GeoJSONLineString struct {
	Type        string      `json:"type"`
	Coordinates [][]float64 `json:"coordinates"`
}

type Route struct {
	Geometry       GeoJSONLineString
	DistanceMeters float64
	Duration       time.Duration
}

// Validate checks a request-scoped route geometry before it crosses into a
// spatial query. It does not retain or serialize the geometry.
func (g GeoJSONLineString) Validate() error {
	if g.Type != "LineString" || len(g.Coordinates) < 2 {
		return errors.Join(ErrInvalidRequest, errors.New("route geometry must be a LineString with at least two coordinates"))
	}
	for _, coordinate := range g.Coordinates {
		if len(coordinate) < 2 || !finite(coordinate[0]) || !finite(coordinate[1]) || coordinate[0] < -180 || coordinate[0] > 180 || coordinate[1] < -90 || coordinate[1] > 90 {
			return errors.Join(ErrInvalidRequest, errors.New("route geometry coordinate is invalid"))
		}
	}
	return nil
}

type Provider interface {
	Route(context.Context, Request) (Route, error)
}

func (r Request) validate() error {
	if err := validatePoint(r.Origin); err != nil {
		return err
	}
	if err := validatePoint(r.Destination); err != nil {
		return err
	}
	if len(r.Waypoints) > MaxWaypoints {
		return errors.Join(ErrInvalidRequest, errors.New("waypoint count exceeds limit"))
	}
	for _, waypoint := range r.Waypoints {
		if err := validatePoint(waypoint); err != nil {
			return err
		}
	}
	return nil
}

func validatePoint(point Point) error {
	if !finite(point.Latitude) || !finite(point.Longitude) || point.Latitude < -90 || point.Latitude > 90 || point.Longitude < -180 || point.Longitude > 180 {
		return ErrInvalidRequest
	}
	return nil
}

func finite(value float64) bool {
	return value == value && value > -1.7976931348623157e+308 && value < 1.7976931348623157e+308
}

func validateRoute(route Route) error {
	if err := route.Geometry.Validate(); err != nil {
		return errors.Join(ErrInvalidResponse, err)
	}
	if !finite(route.DistanceMeters) || route.DistanceMeters < 0 || route.Duration < 0 {
		return ErrInvalidResponse
	}
	return nil
}
