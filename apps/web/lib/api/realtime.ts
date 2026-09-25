import { streamRealtimeEvents } from "./generated";
import { createClient } from "./generated/client";
import { getApiBaseUrl } from "./reports";

/**
 * Opens the authenticated SSE stream with browser cookies included for a
 * separately hosted frontend/API deployment.
 */
export function streamAuthenticatedEvents(
  fetchImplementation: typeof fetch = globalThis.fetch,
) {
  return streamRealtimeEvents({
    client: createClient({
      baseUrl: getApiBaseUrl(),
      credentials: "include",
      fetch: fetchImplementation,
    }),
  });
}
