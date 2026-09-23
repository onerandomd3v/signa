"use client";

import { useState, type FormEvent } from "react";
import { Button } from "../../components/ui/button";
import { Textarea } from "../../components/ui/textarea";
import type { CreateReportRequest } from "../../lib/api/generated";

type ReportFormProps = {
  onSubmit?: (request: CreateReportRequest) => Promise<void>;
};

type SubmissionState = "idle" | "submitting" | "success" | "error";

const unavailableMessage = "Not sent — report submission isn’t available yet.";

export function ReportForm({ onSubmit }: ReportFormProps) {
  const [reportText, setReportText] = useState("");
  const [submissionState, setSubmissionState] =
    useState<SubmissionState>("idle");
  const [message, setMessage] = useState("");
  const [hasValidationError, setHasValidationError] = useState(false);

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

    if (!onSubmit) {
      setSubmissionState("error");
      setMessage(unavailableMessage);
      return;
    }

    try {
      await onSubmit({ raw_text: rawText });
      setSubmissionState("success");
      setMessage("Report submitted.");
    } catch {
      setSubmissionState("error");
      setMessage("Could not submit. Try again.");
    }
  }

  function handleChange(value: string) {
    setReportText(value);
    if (submissionState === "error") {
      setHasValidationError(false);
      setSubmissionState("idle");
      setMessage("");
    }
  }

  function handleReset() {
    setReportText("");
    setSubmissionState("idle");
    setMessage("");
    setHasValidationError(false);
  }

  const isSubmitting = submissionState === "submitting";
  const isSuccess = submissionState === "success";

  return (
    <form className="space-y-6" onSubmit={handleSubmit}>
      {isSuccess ? (
        <div className="space-y-5" role="status" aria-live="polite">
          <p className="font-medium text-foreground">{message}</p>
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
            disabled={isSubmitting}
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
