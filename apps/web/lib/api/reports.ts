import { createReport } from "./generated";
import type {
  CreateReportRequest,
  ErrorResponse,
  ReportAcknowledgement,
} from "./generated";
import { createClient } from "./generated/client";

const DEFAULT_API_BASE_URL = "http://localhost:8080";

export class ApiConfigurationError extends Error {
  constructor() {
    super("NEXT_PUBLIC_API_BASE_URL must be configured for production.");
    this.name = "ApiConfigurationError";
  }
}

export type ReportSubmissionResult =
  | { ok: true; acknowledgement: ReportAcknowledgement }
  | {
      ok: false;
      status: number;
      error?: ErrorResponse;
      retryAfter?: string | null;
    };

export function getApiBaseUrl(): string {
  const configuredUrl = process.env.NEXT_PUBLIC_API_BASE_URL?.trim();
  if (!configuredUrl) {
    if (process.env.NODE_ENV === "production") {
      throw new ApiConfigurationError();
    }
    return DEFAULT_API_BASE_URL;
  }

  let parsedUrl: URL;
  try {
    parsedUrl = new URL(configuredUrl);
  } catch {
    throw new ApiConfigurationError();
  }
  if (
    (parsedUrl.protocol !== "http:" && parsedUrl.protocol !== "https:") ||
    !parsedUrl.host ||
    parsedUrl.username ||
    parsedUrl.password ||
    parsedUrl.search ||
    parsedUrl.hash
  ) {
    throw new ApiConfigurationError();
  }

  return configuredUrl.replace(/\/+$/, "");
}

export async function submitReport(
  body: CreateReportRequest,
  idempotencyKey: string,
  fetchImplementation: typeof fetch = globalThis.fetch,
): Promise<ReportSubmissionResult> {
  const client = createClient({
    baseUrl: getApiBaseUrl(),
    fetch: fetchImplementation,
  });
  const result = await createReport({
    body,
    client,
    headers: { "Idempotency-Key": idempotencyKey },
  });

  if (result.data) {
    return { ok: true, acknowledgement: result.data };
  }

  return {
    ok: false,
    status: result.response?.status ?? 0,
    error: result.error,
    retryAfter: result.response?.headers.get("Retry-After"),
  };
}
