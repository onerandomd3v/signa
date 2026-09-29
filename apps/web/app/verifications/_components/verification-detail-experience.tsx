"use client";

import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import { Button } from "../../../components/ui/button";
import type {
  VerificationRequestView,
  VerificationResponseInput,
} from "../../../lib/api/generated";
import {
  fetchVerificationRequest,
  sendVerificationResponse,
} from "../../../lib/api/verifications";
import { VerificationResponseForm } from "./verification-response-form";
import { VerificationStatePanel } from "./verification-state-panel";

const safetyPrompt =
  "Respond only from what you already safely know or observed. Never approach an incident or unsafe area to verify it.";

type DetailState =
  | { status: "loading" }
  | { status: "ready"; request: VerificationRequestView }
  | { status: "unauthorized" }
  | { status: "forbidden" }
  | { status: "not-found" }
  | { status: "unavailable" };

function displayValue(value: string | null | undefined): string {
  if (!value?.trim()) return "Incident update";
  return value
    .trim()
    .replace(/[_-]+/g, " ")
    .toLocaleLowerCase("en")
    .replace(/\b[a-z]/g, (letter) => letter.toLocaleUpperCase("en"));
}

function dateLabel(value: string): string {
  const date = new Date(value);
  return Number.isNaN(date.getTime())
    ? "Time unavailable"
    : new Intl.DateTimeFormat("en", {
        dateStyle: "medium",
        timeStyle: "short",
      }).format(date);
}

export function VerificationDetailExperience({
  requestId,
}: {
  requestId: string;
}) {
  const [state, setState] = useState<DetailState>({ status: "loading" });
  const [reload, setReload] = useState(0);

  const load = useCallback(
    async (signal: AbortSignal) => {
      if (signal.aborted) return;
      const result = await fetchVerificationRequest(requestId, signal);
      if (signal.aborted) return;
      if (!result.ok) {
        const status = result.status;
        setState(
          status === 401
            ? { status: "unauthorized" }
            : status === 403
              ? { status: "forbidden" }
              : status === 404
                ? { status: "not-found" }
                : { status: "unavailable" },
        );
        return;
      }
      if (result.data.safety_prompt !== safetyPrompt) {
        setState({ status: "unavailable" });
        return;
      }
      setState({ status: "ready", request: result.data });
    },
    [requestId],
  );

  useEffect(() => {
    const controller = new AbortController();
    void Promise.resolve().then(() => load(controller.signal));
    return () => controller.abort();
  }, [load, reload]);

  if (state.status === "loading") {
    return (
      <div
        aria-label="Loading verification request"
        className="space-y-3"
        role="status"
      >
        <div className="h-40 animate-pulse rounded-xl border border-border bg-card motion-reduce:animate-none" />
        <div className="h-56 animate-pulse rounded-xl border border-border bg-card motion-reduce:animate-none" />
      </div>
    );
  }

  if (state.status === "unauthorized") {
    return (
      <VerificationStatePanel title="Sign in required" role="alert">
        <p>Sign in to view this verification request.</p>
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
  if (state.status === "not-found") {
    return (
      <VerificationStatePanel title="Request no longer available" role="alert">
        <p>This verification request is no longer available.</p>
        <Link
          className="mt-3 inline-flex min-h-11 items-center font-semibold text-primary underline-offset-4 hover:underline focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-ring"
          href="/verifications"
        >
          Return to inbox
        </Link>
      </VerificationStatePanel>
    );
  }
  if (state.status === "unavailable") {
    return (
      <VerificationStatePanel title="Request unavailable" role="alert">
        <p>Couldn’t load this request. Try again.</p>
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

  function handleProtectedError(status: number) {
    if (status === 401) setState({ status: "unauthorized" });
    else if (status === 403) setState({ status: "forbidden" });
    else setState({ status: "not-found" });
  }

  const incident = state.request.incident;
  return (
    <div className="space-y-5">
      <header>
        <p className="text-sm font-semibold text-primary">
          Verification request
        </p>
        <h1 className="mt-2 text-3xl leading-tight font-semibold tracking-[-0.04em] sm:text-4xl">
          {displayValue(incident.event_type)}
        </h1>
        <p className="mt-2 text-sm leading-6 text-muted-foreground">
          {displayValue(incident.status)} incident
        </p>
      </header>

      <section
        aria-labelledby="verification-safety-title"
        className="rounded-xl border border-primary/30 bg-secondary p-4 sm:p-5"
      >
        <h2 className="text-base font-semibold" id="verification-safety-title">
          Safety first
        </h2>
        <p className="mt-2 text-sm leading-6 text-foreground">{safetyPrompt}</p>
      </section>

      <section
        aria-label="Public incident details"
        className="rounded-xl border border-border bg-card p-4 shadow-sm sm:p-5"
      >
        <h2 className="text-base font-semibold">Incident details</h2>
        <dl className="mt-3 grid grid-cols-2 gap-4 text-sm">
          <div>
            <dt className="font-medium text-muted-foreground">Confidence</dt>
            <dd className="mt-1">{displayValue(incident.confidence_state)}</dd>
          </div>
          {incident.severity && (
            <div>
              <dt className="font-medium text-muted-foreground">Severity</dt>
              <dd className="mt-1">{displayValue(incident.severity)}</dd>
            </div>
          )}
        </dl>
        <p className="mt-4 border-t border-border pt-3 text-sm leading-6 text-muted-foreground">
          Requested{" "}
          <time dateTime={state.request.created_at}>
            {dateLabel(state.request.created_at)}
          </time>
          <span aria-hidden="true"> · </span>
          Expires{" "}
          <time dateTime={state.request.expires_at}>
            {dateLabel(state.request.expires_at)}
          </time>
        </p>
      </section>

      <section
        aria-labelledby="response-title"
        className="rounded-xl border border-border bg-card p-4 shadow-sm sm:p-5"
      >
        <h2 className="text-lg font-semibold" id="response-title">
          Your response
        </h2>
        <div className="mt-4">
          <VerificationResponseForm
            onProtectedError={handleProtectedError}
            requestId={requestId}
            submitResponse={(body: VerificationResponseInput, key: string) =>
              sendVerificationResponse(requestId, body, key)
            }
          />
        </div>
      </section>
    </div>
  );
}
