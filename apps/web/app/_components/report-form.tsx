"use client";

import { useRef, useState, type FormEvent } from "react";
import { Button } from "../../components/ui/button";
import { Textarea } from "../../components/ui/textarea";
import type {
  CreateReportRequest,
  DeviceLocation,
  ReportAcknowledgement,
} from "../../lib/api/generated";
import {
  createIdempotencyKey,
  SecureKeyUnavailableError,
} from "../../lib/api/idempotency";
import {
  ApiConfigurationError,
  submitReport as submitReportToApi,
  type ReportSubmissionResult,
} from "../../lib/api/reports";
import {
  DeviceLocationError,
  requestDeviceLocation,
  type LocationFailure,
} from "./device-location";

type ReportFormProps = {
  submitReport?: (
    request: CreateReportRequest,
    idempotencyKey: string,
  ) => Promise<ReportSubmissionResult>;
  requestLocation?: () => Promise<DeviceLocation>;
};

type SubmissionState = "idle" | "submitting" | "success" | "error";
type LocationState = "idle" | "requesting" | "ready" | LocationFailure;

const locationMessages: Record<LocationFailure, string> = {
  denied: "Permission denied. You can continue without location.",
  unavailable: "Location unavailable. You can continue without it.",
  timeout: "Location request timed out. Try again or continue without it.",
  unsupported:
    "Location isn’t available in this browser. You can continue without it.",
  "low-accuracy":
    "Location was too imprecise. Try again or continue without it.",
  unknown: "Couldn’t get location. Try again or continue without it.",
};

function retryDelayMessage(retryAfter?: string | null): string | undefined {
  if (!retryAfter) return undefined;

  const seconds = Number(retryAfter);
  const retryAt = Number.isFinite(seconds)
    ? Date.now() + seconds * 1_000
    : Date.parse(retryAfter);
  if (!Number.isFinite(retryAt)) return undefined;

  const delay = Math.max(1, Math.ceil((retryAt - Date.now()) / 1_000));
  return ` Try again in about ${delay} ${delay === 1 ? "second" : "seconds"}.`;
}

function submissionErrorMessage(
  status: number,
  retryAfter?: string | null,
): string {
  switch (status) {
    case 400:
      return "Please check the report text and try again.";
    case 409:
      return "This request conflicted with an earlier submission. Try again.";
    case 429:
      return `Too many reports were sent.${retryDelayMessage(retryAfter) ?? " Try again shortly."}`;
    case 500:
      return "Signa couldn’t accept the report just now. Try again.";
    default:
      return "Could not submit the report. Try again.";
  }
}

export function ReportForm({
  submitReport = submitReportToApi,
  requestLocation = requestDeviceLocation,
}: ReportFormProps) {
  const [reportText, setReportText] = useState("");
  const [deviceLocation, setDeviceLocation] = useState<DeviceLocation | null>(
    null,
  );
  const [locationState, setLocationState] = useState<LocationState>("idle");
  const [submissionState, setSubmissionState] =
    useState<SubmissionState>("idle");
  const [acknowledgement, setAcknowledgement] =
    useState<ReportAcknowledgement | null>(null);
  const [message, setMessage] = useState("");
  const [hasValidationError, setHasValidationError] = useState(false);
  const locationRequestId = useRef(0);
  const idempotencyKey = useRef<string | null>(null);

  async function handleRequestLocation() {
    const requestId = ++locationRequestId.current;
    setDeviceLocation(null);
    setLocationState("requesting");

    try {
      const location = await requestLocation();
      if (requestId !== locationRequestId.current) return;
      idempotencyKey.current = null;
      setDeviceLocation(location);
      setLocationState("ready");
    } catch (error) {
      if (requestId !== locationRequestId.current) return;
      setDeviceLocation(null);
      setLocationState(
        error instanceof DeviceLocationError ? error.reason : "unknown",
      );
    }
  }

  function handleContinueWithoutLocation() {
    locationRequestId.current += 1;
    if (deviceLocation) idempotencyKey.current = null;
    setDeviceLocation(null);
    setLocationState("idle");
  }

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (submissionState === "submitting") return;

    const rawText = reportText.trim();
    if (!rawText) {
      setHasValidationError(true);
      setSubmissionState("error");
      setMessage("Add a short description.");
      return;
    }

    setHasValidationError(false);
    setSubmissionState("submitting");
    setMessage("Submitting report…");

    const request: CreateReportRequest = {
      raw_text: rawText,
      ...(deviceLocation ? { device_location: deviceLocation } : {}),
    };
    try {
      const requestKey = idempotencyKey.current ?? createIdempotencyKey();
      idempotencyKey.current = requestKey;
      const result = await submitReport(request, requestKey);
      if (!result.ok) {
        if (result.status === 409) idempotencyKey.current = null;
        setSubmissionState("error");
        setMessage(submissionErrorMessage(result.status, result.retryAfter));
        return;
      }

      setAcknowledgement(result.acknowledgement);
      idempotencyKey.current = null;
      setSubmissionState("success");
      setMessage("");
    } catch (error) {
      setSubmissionState("error");
      setMessage(
        error instanceof ApiConfigurationError
          ? "Report service isn’t configured. Please try again later."
          : error instanceof SecureKeyUnavailableError
            ? "A secure submission key isn’t available. Update your browser and try again."
          : "Couldn’t reach Signa. Check your connection and try again.",
      );
    }
  }

  function handleChange(value: string) {
    if (value.trim() !== reportText.trim()) idempotencyKey.current = null;
    setReportText(value);
    if (submissionState === "error") {
      setHasValidationError(false);
      setSubmissionState("idle");
      setMessage("");
    }
  }

  function handleReset() {
    setReportText("");
    setAcknowledgement(null);
    setSubmissionState("idle");
    setMessage("");
    setHasValidationError(false);
    idempotencyKey.current = null;
    handleContinueWithoutLocation();
  }

  const isSubmitting = submissionState === "submitting";
  const isSuccess = submissionState === "success";

  return (
    <form className="space-y-6" onSubmit={handleSubmit}>
      {isSuccess ? (
        <div className="space-y-5" role="status" aria-live="polite">
          <p className="font-medium text-foreground">
            Report accepted for processing.
          </p>
          {acknowledgement && (
            <p className="text-sm leading-6 text-muted-foreground">
              Reference: <span className="font-mono">{acknowledgement.report_id}</span>
            </p>
          )}
          <p className="text-sm leading-6 text-muted-foreground">
            Thanks for sharing.
          </p>
          <Button
            className="h-11 px-5 text-sm font-semibold"
            onClick={handleReset}
            size="lg"
            type="button"
          >
            Write another report
          </Button>
        </div>
      ) : (
        <>
          <div className="space-y-2">
            <label className="text-sm font-semibold" htmlFor="report-text">
              What happened?
              <span className="ml-1 font-normal text-muted-foreground">
                (required)
              </span>
            </label>
            <Textarea
              aria-describedby="report-help report-message"
              aria-invalid={hasValidationError}
              aria-required="true"
              className="min-h-40 resize-y bg-background px-3 py-3 text-base leading-6 text-foreground shadow-xs"
              disabled={isSubmitting}
              id="report-text"
              maxLength={10_000}
              onChange={(event) => handleChange(event.target.value)}
              placeholder="What did you see or hear?"
              value={reportText}
            />
            <p
              className="text-sm leading-6 text-muted-foreground"
              id="report-help"
            >
              A short description is enough.
            </p>
            <p
              aria-live="polite"
              className={`min-h-6 text-sm leading-6 ${submissionState === "error" ? "font-medium text-destructive" : "text-muted-foreground"}`}
              id="report-message"
              role={submissionState === "error" ? "alert" : "status"}
            >
              {message}
            </p>
          </div>

          <section
            aria-labelledby="location-title"
            className="border-t border-border pt-5"
          >
            <h3
              className="text-sm font-medium text-foreground"
              id="location-title"
            >
              Location{" "}
              <span className="font-normal text-muted-foreground">
                (optional)
              </span>
            </h3>
            <p
              className="mt-1 text-sm leading-6 text-muted-foreground"
              id="location-help"
            >
              Choose to add your browser location to this report. Exact
              coordinates won’t appear on screen.
            </p>
            {locationState === "ready" ? (
              <div className="mt-3 flex flex-wrap items-center gap-3">
                <p className="text-sm leading-6 text-foreground" role="status">
                  Approximate location ready. It will accompany this report if
                  submitted.
                </p>
                <Button
                  className="h-10 px-3 text-sm"
                  disabled={submissionState === "submitting"}
                  onClick={handleContinueWithoutLocation}
                  size="lg"
                  type="button"
                  variant="outline"
                >
                  Remove location
                </Button>
              </div>
            ) : locationState === "requesting" ? (
              <div className="mt-3 flex flex-wrap items-center gap-3">
                <p
                  className="text-sm leading-6 text-muted-foreground"
                  role="status"
                >
                  Requesting location…
                </p>
                <Button
                  className="h-10 px-3 text-sm"
                  onClick={handleContinueWithoutLocation}
                  size="lg"
                  type="button"
                  variant="outline"
                >
                  Continue without location
                </Button>
              </div>
            ) : (
              <div className="mt-3">
                <Button
                  aria-describedby="location-help"
                  className="h-10 px-3 text-sm"
                  disabled={submissionState === "submitting"}
                  onClick={handleRequestLocation}
                  size="lg"
                  type="button"
                  variant="outline"
                >
                  Share my location
                </Button>
                {locationState !== "idle" && (
                  <p
                    className="mt-2 text-sm leading-6 text-muted-foreground"
                    role="status"
                  >
                    {locationMessages[locationState]}
                  </p>
                )}
              </div>
            )}
          </section>

          <div className="border-t border-border pt-5">
            <p className="text-sm font-medium text-foreground">
              Add evidence
              <span className="ml-1 font-normal text-muted-foreground">
                (optional)
              </span>
            </p>
            <p className="mt-1 text-sm leading-6 text-muted-foreground">
              Attachments aren’t available yet.
            </p>
            <Button
              aria-describedby="evidence-help"
              className="mt-3 h-10 border-border px-3 text-sm font-normal text-muted-foreground"
              disabled
              size="lg"
              variant="outline"
              type="button"
            >
              Add attachment (optional)
            </Button>
            <span className="sr-only" id="evidence-help">
              Attachments are not available in this preview.
            </span>
          </div>

          <Button
            className="h-12 w-full px-5 text-base font-semibold sm:w-auto"
            disabled={
              isSubmitting ||
              locationState === "requesting" ||
              !reportText.trim()
            }
            size="lg"
            type="submit"
          >
            {isSubmitting ? "Submitting…" : "Submit report"}
          </Button>
        </>
      )}
    </form>
  );
}
