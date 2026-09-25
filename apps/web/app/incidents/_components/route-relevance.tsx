import { useId, type ReactNode } from "react";

/**
 * UI presentation state only. This is not a wire contract; an approved API
 * adapter must map its generated response into these states.
 */
export type RouteRelevanceState =
  | { status: "no-route" }
  | { status: "loading" }
  | { status: "unavailable" }
  | { status: "unknown" }
  | { status: "relevant" }
  | { status: "not-relevant" }
  | { status: "incident-data-unavailable" }
  | { status: "error"; onRetry?: () => void };

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
      <div className="mt-1 text-sm leading-6 text-muted-foreground">
        {children}
      </div>
    </section>
  );
}

export function RouteRelevancePanel({ state }: { state: RouteRelevanceState }) {
  switch (state.status) {
    case "no-route":
      return (
        <RoutePanel title="No route selected">
          <p>Select a route before checking for incident relevance.</p>
        </RoutePanel>
      );
    case "loading":
      return (
        <RoutePanel title="Checking route relevance">
          <p>Waiting for a route relevance result.</p>
        </RoutePanel>
      );
    case "unavailable":
      return (
        <RoutePanel title="Route checks aren’t available yet">
          <p>
            Signa has no approved browser route service yet. Route impact is
            unknown; this does not mean a route is safe.
          </p>
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
            The backend classified an incident as route-relevant. Review its
            details; this classification is not proof the incident is occurring.
          </p>
        </RoutePanel>
      );
    case "not-relevant":
      return (
        <RoutePanel title="No evaluated incident was route-relevant">
          <p>This result does not confirm that the route is safe.</p>
        </RoutePanel>
      );
    case "incident-data-unavailable":
      return (
        <RoutePanel title="Incident data is unavailable">
          <p>
            Route impact cannot be determined without incident data. Missing
            data does not mean the route is safe.
          </p>
        </RoutePanel>
      );
    case "error":
      return (
        <RoutePanel title="Couldn’t check route relevance" role="alert">
          <p>Route impact is unknown. Try again.</p>
          {state.onRetry && (
            <button
              className="mt-3 inline-flex min-h-11 items-center rounded-md bg-primary px-4 text-sm font-semibold text-primary-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
              onClick={state.onRetry}
              type="button"
            >
              Retry route check
            </button>
          )}
        </RoutePanel>
      );
  }
}
