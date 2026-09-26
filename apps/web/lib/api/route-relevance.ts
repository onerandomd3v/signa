import { evaluateRouteRelevance, type ErrorResponse } from "./generated";
import type {
  RouteRelevanceRequest,
  RouteRelevanceResponse,
} from "./generated";
import { createClient } from "./generated/client";
import { getApiBaseUrl } from "./reports";

export type RouteRelevanceResult =
  | { status: "success"; response: RouteRelevanceResponse }
  | { status: "validation" | "unauthorized" | "forbidden" }
  | { status: "rate-limited"; retryAfter: string | null }
  | { status: "route-unavailable" | "incident-data-unavailable" }
  | { status: "service-error" | "network-error" | "aborted" };

function errorCode(value: unknown): string | undefined {
  if (typeof value !== "object" || value === null) return undefined;
  const error = (value as Partial<ErrorResponse>).error;
  return typeof error?.code === "string" ? error.code : undefined;
}

/** Sends one explicit, cookie-authenticated route evaluation request. */
export async function requestRouteRelevance(
  body: RouteRelevanceRequest,
  signal: AbortSignal,
  fetchImplementation: typeof fetch = globalThis.fetch,
): Promise<RouteRelevanceResult> {
  try {
    const result = await evaluateRouteRelevance({
      body,
      signal,
      client: createClient({
        baseUrl: getApiBaseUrl(),
        credentials: "include",
        fetch: fetchImplementation,
      }),
    });

    if (result.data) return { status: "success", response: result.data };
    if (!result.response) {
      return signal.aborted
        ? { status: "aborted" }
        : { status: "network-error" };
    }

    switch (result.response?.status) {
      case 400:
        return { status: "validation" };
      case 401:
        return { status: "unauthorized" };
      case 403:
        return { status: "forbidden" };
      case 429:
        return {
          status: "rate-limited",
          retryAfter: result.response.headers.get("Retry-After"),
        };
      case 503:
        if (errorCode(result.error) === "incident_data_unavailable") {
          return { status: "incident-data-unavailable" };
        }
        return { status: "route-unavailable" };
      default:
        return { status: "service-error" };
    }
  } catch {
    if (signal.aborted) return { status: "aborted" };
    return { status: "network-error" };
  }
}
