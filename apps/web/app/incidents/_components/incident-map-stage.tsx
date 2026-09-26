"use client";

import dynamic from "next/dynamic";
import type { IncidentFeatureCollection } from "./incident-geojson";
import type { RouteLineGeometry } from "./route-geometry";

const MapCanvas = dynamic(
  () => import("./incident-map").then((module) => module.IncidentMap),
  {
    ssr: false,
    loading: () => (
      <div
        aria-label="Loading map"
        className="h-[min(58svh,34rem)] min-h-64 animate-pulse rounded-lg bg-muted motion-reduce:animate-none"
        role="status"
      />
    ),
  },
);

export function IncidentMapStage(props: {
  styleUrl: string;
  featureCollection: IncidentFeatureCollection;
  routeGeometry: RouteLineGeometry | null;
  selectedId: string | null;
  onSelect: (id: string) => void;
  onUnavailable: () => void;
}) {
  return <MapCanvas {...props} />;
}
