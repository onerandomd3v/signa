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

setWorkerUrl("/maplibre/maplibre-gl-worker.mjs");

const SOURCE_ID = "public-incidents";
const FILL_LAYER_ID = "public-incident-areas";
const LINE_LAYER_ID = "public-incident-outlines";

export function IncidentMap({
  styleUrl,
  featureCollection,
  selectedId,
  onSelect,
  onUnavailable,
}: {
  styleUrl: string;
  featureCollection: IncidentFeatureCollection;
  selectedId: string | null;
  onSelect: (id: string) => void;
  onUnavailable: () => void;
}) {
  const containerRef = useRef<HTMLDivElement>(null);
  const mapRef = useRef<MapLibreMap | null>(null);
  const onSelectRef = useRef(onSelect);
  const onUnavailableRef = useRef(onUnavailable);
  const featuresRef = useRef(featureCollection);
  const selectedRef = useRef(selectedId);

  useEffect(() => {
    onSelectRef.current = onSelect;
    onUnavailableRef.current = onUnavailable;
    featuresRef.current = featureCollection;
    selectedRef.current = selectedId;
  }, [featureCollection, onSelect, onUnavailable, selectedId]);

  useEffect(() => {
    if (!containerRef.current) return;

    try {
      const map = new MapLibreMap({
        container: containerRef.current,
        style: styleUrl,
        center: [0, 15],
        zoom: 1.5,
        cooperativeGestures: true,
      });
      mapRef.current = map;
      map.addControl(new NavigationControl(), "top-right");
      map.on("load", () => {
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
          const bounds = getIncidentBounds(featuresRef.current);
          if (bounds) {
            map.fitBounds(bounds, { padding: 48, maxZoom: 11, duration: 0 });
          }
          map.addLayer({
            id: LINE_LAYER_ID,
            type: "line",
            source: SOURCE_ID,
            paint: { "line-color": "#176a57", "line-width": 2 },
          });
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
          onUnavailableRef.current();
        }
      });
      map.on("error", () => onUnavailableRef.current());
    } catch {
      onUnavailableRef.current();
    }

    return () => {
      mapRef.current?.remove();
      mapRef.current = null;
    };
  }, [styleUrl]);

  useEffect(() => {
    const map = mapRef.current;
    if (!map?.isStyleLoaded() || !map.getSource(SOURCE_ID)) return;
    (map.getSource(SOURCE_ID) as GeoJSONSource).setData(
      featureCollection as never,
    );
    const bounds = getIncidentBounds(featureCollection);
    if (bounds) map.fitBounds(bounds, { padding: 48, maxZoom: 11 });
    map.setPaintProperty(FILL_LAYER_ID, "fill-opacity", [
      "case",
      ["==", ["get", "id"], selectedId ?? ""],
      0.48,
      0.24,
    ]);
  }, [featureCollection, selectedId]);

  return (
    <div
      aria-label="Map of public generalized incident areas"
      className="h-[min(58svh,34rem)] min-h-64 w-full overflow-hidden rounded-lg bg-muted"
      ref={containerRef}
      role="region"
    />
  );
}
