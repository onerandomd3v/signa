"use client";

import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import { Button } from "../../../components/ui/button";
import type { VerificationRequestView } from "../../../lib/api/generated";
import { fetchVerificationRequests } from "../../../lib/api/verifications";
import { VerificationStatePanel } from "./verification-state-panel";

type InboxState =
  | { status: "loading" }
  | { status: "ready"; requests: VerificationRequestView[] }
  | { status: "unauthorized" }
  | { status: "forbidden" }
  | { status: "unavailable" };

function dateLabel(value: string): string {
  const date = new Date(value);
  return Number.isNaN(date.getTime())
    ? "Time unavailable"
    : new Intl.DateTimeFormat("en", {
        dateStyle: "medium",
        timeStyle: "short",
      }).format(date);
}

function displayValue(value: string | null | undefined): string {
  if (!value?.trim()) return "Incident update";
  return value
    .trim()
    .replace(/[_-]+/g, " ")
    .toLocaleLowerCase("en")
    .replace(/\b[a-z]/g, (letter) => letter.toLocaleUpperCase("en"));
}

function fromStatus(status: number): InboxState {
  if (status === 401) return { status: "unauthorized" };
  if (status === 403) return { status: "forbidden" };
  return { status: "unavailable" };
}

export function VerificationInbox() {
  const [state, setState] = useState<InboxState>({ status: "loading" });
  const [reload, setReload] = useState(0);

  const load = useCallback(async (signal: AbortSignal) => {
    if (signal.aborted) return;
    const result = await fetchVerificationRequests(signal);
    if (signal.aborted) return;
    setState(
      result.ok
        ? { status: "ready", requests: result.data }
        : fromStatus(result.status),
    );
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    void Promise.resolve().then(() => load(controller.signal));
    return () => controller.abort();
  }, [load, reload]);

  if (state.status === "loading") {
    return (
      <div
        aria-label="Loading verification requests"
        className="space-y-3"
        role="status"
      >
        <div className="h-32 animate-pulse rounded-xl border border-border bg-card motion-reduce:animate-none" />
        <div className="h-32 animate-pulse rounded-xl border border-border bg-card motion-reduce:animate-none" />
      </div>
    );
  }

  if (state.status === "unauthorized") {
    return (
      <VerificationStatePanel title="Sign in required" role="alert">
        <p>Sign in to view verification requests.</p>
      </VerificationStatePanel>
    );
  }

  if (state.status === "forbidden") {
    return (
      <VerificationStatePanel title="Verifier access required" role="alert">
        <p>This account cannot access verification requests.</p>
      </VerificationStatePanel>
    );
  }

  if (state.status === "unavailable") {
    return (
      <VerificationStatePanel title="Inbox unavailable" role="alert">
        <p>Couldn’t load requests. Try again.</p>
        <Button
          className="mt-4 min-h-11 px-4"
          onClick={() => {
            setState({ status: "loading" });
            setReload((current) => current + 1);
          }}
          type="button"
          variant="outline"
        >
          Retry
        </Button>
      </VerificationStatePanel>
    );
  }

  if (state.requests.length === 0) {
    return (
      <VerificationStatePanel title="No active requests">
        <p>There are no verification requests to review right now.</p>
      </VerificationStatePanel>
    );
  }

  return (
    <ul aria-label="Active verification requests" className="space-y-3">
      {state.requests.map((request) => {
        const eventLabel = displayValue(request.incident.event_type);
        return (
          <li key={request.id}>
            <article className="rounded-xl border border-border bg-card p-4 shadow-sm sm:p-5">
              <div className="flex flex-wrap items-start justify-between gap-3">
                <div className="min-w-0">
                  <h2 className="text-lg leading-6 font-semibold tracking-tight sm:text-xl">
                    {eventLabel}
                  </h2>
                  <p className="mt-1 text-sm leading-6 text-muted-foreground">
                    {displayValue(request.incident.status)}
                  </p>
                </div>
                {request.incident.severity && (
                  <span className="inline-flex min-h-7 items-center rounded-full border border-border bg-muted px-2.5 py-1 text-xs font-semibold text-foreground">
                    {displayValue(request.incident.severity)} severity
                  </span>
                )}
              </div>
              <dl className="mt-4 grid grid-cols-1 gap-2 border-t border-border pt-4 text-sm sm:grid-cols-2">
                <div>
                  <dt className="font-medium text-muted-foreground">
                    Requested
                  </dt>
                  <dd className="mt-1">
                    <time dateTime={request.created_at}>
                      {dateLabel(request.created_at)}
                    </time>
                  </dd>
                </div>
                <div>
                  <dt className="font-medium text-muted-foreground">Expires</dt>
                  <dd className="mt-1">
                    <time dateTime={request.expires_at}>
                      {dateLabel(request.expires_at)}
                    </time>
                  </dd>
                </div>
              </dl>
              <Link
                aria-label={`Review request: ${eventLabel}`}
                className="mt-4 inline-flex min-h-11 items-center rounded-md font-semibold text-primary underline-offset-4 hover:underline focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-ring"
                href={`/verifications/${encodeURIComponent(request.id)}`}
              >
                Review request{" "}
                <span aria-hidden="true" className="ml-1">
                  →
                </span>
              </Link>
            </article>
          </li>
        );
      })}
    </ul>
  );
}
