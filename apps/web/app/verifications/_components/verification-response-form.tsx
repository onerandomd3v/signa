"use client";

import { useRef, useState, type FormEvent } from "react";
import { Button } from "../../../components/ui/button";
import type { VerificationResponseInput } from "../../../lib/api/generated";
import { SecureKeyUnavailableError } from "../../../lib/api/idempotency";
import { createIdempotencyKey } from "../../../lib/api/idempotency";
import type { VerificationApiResult } from "../../../lib/api/verifications";

type Conclusion = NonNullable<VerificationResponseInput["conclusion"]>;
type Observation = NonNullable<VerificationResponseInput["observation"]>;

const conclusions: { value: Conclusion; label: string }[] = [
  { value: "CONFIRM", label: "Confirm" },
  { value: "CANNOT_CONFIRM", label: "Cannot confirm" },
  { value: "DISPUTE", label: "Dispute" },
];
const observations: { value: Observation; label: string }[] = [
  { value: "SAW", label: "Saw it" },
  { value: "HEARD", label: "Heard it" },
];

function errorMessage(status: number): string {
  switch (status) {
    case 400:
      return "Check your response and try again.";
    case 409:
      return "This key conflicts with different response details. Submit again to use a new key.";
    case 503:
      return "The service is unavailable. Try again.";
    default:
      return "Couldn’t send your response. Check your connection and try again.";
  }
}

function radioId(prefix: string, group: string, value: string): string {
  return `${prefix}-${group}-${value.toLowerCase().replaceAll("_", "-")}`;
}

function semanticPayload(
  conclusion: Conclusion | null,
  observation: Observation | null,
): VerificationResponseInput {
  if (conclusion && observation) return { conclusion, observation };
  if (conclusion) return { conclusion };
  if (observation) return { observation };
  throw new Error("At least one response dimension is required.");
}

export function VerificationResponseForm({
  requestId,
  submitResponse,
  onProtectedError,
}: {
  requestId: string;
  submitResponse: (
    body: VerificationResponseInput,
    idempotencyKey: string,
  ) => Promise<VerificationApiResult<unknown>>;
  onProtectedError: (status: number) => void;
}) {
  const [conclusion, setConclusion] = useState<Conclusion | null>(null);
  const [observation, setObservation] = useState<Observation | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [submitted, setSubmitted] = useState(false);
  const [message, setMessage] = useState("");
  const [invalid, setInvalid] = useState(false);
  const inFlight = useRef(false);
  const attempt = useRef<{ semantic: string; key: string } | null>(null);
  const prefix = `response-${requestId.replace(/[^a-zA-Z0-9_-]/g, "-")}`;

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (inFlight.current || submitted) return;
    if (!conclusion && !observation) {
      setInvalid(true);
      setMessage("Choose a conclusion or an observation before submitting.");
      return;
    }

    const body = semanticPayload(conclusion, observation);
    const semantic = JSON.stringify({
      conclusion: conclusion ?? null,
      observation: observation ?? null,
    });
    let key: string;
    try {
      if (attempt.current?.semantic === semantic) {
        key = attempt.current.key;
      } else {
        key = createIdempotencyKey();
        attempt.current = { semantic, key };
      }
    } catch (error) {
      setMessage(
        error instanceof SecureKeyUnavailableError
          ? "A secure response key isn’t available in this browser."
          : "Couldn’t prepare your response. Try again.",
      );
      return;
    }

    inFlight.current = true;
    setSubmitting(true);
    setInvalid(false);
    setMessage("Sending your response…");
    try {
      const result = await submitResponse(body, key);
      if (result.ok) {
        attempt.current = null;
        setSubmitted(true);
        setMessage(
          "Your response was recorded as evidence. It does not determine incident truth or change confidence.",
        );
        return;
      }
      if ([401, 403, 404].includes(result.status)) {
        attempt.current = null;
        onProtectedError(result.status);
        return;
      }
      if (result.status === 409) attempt.current = null;
      setMessage(errorMessage(result.status));
    } catch {
      setMessage(errorMessage(0));
    } finally {
      inFlight.current = false;
      setSubmitting(false);
    }
  }

  return (
    <form className="space-y-5" onSubmit={handleSubmit}>
      <p className="text-sm leading-6 text-muted-foreground">
        Conclusion and observation are separate. Choose either one, or both.
      </p>

      <fieldset
        aria-describedby={`${prefix}-conclusion-help`}
        className="space-y-2"
        disabled={submitting || submitted}
      >
        <legend className="text-sm font-semibold">
          Conclusion{" "}
          <span className="font-normal text-muted-foreground">(optional)</span>
        </legend>
        <p
          className="text-sm leading-6 text-muted-foreground"
          id={`${prefix}-conclusion-help`}
        >
          What conclusion can you share?
        </p>
        <div className="grid gap-2 sm:grid-cols-3">
          {conclusions.map((option) => {
            const id = radioId(prefix, "conclusion", option.value);
            return (
              <label
                className={`flex min-h-11 cursor-pointer items-center gap-3 rounded-lg border px-3 py-2 text-sm transition-colors focus-within:ring-2 focus-within:ring-ring ${conclusion === option.value ? "border-primary bg-primary/5" : "border-border bg-card hover:bg-muted/50"}`}
                htmlFor={id}
                key={option.value}
              >
                <input
                  checked={conclusion === option.value}
                  className="size-4 accent-primary"
                  id={id}
                  name={`${prefix}-conclusion`}
                  onChange={() => setConclusion(option.value)}
                  type="radio"
                  value={option.value}
                />
                {option.label}
              </label>
            );
          })}
        </div>
      </fieldset>

      <fieldset
        aria-describedby={`${prefix}-observation-help`}
        className="space-y-2"
        disabled={submitting || submitted}
      >
        <legend className="text-sm font-semibold">
          Observation{" "}
          <span className="font-normal text-muted-foreground">(optional)</span>
        </legend>
        <p
          className="text-sm leading-6 text-muted-foreground"
          id={`${prefix}-observation-help`}
        >
          What did you observe?
        </p>
        <div className="grid gap-2 sm:grid-cols-2">
          {observations.map((option) => {
            const id = radioId(prefix, "observation", option.value);
            return (
              <label
                className={`flex min-h-11 cursor-pointer items-center gap-3 rounded-lg border px-3 py-2 text-sm transition-colors focus-within:ring-2 focus-within:ring-ring ${observation === option.value ? "border-primary bg-primary/5" : "border-border bg-card hover:bg-muted/50"}`}
                htmlFor={id}
                key={option.value}
              >
                <input
                  checked={observation === option.value}
                  className="size-4 accent-primary"
                  id={id}
                  name={`${prefix}-observation`}
                  onChange={() => setObservation(option.value)}
                  type="radio"
                  value={option.value}
                />
                {option.label}
              </label>
            );
          })}
        </div>
      </fieldset>

      <p
        aria-live="polite"
        className={`min-h-6 text-sm leading-6 ${invalid ? "font-medium text-destructive" : "text-muted-foreground"}`}
        role={invalid ? "alert" : "status"}
      >
        {message}
      </p>
      {!submitted && (
        <Button
          className="min-h-11 w-full px-5 text-base font-semibold sm:w-auto"
          disabled={submitting}
          type="submit"
        >
          {submitting ? "Sending…" : "Send response"}
        </Button>
      )}
    </form>
  );
}
