"use client";

import Link from "next/link";
import type { AlertSnapshotEntry } from "./use-alert-reconciliation";

const asOfFormatter = new Intl.DateTimeFormat("en", {
  dateStyle: "medium",
  timeStyle: "short",
});

function label(value: string): string {
  return value
    .replace(/[_-]+/g, " ")
    .toLocaleLowerCase("en")
    .replace(/\b[a-z]/g, (letter) => letter.toLocaleUpperCase("en"));
}

export function RealtimeAlertSnapshots({
  alerts,
  hasOverflow = false,
  onRetry,
  incidentId,
}: {
  alerts: AlertSnapshotEntry[];
  hasOverflow?: boolean;
  onRetry: (alertId: string) => void;
  incidentId?: string;
}) {
  const visibleAlerts = alerts.filter(
    (entry) =>
      entry.status === "unavailable" ||
      !incidentId ||
      entry.alert.incident_id === incidentId,
  );
  if (visibleAlerts.length === 0 && !hasOverflow) return null;

  return (
    <section
      aria-labelledby="realtime-alert-snapshots-heading"
      className="space-y-3"
    >
      <h2
        className="text-base font-semibold"
        id="realtime-alert-snapshots-heading"
      >
        Alert snapshots
      </h2>
      {hasOverflow && (
        <p
          aria-live="polite"
          className="rounded-lg border border-border bg-muted p-3 text-sm leading-6"
          role="status"
        >
          Alert updates arrived faster than this view could load them. Some
          snapshots may be missing; there is no alert history endpoint.
        </p>
      )}
      <ul className="space-y-3">
        {visibleAlerts.map((entry) => (
          <li key={entry.alertId}>
            {entry.status === "unavailable" ? (
              <article className="rounded-xl border border-border bg-card p-4">
                <p className="text-sm leading-6 text-muted-foreground">
                  Alert details are unavailable right now. Incident browsing is
                  unaffected.
                </p>
                <button
                  className="mt-2 inline-flex min-h-11 items-center text-sm font-semibold text-primary underline-offset-4 hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
                  onClick={() => onRetry(entry.alertId)}
                  type="button"
                >
                  Retry alert details
                </button>
              </article>
            ) : (
              <article className="rounded-xl border border-border bg-card p-4">
                <div className="flex flex-wrap items-start justify-between gap-3">
                  <h3 className="min-w-0 text-base font-semibold">
                    {label(entry.alert.alert_type)} alert snapshot
                  </h3>
                  <span className="rounded-full border border-border px-2.5 py-1 text-xs font-semibold">
                    {entry.alert.freshness_snapshot === "STALE" ||
                    entry.alert.supersedes_alert_id !== null ||
                    entry.alert.status_snapshot !== "OPEN"
                      ? "Historical snapshot"
                      : "As-of snapshot"}
                  </span>
                </div>
                <p className="mt-2 text-sm leading-6">{entry.alert.message}</p>
                <p className="mt-2 text-xs leading-5 text-muted-foreground">
                  This immutable snapshot does not confirm current incident
                  conditions.
                </p>
                {entry.stale && (
                  <div className="mt-3 rounded-md bg-muted p-3">
                    <p className="text-sm leading-5">
                      Couldn’t refresh this authorized snapshot. It may be out
                      of date.
                    </p>
                    <button
                      className="mt-1 inline-flex min-h-11 items-center text-sm font-semibold text-primary underline-offset-4 hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
                      onClick={() => onRetry(entry.alertId)}
                      type="button"
                    >
                      Retry alert details
                    </button>
                  </div>
                )}
                <dl className="mt-3 grid grid-cols-2 gap-x-4 gap-y-2 border-t border-border pt-3 text-sm sm:grid-cols-3">
                  <div>
                    <dt className="text-xs font-medium text-muted-foreground">
                      Priority snapshot
                    </dt>
                    <dd className="mt-1 font-medium">
                      {entry.alert.priority_snapshot}
                    </dd>
                  </div>
                  <div>
                    <dt className="text-xs font-medium text-muted-foreground">
                      Confidence snapshot
                    </dt>
                    <dd className="mt-1 font-medium">
                      {label(entry.alert.confidence_snapshot)}
                    </dd>
                  </div>
                  <div>
                    <dt className="text-xs font-medium text-muted-foreground">
                      Severity snapshot
                    </dt>
                    <dd className="mt-1 font-medium">
                      {label(entry.alert.severity_snapshot)}
                    </dd>
                  </div>
                  <div>
                    <dt className="text-xs font-medium text-muted-foreground">
                      Status snapshot
                    </dt>
                    <dd className="mt-1 font-medium">
                      {label(entry.alert.status_snapshot)}
                    </dd>
                  </div>
                  <div>
                    <dt className="text-xs font-medium text-muted-foreground">
                      Freshness snapshot
                    </dt>
                    <dd className="mt-1 font-medium">
                      {label(entry.alert.freshness_snapshot)}
                    </dd>
                  </div>
                  <div>
                    <dt className="text-xs font-medium text-muted-foreground">
                      As of
                    </dt>
                    <dd className="mt-1 font-medium">
                      <time dateTime={entry.alert.as_of}>
                        {asOfFormatter.format(new Date(entry.alert.as_of))}
                      </time>
                    </dd>
                  </div>
                </dl>
                <Link
                  className="mt-3 inline-flex min-h-11 items-center text-sm font-semibold text-primary underline-offset-4 hover:underline focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-ring"
                  href={`/incidents/${encodeURIComponent(entry.alert.incident_id)}`}
                >
                  View related incident
                </Link>
              </article>
            )}
          </li>
        ))}
      </ul>
    </section>
  );
}
