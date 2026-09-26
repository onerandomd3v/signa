"use client";

import { useCallback, useEffect, useId, useRef, useState } from "react";
import type { FormEvent, ReactNode } from "react";
import type {
  PublicIncident,
  RouteRelevanceRequest,
} from "@/lib/api/generated";
import {
  requestRouteRelevance,
  type RouteRelevanceResult,
} from "@/lib/api/route-relevance";
import {
  toRouteFeatureCollection,
  type RouteLineGeometry,
} from "./route-geometry";

type RetryState = { onRetry: () => void };

export type RouteRelevanceState =
  | { status: "no-route" }
  | { status: "loading" }
  | { status: "validation-error"; message: string }
  | { status: "unauthorized" | "forbidden" }
  | ({ status: "rate-limited"; retryAfter: string | null } & RetryState)
  | ({ status: "route-unavailable" | "incident-data-unavailable" } & RetryState)
  | ({ status: "service-error" | "network-error" } & RetryState)
  | { status: "unknown" | "relevant" | "not-relevant" };

export type RouteMapResult = {
  geometry: RouteLineGeometry;
  incidents: PublicIncident[];
};

export type RouteRelevanceEvaluator = (
  request: RouteRelevanceRequest,
  signal: AbortSignal,
) => Promise<RouteRelevanceResult>;

type CoordinateInput = { latitude: string; longitude: string };
type RouteFormValues = {
  origin: CoordinateInput;
  destination: CoordinateInput;
  waypoints: CoordinateInput[];
};

const emptyCoordinate = { latitude: "", longitude: "" };
const MAX_WAYPOINTS = 25;
const inputClassName =
  "mt-1 min-h-11 w-full min-w-0 rounded-md border border-input bg-background px-3 text-base text-foreground shadow-sm focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring";
const buttonClassName =
  "inline-flex min-h-11 items-center justify-center rounded-md bg-primary px-4 text-sm font-semibold text-primary-foreground hover:bg-primary/90 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring disabled:cursor-not-allowed disabled:opacity-60";
const textButtonClassName =
  "inline-flex min-h-11 items-center rounded-md px-3 text-sm font-semibold text-primary underline-offset-4 hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring";

function parseCoordinate(
  coordinate: CoordinateInput,
  label: string,
): { value: { latitude: number; longitude: number } } | { error: string } {
  if (!coordinate.latitude.trim() || !coordinate.longitude.trim()) {
    return { error: `Enter both latitude and longitude for ${label}.` };
  }
  const latitude = Number(coordinate.latitude);
  const longitude = Number(coordinate.longitude);
  if (!Number.isFinite(latitude) || latitude < -90 || latitude > 90) {
    return { error: `${label} latitude must be between -90 and 90.` };
  }
  if (!Number.isFinite(longitude) || longitude < -180 || longitude > 180) {
    return { error: `${label} longitude must be between -180 and 180.` };
  }
  return { value: { latitude, longitude } };
}

function parseRoute(
  values: RouteFormValues,
): { request: RouteRelevanceRequest } | { error: string } {
  const origin = parseCoordinate(values.origin, "the origin");
  if ("error" in origin) return origin;
  const destination = parseCoordinate(values.destination, "the destination");
  if ("error" in destination) return destination;
  const waypoints: RouteRelevanceRequest["waypoints"] = [];
  for (const [index, waypoint] of values.waypoints.entries()) {
    const parsed = parseCoordinate(waypoint, `waypoint ${index + 1}`);
    if ("error" in parsed) return parsed;
    waypoints.push(parsed.value);
  }
  return {
    request: {
      origin: origin.value,
      destination: destination.value,
      ...(waypoints.length > 0 ? { waypoints } : {}),
    },
  };
}

function retryAfterText(value: string | null): string {
  if (!value) return "Wait briefly before trying again.";
  const seconds = Number(value);
  if (Number.isFinite(seconds) && seconds >= 0) {
    return `Try again in about ${Math.ceil(seconds)} seconds.`;
  }
  const date = Date.parse(value);
  if (Number.isFinite(date)) {
    const secondsUntil = Math.max(0, Math.ceil((date - Date.now()) / 1_000));
    return secondsUntil === 0
      ? "You can try again now."
      : `Try again in about ${secondsUntil} seconds.`;
  }
  return "Wait briefly before trying again.";
}

function RoutePanel({
  title,
  children,
  role = "status",
}: {
  title: string;
  children: ReactNode;
  role?: "status" | "alert";
}) {
  const headingId = useId();
  return (
    <section
      aria-live={role === "alert" ? "assertive" : "polite"}
      aria-labelledby={headingId}
      className="rounded-xl border border-border bg-card p-4 sm:p-5"
      role={role}
    >
      <h2 className="text-base font-semibold tracking-tight" id={headingId}>
        {title}
      </h2>
      <div className="mt-1 space-y-2 text-sm leading-6 text-muted-foreground">
        {children}
      </div>
    </section>
  );
}

export function RouteRelevancePanel({ state }: { state: RouteRelevanceState }) {
  switch (state.status) {
    case "no-route":
      return (
        <RoutePanel title="No route checked">
          <p>Enter route coordinates, then check the route.</p>
        </RoutePanel>
      );
    case "loading":
      return (
        <RoutePanel title="Checking route" role="status">
          <p>Waiting for the route result.</p>
        </RoutePanel>
      );
    case "validation-error":
      return (
        <RoutePanel title="Check the route coordinates" role="alert">
          <p>{state.message}</p>
        </RoutePanel>
      );
    case "unauthorized":
      return (
        <RoutePanel title="Sign in to check this route" role="alert">
          <p>An active Signa session is required. Sign in, then try again.</p>
        </RoutePanel>
      );
    case "forbidden":
      return (
        <RoutePanel title="Route checks aren’t allowed here" role="alert">
          <p>This site isn’t authorized to request route checks.</p>
        </RoutePanel>
      );
    case "rate-limited":
      return (
        <RoutePanel title="Too many route checks" role="alert">
          <p>{retryAfterText(state.retryAfter)}</p>
          <button
            className={buttonClassName}
            onClick={state.onRetry}
            type="button"
          >
            Retry route check
          </button>
        </RoutePanel>
      );
    case "route-unavailable":
      return (
        <RoutePanel title="Route calculation is unavailable" role="alert">
          <p>Route impact is unknown. Try again later.</p>
          <button
            className={buttonClassName}
            onClick={state.onRetry}
            type="button"
          >
            Retry route check
          </button>
        </RoutePanel>
      );
    case "incident-data-unavailable":
      return (
        <RoutePanel title="Incident data is unavailable" role="alert">
          <p>
            Route impact is unknown. Missing data does not mean the route is
            safe.
          </p>
          <button
            className={buttonClassName}
            onClick={state.onRetry}
            type="button"
          >
            Retry route check
          </button>
        </RoutePanel>
      );
    case "service-error":
      return (
        <RoutePanel title="Signa couldn’t check this route" role="alert">
          <p>Route impact is unknown. Try again.</p>
          <button
            className={buttonClassName}
            onClick={state.onRetry}
            type="button"
          >
            Retry route check
          </button>
        </RoutePanel>
      );
    case "network-error":
      return (
        <RoutePanel title="Couldn’t reach Signa" role="alert">
          <p>Route impact is unknown. Check your connection, then try again.</p>
          <button
            className={buttonClassName}
            onClick={state.onRetry}
            type="button"
          >
            Retry route check
          </button>
        </RoutePanel>
      );
    case "unknown":
      return (
        <RoutePanel title="Route impact is unknown">
          <p>
            The backend could not classify route relevance. This is not a safety
            assessment.
          </p>
        </RoutePanel>
      );
    case "relevant":
      return (
        <RoutePanel title="A reported incident may affect this route">
          <p>
            The backend marked this route as relevant. Review incident details;
            this is not proof an incident is occurring.
          </p>
        </RoutePanel>
      );
    case "not-relevant":
      return (
        <RoutePanel title="No evaluated incident was route-relevant">
          <p>This result does not confirm that the route is safe.</p>
        </RoutePanel>
      );
  }
}

function CoordinateFields({
  id,
  title,
  value,
  onChange,
  onRemove,
}: {
  id: string;
  title: string;
  value: CoordinateInput;
  onChange: (field: keyof CoordinateInput, value: string) => void;
  onRemove?: () => void;
}) {
  return (
    <fieldset className="min-w-0 rounded-lg border border-border p-3 sm:p-4">
      <legend className="px-1 text-sm font-semibold">{title}</legend>
      {onRemove && (
        <button
          aria-label={`Remove ${title.toLocaleLowerCase("en")}`}
          className={textButtonClassName}
          onClick={onRemove}
          type="button"
        >
          Remove
        </button>
      )}
      <div className="grid min-w-0 grid-cols-1 gap-3 sm:grid-cols-2">
        {(["latitude", "longitude"] as const).map((field) => {
          const inputId = `${id}-${field}`;
          const long = field === "latitude" ? "Latitude" : "Longitude";
          const bounds = field === "latitude" ? "-90 to 90" : "-180 to 180";
          return (
            <label
              className="min-w-0 text-sm font-medium"
              htmlFor={inputId}
              key={field}
            >
              {long}{" "}
              <span className="font-normal text-muted-foreground">
                ({bounds})
              </span>
              <input
                autoComplete="off"
                className={inputClassName}
                id={inputId}
                inputMode="decimal"
                onChange={(event) => onChange(field, event.currentTarget.value)}
                step="any"
                type="number"
                value={value[field]}
              />
            </label>
          );
        })}
      </div>
    </fieldset>
  );
}

export function RouteRelevanceExperience({
  evaluate = requestRouteRelevance,
  onResultChange,
}: {
  evaluate?: RouteRelevanceEvaluator;
  onResultChange: (result: RouteMapResult | null) => void;
}) {
  const [values, setValues] = useState<RouteFormValues>({
    origin: { ...emptyCoordinate },
    destination: { ...emptyCoordinate },
    waypoints: [],
  });
  const [state, setState] = useState<RouteRelevanceState>({
    status: "no-route",
  });
  const requestControllerRef = useRef<AbortController | null>(null);
  const requestIdRef = useRef(0);
  const runRequestRef = useRef<(request: RouteRelevanceRequest) => void>(
    () => {},
  );
  const latestRequestRef = useRef<RouteRelevanceRequest | null>(null);

  useEffect(
    () => () => {
      requestIdRef.current += 1;
      requestControllerRef.current?.abort();
      requestControllerRef.current = null;
    },
    [],
  );

  const runRequest = useCallback(
    async (request: RouteRelevanceRequest) => {
      requestControllerRef.current?.abort();
      const controller = new AbortController();
      requestControllerRef.current = controller;
      const requestId = ++requestIdRef.current;
      latestRequestRef.current = request;
      onResultChange(null);
      setState({ status: "loading" });

      const retryLatest = () => {
        const latest = latestRequestRef.current;
        if (latest) runRequestRef.current(latest);
      };

      try {
        const result = await evaluate(request, controller.signal);
        if (controller.signal.aborted || requestId !== requestIdRef.current) {
          return;
        }
        setStateFromResult(result, retryLatest, setState, onResultChange);
      } catch {
        if (!controller.signal.aborted && requestId === requestIdRef.current) {
          setState({ status: "network-error", onRetry: retryLatest });
        }
      }
    },
    [evaluate, onResultChange],
  );

  useEffect(() => {
    runRequestRef.current = (request) => {
      void runRequest(request);
    };
  }, [runRequest]);

  const clearForEdit = () => {
    requestIdRef.current += 1;
    requestControllerRef.current?.abort();
    requestControllerRef.current = null;
    latestRequestRef.current = null;
    onResultChange(null);
    setState({ status: "no-route" });
  };

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const parsed = parseRoute(values);
    if ("error" in parsed) {
      clearForEdit();
      setState({ status: "validation-error", message: parsed.error });
      return;
    }
    void runRequest(parsed.request);
  };

  const updateCoordinate = (
    group: "origin" | "destination",
    field: keyof CoordinateInput,
    value: string,
  ) => {
    clearForEdit();
    setValues((current) => ({
      ...current,
      [group]: { ...current[group], [field]: value },
    }));
  };

  const updateWaypoint = (
    index: number,
    field: keyof CoordinateInput,
    value: string,
  ) => {
    clearForEdit();
    setValues((current) => ({
      ...current,
      waypoints: current.waypoints.map((waypoint, waypointIndex) =>
        waypointIndex === index ? { ...waypoint, [field]: value } : waypoint,
      ),
    }));
  };

  const formId = useId();
  return (
    <section className="space-y-3 rounded-xl border border-border bg-card p-4 sm:p-5">
      <div>
        <h2 className="text-lg font-semibold tracking-tight">Check a route</h2>
        <p className="mt-1 text-sm leading-6 text-muted-foreground">
          Enter coordinates. They are sent only when you check the route.
        </p>
      </div>
      <form
        aria-label="Check route relevance"
        className="space-y-3"
        noValidate
        onSubmit={handleSubmit}
      >
        <CoordinateFields
          id={`${formId}-origin`}
          onChange={(field, value) => updateCoordinate("origin", field, value)}
          title="Origin"
          value={values.origin}
        />
        <CoordinateFields
          id={`${formId}-destination`}
          onChange={(field, value) =>
            updateCoordinate("destination", field, value)
          }
          title="Destination"
          value={values.destination}
        />
        {values.waypoints.map((waypoint, index) => (
          <CoordinateFields
            id={`${formId}-waypoint-${index}`}
            key={index}
            onChange={(field, value) => updateWaypoint(index, field, value)}
            onRemove={() => {
              clearForEdit();
              setValues((current) => ({
                ...current,
                waypoints: current.waypoints.filter((_, i) => i !== index),
              }));
            }}
            title={`Waypoint ${index + 1}`}
            value={waypoint}
          />
        ))}
        <div className="flex flex-wrap items-center gap-2">
          <button className={buttonClassName} type="submit">
            Check route
          </button>
          <button
            className={textButtonClassName}
            disabled={values.waypoints.length >= MAX_WAYPOINTS}
            onClick={() => {
              clearForEdit();
              setValues((current) => ({
                ...current,
                waypoints: [...current.waypoints, { ...emptyCoordinate }],
              }));
            }}
            type="button"
          >
            Add waypoint
          </button>
        </div>
      </form>
      <RouteRelevancePanel state={state} />
    </section>
  );
}

function setStateFromResult(
  result: RouteRelevanceResult,
  retry: () => void,
  setState: (state: RouteRelevanceState) => void,
  onResultChange: (result: RouteMapResult | null) => void,
) {
  switch (result.status) {
    case "aborted":
      return;
    case "validation":
      onResultChange(null);
      setState({
        status: "validation-error",
        message:
          "The API did not accept these coordinates. Review them and try again.",
      });
      return;
    case "unauthorized":
      onResultChange(null);
      setState({ status: "unauthorized" });
      return;
    case "forbidden":
      onResultChange(null);
      setState({ status: "forbidden" });
      return;
    case "rate-limited":
      onResultChange(null);
      setState({
        status: "rate-limited",
        retryAfter: result.retryAfter,
        onRetry: retry,
      });
      return;
    case "route-unavailable":
      onResultChange(null);
      setState({ status: "route-unavailable", onRetry: retry });
      return;
    case "incident-data-unavailable":
      onResultChange(null);
      setState({ status: "incident-data-unavailable", onRetry: retry });
      return;
    case "service-error":
      onResultChange(null);
      setState({ status: "service-error", onRetry: retry });
      return;
    case "network-error":
      onResultChange(null);
      setState({ status: "network-error", onRetry: retry });
      return;
    case "success": {
      const geometry = toRouteFeatureCollection(result.response.route_geometry)
        .features[0]?.geometry;
      if (!geometry) {
        onResultChange(null);
        setState({ status: "service-error", onRetry: retry });
        return;
      }
      onResultChange({ geometry, incidents: result.response.incidents });
      setState(
        result.response.classification === "RELEVANT"
          ? { status: "relevant" }
          : result.response.classification === "NOT_RELEVANT"
            ? { status: "not-relevant" }
            : { status: "unknown" },
      );
      return;
    }
  }
}
