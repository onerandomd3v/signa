import { streamRealtimeEvents } from "./generated";
import { createClient } from "./generated/client";
import { getApiBaseUrl } from "./reports";
import type {
  ServerSentEventsOptions,
  StreamEvent,
} from "./generated/core/serverSentEvents.gen";
import type { ServerSentEventsResult } from "./generated/core/serverSentEvents.gen";

export type RealtimeEvent = StreamEvent<unknown>;

export type AuthenticatedRealtimeOptions = {
  signal: AbortSignal;
  lastEventId?: string;
  fetchImplementation?: typeof fetch;
  onConnection?: () => void;
  onSseEvent?: (event: RealtimeEvent) => void;
  onSseError?: (error: unknown) => void;
  sseSleepFn?: (ms: number) => Promise<void>;
};

/**
 * Opens the authenticated SSE stream with browser cookies included for a
 * separately hosted frontend/API deployment.
 */
export function streamAuthenticatedEvents(
  options: AuthenticatedRealtimeOptions,
): Promise<ServerSentEventsResult<unknown>> {
  const fetchImplementation = options.fetchImplementation ?? globalThis.fetch;
  const fetchWithConnection: typeof fetch = async (input, init) => {
    const response = await fetchImplementation(input, init);
    if (response.ok) options.onConnection?.();
    return response;
  };

  // The generated endpoint options expose the retry callback but currently
  // omit the generator's injectable sleep function. Pass it through without
  // editing generated code so component cleanup can cancel retry waits.
  const streamOptions: NonNullable<Parameters<typeof streamRealtimeEvents>[0]> &
    Pick<ServerSentEventsOptions, "sseSleepFn"> = {
    client: createClient({
      baseUrl: getApiBaseUrl(),
      credentials: "include",
      fetch: fetchWithConnection,
    }),
    signal: options.signal,
    headers: options.lastEventId
      ? { "Last-Event-ID": options.lastEventId }
      : undefined,
    onSseEvent: options.onSseEvent,
    onSseError: options.onSseError,
    sseDefaultRetryDelay: 500,
    sseMaxRetryDelay: 2_000,
    sseMaxRetryAttempts: 2,
    sseSleepFn: options.sseSleepFn,
  };

  return streamRealtimeEvents({
    ...streamOptions,
  });
}

export function getRealtimeHttpStatus(error: unknown): number | null {
  const message = error instanceof Error ? error.message : String(error);
  const match = /^SSE failed:\s*(\d{3})\b/.exec(message);
  return match ? Number(match[1]) : null;
}
