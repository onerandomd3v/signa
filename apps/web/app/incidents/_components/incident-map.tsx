"use client";

import { useEffect, useRef } from "react";
import {
  Map as MapLibreMap,
  NavigationControl,
  setWorkerUrl,
} from "maplibre-gl";
import type { GeoJSONSource, MapLayerMouseEvent } from "maplibre-gl";
import {
  getIncidentBounds,
  type IncidentFeatureCollection,
} from "./incident-geojson";
import {
  combineBounds,
  getRouteBounds,
  toRouteFeatureCollection,
  type RouteLineGeometry,
} from "./route-geometry";

setWorkerUrl("/maplibre/maplibre-gl-worker.mjs");

const SOURCE_ID = "public-incidents";
const FILL_LAYER_ID = "public-incident-areas";
const LINE_LAYER_ID = "public-incident-outlines";
const ROUTE_SOURCE_ID = "selected-route";
const ROUTE_LAYER_ID = "selected-route-line";

export function IncidentMap({
  styleUrl,
  featureCollection,
  routeGeometry,
  selectedId,
  onSelect,
  onUnavailable,
}: {
  styleUrl: string;
  featureCollection: IncidentFeatureCollection;
  routeGeometry: RouteLineGeometry | null;
  selectedId: string | null;
  onSelect: (id: string) => void;
  onUnavailable: () => void;
}) {
  const containerRef = useRef<HTMLDivElement>(null);
  const mapRef = useRef<MapLibreMap | null>(null);
  const onSelectRef = useRef(onSelect);
  const onUnavailableRef = useRef(onUnavailable);
  const featuresRef = useRef(featureCollection);
  const routeGeometryRef = useRef(routeGeometry);
  const selectedRef = useRef(selectedId);

  useEffect(() => {
    onSelectRef.current = onSelect;
    onUnavailableRef.current = onUnavailable;
    featuresRef.current = featureCollection;
    routeGeometryRef.current = routeGeometry;
    selectedRef.current = selectedId;
  }, [featureCollection, onSelect, onUnavailable, routeGeometry, selectedId]);

  useEffect(() => {
    if (!containerRef.current) return;

    let createdMap: MapLibreMap | null = null;
    let disposed = false;
    let failed = false;
    let styleLoaded = false;
    let styleLoadTimeout: number | null = null;
    const clearStyleLoadTimeout = () => {
      if (styleLoadTimeout !== null) {
        window.clearTimeout(styleLoadTimeout);
        styleLoadTimeout = null;
      }
    };
    const notifyUnavailable = () => {
      if (disposed || failed) return;
      failed = true;
      clearStyleLoadTimeout();
      onUnavailableRef.current();
    };

    try {
      const map = new MapLibreMap({
        container: containerRef.current,
        style: styleUrl,
        center: [0, 15],
        zoom: 1.5,
        cooperativeGestures: true,
      });
      createdMap = map;
      mapRef.current = map;
      map.addControl(new NavigationControl(), "top-right");
      styleLoadTimeout = window.setTimeout(() => {
        if (!styleLoaded) notifyUnavailable();
      }, 15_000);

      map.on("style.load", () => {
        if (disposed || failed) return;
        styleLoaded = true;
        clearStyleLoadTimeout();
        try {
          map.addSource(SOURCE_ID, {
            type: "geojson",
            data: featuresRef.current as never,
          });
          map.addLayer({
            id: FILL_LAYER_ID,
            type: "fill",
            source: SOURCE_ID,
            paint: {
              "fill-color": "#176a57",
              "fill-opacity": [
                "case",
                ["==", ["get", "id"], selectedRef.current ?? ""],
                0.48,
                0.24,
              ],
            },
          });
          map.addLayer({
            id: LINE_LAYER_ID,
            type: "line",
            source: SOURCE_ID,
            paint: { "line-color": "#176a57", "line-width": 2 },
          });
          map.addSource(ROUTE_SOURCE_ID, {
            type: "geojson",
            data: toRouteFeatureCollection(routeGeometryRef.current) as never,
          });
          map.addLayer({
            id: ROUTE_LAYER_ID,
            type: "line",
            source: ROUTE_SOURCE_ID,
            paint: { "line-color": "#1d4ed8", "line-width": 4 },
          });
          const bounds = combineBounds(
            getIncidentBounds(featuresRef.current),
            getRouteBounds(routeGeometryRef.current),
          );
          if (bounds) {
            map.fitBounds(bounds, { padding: 48, maxZoom: 11, duration: 0 });
          }
          map.on("click", FILL_LAYER_ID, (event: MapLayerMouseEvent) => {
            const id = event.features?.[0]?.properties?.id;
            if (typeof id === "string") onSelectRef.current(id);
          });
          map.on("mouseenter", FILL_LAYER_ID, () => {
            map.getCanvas().style.cursor = "pointer";
          });
          map.on("mouseleave", FILL_LAYER_ID, () => {
            map.getCanvas().style.cursor = "";
          });
        } catch {
          notifyUnavailable();
        }
      });
      map.on("error", () => {
        // Source and tile errors can bubble to the map. Keep the text list
        // available and let the style-load timeout detect a base-style failure.
      });

      return () => {
        disposed = true;
        clearStyleLoadTimeout();
        map.remove();
        if (mapRef.current === map) mapRef.current = null;
      };
    } catch {
      try {
        createdMap?.remove();
      } catch {
        // Continue to the text fallback even if partial map cleanup fails.
      }
      if (mapRef.current === createdMap) mapRef.current = null;
      notifyUnavailable();
    }
  }, [styleUrl]);

  useEffect(() => {
    const map = mapRef.current;
    if (!map?.isStyleLoaded() || !map.getSource(SOURCE_ID)) return;
    (map.getSource(SOURCE_ID) as GeoJSONSource).setData(
      featureCollection as never,
    );
    const routeSource = map.getSource(ROUTE_SOURCE_ID) as
      GeoJSONSource | undefined;
    routeSource?.setData(toRouteFeatureCollection(routeGeometry) as never);
    const bounds = combineBounds(
      getIncidentBounds(featureCollection),
      getRouteBounds(routeGeometry),
    );
    if (bounds) map.fitBounds(bounds, { padding: 48, maxZoom: 11 });
    map.setPaintProperty(FILL_LAYER_ID, "fill-opacity", [
      "case",
      ["==", ["get", "id"], selectedId ?? ""],
      0.48,
      0.24,
    ]);
  }, [featureCollection, routeGeometry, selectedId]);

  return (
    <div
      aria-label={
        toRouteFeatureCollection(routeGeometry).features.length > 0
          ? "Map of public generalized incident areas and selected route"
          : "Map of public generalized incident areas"
      }
      className="h-[min(58svh,34rem)] min-h-64 w-full overflow-hidden rounded-lg bg-muted"
      ref={containerRef}
      role="region"
    />
  );
}
