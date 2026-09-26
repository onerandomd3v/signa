import { getAlert } from "./generated";
import type { AlertRead } from "./generated";
import { createClient } from "./generated/client";
import { getApiBaseUrl } from "./reports";

export type AlertReadResult =
  | { status: "authorized"; alert: AlertRead }
  | { status: "unauthorized" | "not-found" | "invalid" | "unavailable" };

/** Reads one authorized alert snapshot through the generated cookie-auth client. */
export async function fetchAuthorizedAlert(
  alertId: string,
  signal: AbortSignal,
  fetchImplementation: typeof fetch = globalThis.fetch,
): Promise<AlertReadResult> {
  try {
    const result = await getAlert({
      path: { alert_id: alertId },
      signal,
      client: createClient({
        baseUrl: getApiBaseUrl(),
        credentials: "include",
        fetch: fetchImplementation,
      }),
    });

    if (result.data) return { status: "authorized", alert: result.data };

    switch (result.response?.status) {
      case 400:
        return { status: "invalid" };
      case 401:
        return { status: "unauthorized" };
      case 404:
        return { status: "not-found" };
      default:
        return { status: "unavailable" };
    }
  } catch {
    // Do not surface/log server error bodies, which may contain private data.
    return { status: "unavailable" };
  }
}
