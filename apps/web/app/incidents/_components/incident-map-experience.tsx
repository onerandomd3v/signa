"use client";

import Link from "next/link";
import { useEffect, useMemo, useState } from "react";
import type { PublicIncident } from "@/lib/api/generated";
import {
  fetchPublicIncidents,
  getPublicMapStyleUrl,
} from "@/lib/api/incidents";
import {
  toIncidentFeatureCollection,
  type IncidentFeatureCollection,
} from "./incident-geojson";
import { IncidentMapStage } from "./incident-map-stage";

type LoadState = "loading" | "error" | "ready";

function label(value: string | null | undefined): string {
  if (!value?.trim()) return "Not provided";
  return value
    .replace(/[_-]+/g, " ")
    .toLocaleLowerCase("en")
    .replace(/\b[a-z]/g, (letter) => letter.toLocaleUpperCase("en"));
}

function freshness(value: string | null): string {
  if (!value || !Number.isFinite(Date.parse(value))) return "Time unavailable";
  const minutes = Math.round((Date.parse(value) - Date.now()) / 60_000);
  const amount = Math.abs(minutes);
  if (amount < 60)
    return new Intl.RelativeTimeFormat("en", { numeric: "auto" }).format(
      minutes,
      "minute",
    );
  const hours = Math.round(minutes / 60);
  if (Math.abs(hours) < 24)
    return new Intl.RelativeTimeFormat("en", { numeric: "auto" }).format(
      hours,
      "hour",
    );
  return new Intl.RelativeTimeFormat("en", { numeric: "auto" }).format(
    Math.round(hours / 24),
    "day",
  );
}

export function IncidentMapExperience({
  mapStyleUrl,
  loadIncidents = fetchPublicIncidents,
}: {
  mapStyleUrl: string | null;
  loadIncidents?: () => Promise<PublicIncident[]>;
}) {
  const [state, setState] = useState<LoadState>("loading");
  const [incidents, setIncidents] = useState<PublicIncident[]>([]);
  const [mapUnavailable, setMapUnavailable] = useState(false);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const featureCollection: IncidentFeatureCollection = useMemo(
    () => toIncidentFeatureCollection(incidents),
    [incidents],
  );

  const load = async () => {
    try {
      setIncidents(await loadIncidents());
      setState("ready");
    } catch {
      setState("error");
    }
  };

  useEffect(() => {
    let active = true;
    loadIncidents()
      .then((loadedIncidents) => {
        if (!active) return;
        setIncidents(loadedIncidents);
        setState("ready");
      })
      .catch(() => {
        if (active) setState("error");
      });
    return () => {
      active = false;
    };
  }, [loadIncidents]);

  if (state === "loading") {
    return (
      <div
        aria-label="Loading incidents"
        className="h-36 animate-pulse rounded-xl border border-border bg-card motion-reduce:animate-none"
        role="status"
      />
    );
  }

  if (state === "error") {
    return (
      <section
        aria-live="assertive"
        className="rounded-xl border border-border bg-card p-5"
        role="alert"
      >
        <h2 className="text-lg font-semibold">Couldn’t load incidents</h2>
        <p className="mt-2 text-sm leading-6 text-muted-foreground">
          Try again. Incident data isn’t available.
        </p>
        <button
          className="mt-4 inline-flex min-h-11 items-center rounded-md bg-primary px-4 text-sm font-semibold text-primary-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
          onClick={() => {
            setState("loading");
            void load();
          }}
          type="button"
        >
          Retry
        </button>
      </section>
    );
  }

  if (incidents.length === 0) {
    return (
      <section
        className="rounded-xl border border-border bg-card p-5"
        role="status"
      >
        <h2 className="text-lg font-semibold">No active incidents</h2>
        <p className="mt-2 text-sm leading-6 text-muted-foreground">
          No public incidents are listed right now. This does not mean an area
          is safe.
        </p>
      </section>
    );
  }

  return (
    <div className="space-y-4">
      {mapStyleUrl && !mapUnavailable ? (
        <section
          aria-label="Incident map"
          className="overflow-hidden rounded-xl border border-border bg-card p-2 sm:p-3"
        >
          <IncidentMapStage
            featureCollection={featureCollection}
            onSelect={setSelectedId}
            onUnavailable={() => setMapUnavailable(true)}
            selectedId={selectedId}
            styleUrl={mapStyleUrl}
          />
        </section>
      ) : (
        <p
          className="rounded-lg border border-border bg-card p-4 text-sm leading-6 text-muted-foreground"
          role="status"
        >
          Map unavailable
          {mapStyleUrl ? " right now" : ": no map style is configured"}.
          Incident details remain available below.
        </p>
      )}

      <ul aria-label="Incidents" className="space-y-3">
        {incidents.map((incident) => (
          <li key={incident.id}>
            <article
              className={`rounded-xl border bg-card p-4 shadow-sm sm:p-5 ${selectedId === incident.id ? "border-primary" : "border-border"}`}
            >
              <div className="flex flex-wrap items-start justify-between gap-3">
                <div className="min-w-0">
                  <h2 className="text-lg font-semibold">
                    {label(incident.event_type)}
                  </h2>
                  <p className="mt-1 text-sm leading-6 text-muted-foreground">
                    {label(incident.status)}
                  </p>
                </div>
                <span className="rounded-full border border-border px-2.5 py-1 text-xs font-semibold">
                  {label(incident.confidence_state)}
                </span>
              </div>
              <dl className="mt-4 grid grid-cols-2 gap-3 border-t border-border pt-4 sm:grid-cols-3">
                <div>
                  <dt className="text-xs font-medium text-muted-foreground">
                    Severity
                  </dt>
                  <dd className="mt-1 text-sm font-medium">
                    {label(incident.severity)}
                  </dd>
                </div>
                <div>
                  <dt className="text-xs font-medium text-muted-foreground">
                    Last signal
                  </dt>
                  <dd className="mt-1 text-sm font-medium">
                    <time dateTime={incident.last_signal_at ?? undefined}>
                      {freshness(incident.last_signal_at)}
                    </time>
                  </dd>
                </div>
                <div>
                  <dt className="text-xs font-medium text-muted-foreground">
                    Updated
                  </dt>
                  <dd className="mt-1 text-sm font-medium">
                    <time dateTime={incident.updated_at}>
                      {freshness(incident.updated_at)}
                    </time>
                  </dd>
                </div>
              </dl>
              <div className="mt-4 flex flex-wrap items-center gap-4">
                <button
                  aria-pressed={selectedId === incident.id}
                  className="inline-flex min-h-11 items-center rounded-md border border-border px-3 text-sm font-semibold hover:bg-muted focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
                  onClick={() =>
                    setSelectedId(
                      selectedId === incident.id ? null : incident.id,
                    )
                  }
                  type="button"
                >
                  {selectedId === incident.id
                    ? "Selected on map"
                    : "Select area"}
                </button>
                <Link
                  className="inline-flex min-h-11 items-center text-sm font-semibold text-primary underline-offset-4 hover:underline focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-ring"
                  href={`/incidents/${encodeURIComponent(incident.id)}`}
                >
                  View details{" "}
                  <span aria-hidden="true" className="ml-1">
                    →
                  </span>
                </Link>
              </div>
              {!featureCollection.features.some(
                (feature) => feature.id === incident.id,
              ) && (
                <p className="mt-3 text-sm text-muted-foreground">
                  Area geometry unavailable.
                </p>
              )}
            </article>
          </li>
        ))}
      </ul>
      <p className="text-sm leading-6 text-muted-foreground">
        Areas are generalized. Confidence is not proof; severity describes
        potential impact.
      </p>
    </div>
  );
}

export function IncidentMapRoute() {
  return <IncidentMapExperience mapStyleUrl={getPublicMapStyleUrl()} />;
}
