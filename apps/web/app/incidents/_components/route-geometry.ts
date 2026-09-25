export type RoutePosition = [longitude: number, latitude: number];

/** Display-only GeoJSON shape; it is not an API request/response type. */
export type RouteLineGeometry = {
  type: "LineString";
  coordinates: RoutePosition[];
};

export type RouteFeatureCollection = {
  type: "FeatureCollection";
  features: {
    type: "Feature";
    id: "selected-route";
    geometry: RouteLineGeometry;
    properties: Record<string, never>;
  }[];
};

export type Bounds = [[number, number], [number, number]];

function isPosition(value: unknown): value is RoutePosition {
  return (
    Array.isArray(value) &&
    value.length === 2 &&
    typeof value[0] === "number" &&
    Number.isFinite(value[0]) &&
    value[0] >= -180 &&
    value[0] <= 180 &&
    typeof value[1] === "number" &&
    Number.isFinite(value[1]) &&
    value[1] >= -90 &&
    value[1] <= 90
  );
}

function validatedLine(value: unknown): RouteLineGeometry | null {
  if (!value || typeof value !== "object") return null;
  const geometry = value as { type?: unknown; coordinates?: unknown };
  if (
    geometry.type !== "LineString" ||
    !Array.isArray(geometry.coordinates) ||
    geometry.coordinates.length < 2 ||
    !geometry.coordinates.every(isPosition)
  ) {
    return null;
  }
  return {
    type: "LineString",
    coordinates: geometry.coordinates,
  };
}

export function toRouteFeatureCollection(
  geometry: unknown,
): RouteFeatureCollection {
  const line = validatedLine(geometry);
  return {
    type: "FeatureCollection",
    features: line
      ? [
          {
            type: "Feature",
            id: "selected-route",
            geometry: line,
            properties: {},
          },
        ]
      : [],
  };
}

export function getRouteBounds(geometry: unknown): Bounds | null {
  const line = validatedLine(geometry);
  if (!line) return null;

  let west = Infinity;
  let south = Infinity;
  let east = -Infinity;
  let north = -Infinity;
  for (const [longitude, latitude] of line.coordinates) {
    west = Math.min(west, longitude);
    south = Math.min(south, latitude);
    east = Math.max(east, longitude);
    north = Math.max(north, latitude);
  }
  return [
    [west, south],
    [east, north],
  ];
}

export function combineBounds(
  first: Bounds | null,
  second: Bounds | null,
): Bounds | null {
  if (!first) return second;
  if (!second) return first;
  return [
    [Math.min(first[0][0], second[0][0]), Math.min(first[0][1], second[0][1])],
    [Math.max(first[1][0], second[1][0]), Math.max(first[1][1], second[1][1])],
  ];
}
