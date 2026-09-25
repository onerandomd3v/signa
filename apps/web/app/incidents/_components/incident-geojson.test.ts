import { describe, expect, it } from "vitest";
import type { PublicIncident } from "@/lib/api/generated";
import {
  getIncidentBounds,
  toIncidentFeatureCollection,
} from "./incident-geojson";

const baseIncident: PublicIncident = {
  id: "public-incident-1",
  event_type: "road_closure",
  status: "OPEN",
  confidence_state: "EMERGING",
  severity: "MODERATE",
  public_geometry: {
    type: "Polygon",
    coordinates: [
      [
        [3, 6],
        [4, 6],
        [4, 7],
        [3, 6],
      ],
    ],
  },
  started_at: "2026-09-25T08:00:00Z",
  last_signal_at: "2026-09-25T08:30:00Z",
  updated_at: "2026-09-25T08:30:00Z",
};

describe("toIncidentFeatureCollection", () => {
  it("preserves valid GeoJSON longitude-latitude order and only public display fields", () => {
    const result = toIncidentFeatureCollection([
      {
        ...baseIncident,
        reporter: "should never pass through",
      } as PublicIncident,
    ]);
    expect(result.features[0].geometry).toEqual(baseIncident.public_geometry);
    expect(result.features[0].geometry.type).toBe("Polygon");
    expect(result.features[0].properties).toEqual({
      id: baseIncident.id,
      event_type: baseIncident.event_type,
      status: baseIncident.status,
      confidence_state: baseIncident.confidence_state,
      severity: baseIncident.severity,
      started_at: baseIncident.started_at,
      last_signal_at: baseIncident.last_signal_at,
      updated_at: baseIncident.updated_at,
    });
    expect(JSON.stringify(result)).not.toMatch(/reporter|latitude|longitude/i);
  });

  it("accepts a valid MultiPolygon", () => {
    const incident: PublicIncident = {
      ...baseIncident,
      public_geometry: {
        type: "MultiPolygon",
        coordinates: [
          [
            [
              [3, 6],
              [4, 6],
              [4, 7],
              [3, 6],
            ],
          ],
        ],
      },
    };
    expect(
      toIncidentFeatureCollection([incident]).features[0].geometry.type,
    ).toBe("MultiPolygon");
  });

  it("fits map bounds using longitude first", () => {
    const features = toIncidentFeatureCollection([baseIncident]);
    expect(getIncidentBounds(features)).toEqual([
      [3, 6],
      [4, 7],
    ]);
    expect(getIncidentBounds({ type: "FeatureCollection", features: [] })).toBe(
      null,
    );
  });

  it.each([
    { type: "Point", coordinates: [3, 6] },
    {
      type: "Polygon",
      coordinates: [
        [
          [181, 6],
          [4, 6],
          [4, 7],
          [181, 6],
        ],
      ],
    },
    {
      type: "Polygon",
      coordinates: [
        [
          [3, 6],
          [4, 6],
          [4, 7],
          [3, 7],
        ],
      ],
    },
    { type: "MultiPolygon", coordinates: [] },
  ])("drops unsupported or malformed public geometry", (geometry) => {
    const incident = {
      ...baseIncident,
      public_geometry: geometry,
    } as unknown as PublicIncident;
    expect(toIncidentFeatureCollection([incident]).features).toEqual([]);
  });
});
