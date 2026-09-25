import type { PublicIncident } from "@/lib/api/generated";

type Position = [number, number];
type PolygonGeometry = { type: "Polygon"; coordinates: Position[][] };
type MultiPolygonGeometry = {
  type: "MultiPolygon";
  coordinates: Position[][][];
};
type IncidentGeometry = PolygonGeometry | MultiPolygonGeometry;

export type IncidentFeature = {
  type: "Feature";
  id: string;
  geometry: IncidentGeometry;
  properties: {
    id: string;
    event_type: string | null;
    status: string;
    confidence_state: string;
    severity: string | null;
    started_at: string | null;
    last_signal_at: string | null;
    updated_at: string;
  };
};

export type IncidentFeatureCollection = {
  type: "FeatureCollection";
  features: IncidentFeature[];
};

function isPosition(value: unknown): value is Position {
  return (
    Array.isArray(value) &&
    value.length >= 2 &&
    value.length <= 3 &&
    typeof value[0] === "number" &&
    Number.isFinite(value[0]) &&
    value[0] >= -180 &&
    value[0] <= 180 &&
    typeof value[1] === "number" &&
    Number.isFinite(value[1]) &&
    value[1] >= -90 &&
    value[1] <= 90 &&
    (value.length === 2 ||
      (typeof value[2] === "number" && Number.isFinite(value[2])))
  );
}

function isRing(value: unknown): value is Position[] {
  if (!Array.isArray(value) || value.length < 4 || !value.every(isPosition)) {
    return false;
  }
  const first = value[0];
  const last = value[value.length - 1];
  return first[0] === last[0] && first[1] === last[1];
}

function validatedGeometry(geometry: unknown): IncidentGeometry | null {
  if (!geometry || typeof geometry !== "object") return null;
  const value = geometry as { type?: unknown; coordinates?: unknown };
  if (value.type === "Polygon") {
    const rings = value.coordinates;
    if (Array.isArray(rings) && rings.length > 0 && rings.every(isRing)) {
      return { type: "Polygon", coordinates: rings };
    }
    return null;
  }
  if (value.type === "MultiPolygon") {
    const polygons = value.coordinates;
    if (
      Array.isArray(polygons) &&
      polygons.length > 0 &&
      polygons.every(
        (polygon) =>
          Array.isArray(polygon) && polygon.length > 0 && polygon.every(isRing),
      )
    ) {
      return { type: "MultiPolygon", coordinates: polygons };
    }
  }
  return null;
}

export function toIncidentFeatureCollection(
  incidents: PublicIncident[],
): IncidentFeatureCollection {
  return {
    type: "FeatureCollection",
    features: incidents.flatMap((incident) => {
      const geometry = validatedGeometry(incident.public_geometry);
      if (!geometry) return [];
      return [
        {
          type: "Feature" as const,
          id: incident.id,
          geometry,
          // Explicit allowlist: never forward arbitrary API fields or raw location.
          properties: {
            id: incident.id,
            event_type: incident.event_type,
            status: incident.status,
            confidence_state: incident.confidence_state,
            severity: incident.severity,
            started_at: incident.started_at,
            last_signal_at: incident.last_signal_at,
            updated_at: incident.updated_at,
          },
        },
      ];
    }),
  };
}

export function getIncidentBounds(
  collection: IncidentFeatureCollection,
): [[number, number], [number, number]] | null {
  let west = Infinity;
  let south = Infinity;
  let east = -Infinity;
  let north = -Infinity;

  const visit = (value: unknown) => {
    if (!Array.isArray(value)) return;
    if (isPosition(value)) {
      west = Math.min(west, value[0]);
      south = Math.min(south, value[1]);
      east = Math.max(east, value[0]);
      north = Math.max(north, value[1]);
      return;
    }
    value.forEach(visit);
  };

  collection.features.forEach((feature) => visit(feature.geometry.coordinates));
  if (!Number.isFinite(west)) return null;
  return [
    [west, south],
    [east, north],
  ];
}
