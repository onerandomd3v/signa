import { describe, expect, it } from "vitest";
import {
  combineBounds,
  getRouteBounds,
  toRouteFeatureCollection,
  type RouteLineGeometry,
} from "./route-geometry";

const routeGeometry: RouteLineGeometry = {
  type: "LineString",
  coordinates: [
    [2, 5],
    [5, 8],
  ],
};

describe("route geometry presentation", () => {
  it("converts an approved line into a map-only GeoJSON feature", () => {
    expect(toRouteFeatureCollection(routeGeometry)).toEqual({
      type: "FeatureCollection",
      features: [
        {
          type: "Feature",
          id: "selected-route",
          geometry: routeGeometry,
          properties: {},
        },
      ],
    });
  });

  it.each([
    null,
    { type: "Point", coordinates: [3, 6] },
    { type: "LineString", coordinates: [[3, 6]] },
    {
      type: "LineString",
      coordinates: [
        [181, 6],
        [4, 7],
      ],
    },
    {
      type: "LineString",
      coordinates: [
        [3, 91],
        [4, 7],
      ],
    },
    {
      type: "LineString",
      coordinates: [
        [3, Number.NaN],
        [4, 7],
      ],
    },
  ])("omits missing, malformed, or unsupported geometry: %o", (value) => {
    expect(toRouteFeatureCollection(value).features).toEqual([]);
    expect(getRouteBounds(value)).toBeNull();
  });

  it("computes camera bounds only and combines route with public areas", () => {
    const routeBounds = getRouteBounds(routeGeometry);
    expect(routeBounds).toEqual([
      [2, 5],
      [5, 8],
    ]);
    expect(
      combineBounds(
        [
          [3, 6],
          [4, 7],
        ],
        routeBounds,
      ),
    ).toEqual([
      [2, 5],
      [5, 8],
    ]);
  });

  it("does not treat visual overlap as a route-relevance result", () => {
    expect(
      toRouteFeatureCollection(routeGeometry).features[0].properties,
    ).toEqual({});
  });
});
