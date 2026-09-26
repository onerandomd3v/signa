import Link from "next/link";
import type { ReactNode } from "react";
import type { RealtimeConnectionStatus } from "./use-realtime-updates";

/**
 * A deliberately small presentation projection—not an API contract. Keep it
 * limited to generated public API fields and omit private or exact location data.
 */
export type IncidentUpdateView = {
  id: string;
  occurredAt: string;
  status?: string | null;
  confidenceState?: string | null;
  severity?: string | null;
};

export type IncidentView = {
  id: string;
  eventType: string | null;
  confidenceState: string | null;
  severity: string | null;
  status: string;
  approximateArea: string | null;
  lastSignalAt: string | null;
  updates: IncidentUpdateView[] | null;
};

export type IncidentFeedState =
  | { status: "loading" }
  | { status: "error" }
  | { status: "unavailable" }
  | { status: "ready"; incidents: IncidentView[] };

export function RealtimeConnectionStatusMessage({
  status,
  alertUpdateReceived,
  onRetry,
}: {
  status: RealtimeConnectionStatus | "degraded";
  alertUpdateReceived: boolean;
  onRetry?: () => void;
}) {
  const messages: Record<RealtimeConnectionStatus | "degraded", string> = {
    connecting: "Connecting to incident updates…",
    live: "Connected to realtime updates.",
    reconnecting: "Realtime connection interrupted. Reconnecting…",
    unavailable:
      "Realtime updates unavailable. Incident information remains available.",
    degraded:
      "Realtime updates are degraded. Couldn’t refresh incidents; displayed information may be out of date.",
  };
  const isDegraded = status === "degraded";

  return (
    <div className="space-y-1">
      <p
        aria-atomic="true"
        aria-live="polite"
        className="text-xs text-muted-foreground"
        role="status"
      >
        {messages[status]}
      </p>
      {isDegraded && onRetry && (
        <button
          className="inline-flex min-h-11 items-center text-sm font-semibold text-primary underline-offset-4 hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
          onClick={onRetry}
          type="button"
        >
          Retry incident refresh
        </button>
      )}
      {alertUpdateReceived && (
        <p className="text-xs text-muted-foreground">
          An alert update was received.
        </p>
      )}
    </div>
  );
}

const contentWidth = "mx-auto w-full max-w-3xl px-4 sm:px-6";
const terminalStatuses = new Set(["resolved", "expired"]);
const relativeTimeFormatter = new Intl.RelativeTimeFormat("en", {
  numeric: "auto",
});
const updateDateFormatter = new Intl.DateTimeFormat("en", {
  dateStyle: "medium",
  timeStyle: "short",
});

function displayValue(value: string | null | undefined): string {
  if (!value?.trim()) return "Not provided";
  return value
    .trim()
    .replace(/[_-]+/g, " ")
    .toLocaleLowerCase("en")
    .replace(/\b[a-z]/g, (letter) => letter.toLocaleUpperCase("en"));
}

function relativeTime(value: string | null): string {
  if (!value) return "Signal time unavailable";
  const timestamp = Date.parse(value);
  if (!Number.isFinite(timestamp)) return "Signal time unavailable";

  const seconds = Math.round((timestamp - Date.now()) / 1_000);
  const absoluteSeconds = Math.abs(seconds);
  const [amount, unit] =
    absoluteSeconds < 60
      ? [seconds, "second"]
      : absoluteSeconds < 3_600
        ? [Math.round(seconds / 60), "minute"]
        : absoluteSeconds < 86_400
          ? [Math.round(seconds / 3_600), "hour"]
          : [Math.round(seconds / 86_400), "day"];

  return relativeTimeFormatter.format(
    amount,
    unit as Intl.RelativeTimeFormatUnit,
  );
}

function updateSummary(update: IncidentUpdateView): string {
  const changed = [
    update.status ? `Status: ${displayValue(update.status)}` : null,
    update.confidenceState
      ? `Confidence: ${displayValue(update.confidenceState)}`
      : null,
    update.severity ? `Severity: ${displayValue(update.severity)}` : null,
  ].filter((value): value is string => value !== null);

  return changed.length > 0 ? changed.join(" · ") : "System update";
}

function StatusPill({ status }: { status: string }) {
  const label = displayValue(status);
  const terminal = terminalStatuses.has(label.toLowerCase());

  return (
    <span
      className={`inline-flex min-h-7 items-center rounded-full border px-2.5 py-1 text-xs font-semibold capitalize ${terminal ? "border-border bg-muted text-muted-foreground" : "border-primary/25 bg-primary/5 text-primary"}`}
    >
      {label}
    </span>
  );
}

function Freshness({ lastSignalAt }: { lastSignalAt: string | null }) {
  const label = relativeTime(lastSignalAt);

  return (
    <p className="text-sm leading-6 text-muted-foreground">
      Last signal{" "}
      {lastSignalAt && Number.isFinite(Date.parse(lastSignalAt)) ? (
        <time dateTime={lastSignalAt}>{label}</time>
      ) : (
        <span>{label}</span>
      )}
    </p>
  );
}

function IncidentCard({ incident }: { incident: IncidentView }) {
  return (
    <article className="rounded-xl border border-border bg-card p-4 shadow-sm sm:p-5">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <h2 className="text-lg leading-6 font-semibold tracking-tight sm:text-xl">
            {displayValue(incident.eventType)}
          </h2>
          <p className="mt-1 text-sm leading-6 text-muted-foreground">
            {displayValue(incident.approximateArea)}
          </p>
        </div>
        <StatusPill status={incident.status} />
      </div>

      <dl className="mt-4 grid grid-cols-2 gap-x-4 gap-y-3 border-t border-border pt-4 sm:grid-cols-3">
        <div>
          <dt className="text-xs font-medium text-muted-foreground">
            Confidence
          </dt>
          <dd className="mt-1 text-sm font-medium capitalize">
            {displayValue(incident.confidenceState)}
          </dd>
        </div>
        <div>
          <dt className="text-xs font-medium text-muted-foreground">
            Severity
          </dt>
          <dd className="mt-1 text-sm font-medium capitalize">
            {displayValue(incident.severity)}
          </dd>
        </div>
        <div className="col-span-2 sm:col-span-1">
          <dt className="text-xs font-medium text-muted-foreground">
            Freshness
          </dt>
          <dd className="mt-1">
            <Freshness lastSignalAt={incident.lastSignalAt} />
          </dd>
        </div>
      </dl>

      <Link
        className="mt-4 inline-flex min-h-11 items-center rounded-md text-sm font-semibold text-primary underline-offset-4 hover:underline focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-ring"
        href={`/incidents/${encodeURIComponent(incident.id)}`}
      >
        View incident details{" "}
        <span aria-hidden="true" className="ml-1">
          →
        </span>
      </Link>
    </article>
  );
}

function StatePanel({
  title,
  children,
  role = "status",
}: {
  title: string;
  children: ReactNode;
  role?: "status" | "alert";
}) {
  return (
    <section
      aria-live={role === "alert" ? "assertive" : "polite"}
      className="rounded-xl border border-border bg-card p-5 sm:p-7"
      role={role}
    >
      <h2 className="text-lg font-semibold tracking-tight">{title}</h2>
      <div className="mt-2 text-sm leading-6 text-muted-foreground">
        {children}
      </div>
    </section>
  );
}

export function IncidentFeed({ state }: { state: IncidentFeedState }) {
  if (state.status === "loading") {
    return (
      <div className="space-y-3" aria-label="Loading incidents" role="status">
        <div className="h-36 animate-pulse rounded-xl border border-border bg-card motion-reduce:animate-none" />
        <div className="h-36 animate-pulse rounded-xl border border-border bg-card motion-reduce:animate-none" />
      </div>
    );
  }

  if (state.status === "error") {
    return (
      <StatePanel title="Couldn’t load incidents" role="alert">
        <p>Try again in a moment. No incident information is available.</p>
      </StatePanel>
    );
  }

  if (state.status === "unavailable") {
    return (
      <StatePanel title="Incident updates aren’t available yet">
        <p>
          This feed will show incidents when a public incident service is
          available. No sample incidents are shown as live reports.
        </p>
      </StatePanel>
    );
  }

  if (state.incidents.length === 0) {
    return (
      <StatePanel title="No incidents to show right now">
        <p>
          This does not mean an area is safe. Check local sources and use your
          own judgment.
        </p>
      </StatePanel>
    );
  }

  return (
    <ul className="space-y-3" aria-label="Incidents">
      {state.incidents.map((incident) => (
        <li key={incident.id}>
          <IncidentCard incident={incident} />
        </li>
      ))}
    </ul>
  );
}

export type IncidentDetailState =
  | { status: "loading" }
  | { status: "error"; onRetry?: () => void }
  | { status: "not-found" }
  | { status: "unavailable" }
  | { status: "ready"; incident: IncidentView };

export function IncidentDetail({ state }: { state: IncidentDetailState }) {
  if (state.status === "loading") {
    return (
      <div
        aria-label="Loading incident details"
        className="space-y-3"
        role="status"
      >
        <div className="h-48 animate-pulse rounded-xl border border-border bg-card motion-reduce:animate-none" />
        <div className="h-32 animate-pulse rounded-xl border border-border bg-card motion-reduce:animate-none" />
      </div>
    );
  }

  if (state.status === "error") {
    return (
      <StatePanel title="Couldn’t load incident details" role="alert">
        <p>Try again in a moment. No incident information is available.</p>
        {state.onRetry && (
          <button
            className="mt-4 inline-flex min-h-11 items-center rounded-md bg-primary px-4 text-sm font-semibold text-primary-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
            onClick={state.onRetry}
            type="button"
          >
            Retry
          </button>
        )}
      </StatePanel>
    );
  }

  if (state.status === "not-found") {
    return (
      <StatePanel title="Incident not found">
        <p>
          This incident may no longer be active, or the link may be incorrect.
        </p>
      </StatePanel>
    );
  }

  if (state.status === "unavailable") {
    return (
      <StatePanel title="Incident details aren’t available yet">
        <p>
          Public incident details aren’t connected yet. We don’t have verified
          information to show for this link.
        </p>
      </StatePanel>
    );
  }

  const { incident } = state;
  const terminalStatus = displayValue(incident.status).toLowerCase();

  return (
    <article className="space-y-5">
      <header className="rounded-xl border border-border bg-card p-5 shadow-sm sm:p-7">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <p className="text-sm font-medium text-muted-foreground">
            Incident status
          </p>
          <StatusPill status={incident.status} />
        </div>
        {terminalStatus === "resolved" && (
          <p className="mt-4 rounded-lg bg-muted px-3 py-2 text-sm leading-6 text-foreground">
            This incident is marked resolved by the incident system.
          </p>
        )}
        {terminalStatus === "expired" && (
          <p className="mt-4 rounded-lg bg-muted px-3 py-2 text-sm leading-6 text-foreground">
            This incident has expired and may no longer reflect current
            conditions.
          </p>
        )}
        <h1 className="mt-5 text-2xl leading-tight font-semibold tracking-tight sm:text-3xl">
          {displayValue(incident.eventType)}
        </h1>
        {incident.approximateArea && (
          <p className="mt-2 text-base leading-7 text-muted-foreground">
            {displayValue(incident.approximateArea)}
          </p>
        )}
        <dl className="mt-6 grid grid-cols-2 gap-4 border-t border-border pt-5 sm:grid-cols-3">
          <div>
            <dt className="text-xs font-medium text-muted-foreground">
              Confidence state
            </dt>
            <dd className="mt-1 text-sm font-semibold capitalize">
              {displayValue(incident.confidenceState)}
            </dd>
          </div>
          <div>
            <dt className="text-xs font-medium text-muted-foreground">
              Severity
            </dt>
            <dd className="mt-1 text-sm font-semibold capitalize">
              {displayValue(incident.severity)}
            </dd>
          </div>
          <div className="col-span-2 sm:col-span-1">
            <dt className="text-xs font-medium text-muted-foreground">
              Freshness
            </dt>
            <dd className="mt-1">
              <Freshness lastSignalAt={incident.lastSignalAt} />
            </dd>
          </div>
        </dl>
        <p className="mt-5 border-t border-border pt-4 text-sm leading-6 text-muted-foreground">
          Confidence and severity are separate system assessments. Confidence
          can change as information arrives; it is not proof.
        </p>
      </header>

      <section
        aria-labelledby="incident-updates-heading"
        className="rounded-xl border border-border bg-card p-5 sm:p-7"
      >
        <h2
          className="text-lg font-semibold tracking-tight"
          id="incident-updates-heading"
        >
          Incident updates
        </h2>
        {incident.updates === null ? (
          <p className="mt-3 text-sm leading-6 text-muted-foreground">
            Update history isn’t included in the public incident response.
          </p>
        ) : incident.updates.length === 0 ? (
          <p className="mt-3 text-sm leading-6 text-muted-foreground">
            No updates are available.
          </p>
        ) : (
          <ol className="mt-4 space-y-0">
            {incident.updates.map((update) => (
              <li
                className="relative border-l border-border pb-5 pl-5 last:border-l-transparent last:pb-0"
                key={update.id}
              >
                <span
                  aria-hidden="true"
                  className="absolute top-1 left-[-0.3rem] size-2.5 rounded-full border-2 border-primary bg-background"
                />
                <p className="text-sm leading-6 font-medium">
                  {updateSummary(update)}
                </p>
                <p className="mt-1 text-xs leading-5 text-muted-foreground">
                  {Number.isFinite(Date.parse(update.occurredAt)) ? (
                    <time dateTime={update.occurredAt}>
                      {updateDateFormatter.format(new Date(update.occurredAt))}
                    </time>
                  ) : (
                    "Update time unavailable"
                  )}
                </p>
              </li>
            ))}
          </ol>
        )}
      </section>
    </article>
  );
}

export function IncidentPageFrame({ children }: { children: ReactNode }) {
  return (
    <div className={`${contentWidth} flex-1 py-8 sm:py-12`}>{children}</div>
  );
}
