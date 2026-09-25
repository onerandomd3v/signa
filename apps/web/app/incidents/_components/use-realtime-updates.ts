"use client";

import { useEffect, useRef, useState } from "react";
import {
  getRealtimeHttpStatus,
  streamAuthenticatedEvents,
  type AuthenticatedRealtimeOptions,
  type RealtimeEvent,
} from "@/lib/api/realtime";

export type RealtimeConnectionStatus =
  "connecting" | "live" | "reconnecting" | "unavailable";

export type RealtimeInvalidations = {
  incidents: boolean;
  alerts: boolean;
};

export type RealtimeConnector = (
  options: AuthenticatedRealtimeOptions,
) => ReturnType<typeof streamAuthenticatedEvents>;

type RealtimeSleeper = (ms: number, signal: AbortSignal) => Promise<void>;

type UseRealtimeUpdatesOptions = {
  onInvalidation: (invalidations: RealtimeInvalidations) => void;
  connect?: RealtimeConnector;
  sleep?: RealtimeSleeper;
  coalesceMs?: number;
};

const MIN_RECONNECT_DELAY_MS = 1_000;
const MAX_RECONNECT_DELAY_MS = 15_000;
const EVENT_COALESCE_MS = 100;

export function sleepWithSignal(
  ms: number,
  signal: AbortSignal,
): Promise<void> {
  return new Promise((resolve) => {
    if (signal.aborted) {
      resolve();
      return;
    }

    const finish = () => {
      clearTimeout(timer);
      signal.removeEventListener("abort", finish);
      resolve();
    };
    const timer = setTimeout(finish, ms);
    signal.addEventListener("abort", finish, { once: true });
  });
}

function eventCategory(
  event: RealtimeEvent,
): keyof RealtimeInvalidations | null {
  if (typeof event.event !== "string") return null;
  if (event.event.startsWith("incident.")) return "incidents";
  if (event.event.startsWith("alert.")) return "alerts";
  return null;
}

export function useRealtimeUpdates({
  onInvalidation,
  connect = streamAuthenticatedEvents,
  sleep = sleepWithSignal,
  coalesceMs = EVENT_COALESCE_MS,
}: UseRealtimeUpdatesOptions): {
  status: RealtimeConnectionStatus;
  alertUpdateReceived: boolean;
} {
  const onInvalidationRef = useRef(onInvalidation);
  const connectRef = useRef(connect);
  const sleepRef = useRef(sleep);
  const [status, setStatus] = useState<RealtimeConnectionStatus>("connecting");
  const [alertUpdateReceived, setAlertUpdateReceived] = useState(false);

  useEffect(() => {
    onInvalidationRef.current = onInvalidation;
    connectRef.current = connect;
    sleepRef.current = sleep;
  }, [connect, onInvalidation, sleep]);

  useEffect(() => {
    const controller = new AbortController();
    const { signal } = controller;
    let stoppedByAuth = false;
    let cursor: string | undefined;
    let reconnectDelay = MIN_RECONNECT_DELAY_MS;
    let coalesceTimer: ReturnType<typeof setTimeout> | undefined;
    const pending: RealtimeInvalidations = { incidents: false, alerts: false };

    const scheduleInvalidation = (event: RealtimeEvent) => {
      const category = eventCategory(event);
      if (!category) return;

      pending[category] = true;
      if (category === "alerts") setAlertUpdateReceived(true);
      if (coalesceTimer !== undefined) return;

      coalesceTimer = setTimeout(() => {
        coalesceTimer = undefined;
        if (signal.aborted) return;
        const invalidations = { ...pending };
        pending.incidents = false;
        pending.alerts = false;
        onInvalidationRef.current(invalidations);
      }, coalesceMs);
    };

    const run = async () => {
      while (!signal.aborted && !stoppedByAuth) {
        const attemptController = new AbortController();
        const abortAttempt = () => attemptController.abort();
        signal.addEventListener("abort", abortAttempt, { once: true });
        let lastError: unknown;

        try {
          const result = await connectRef.current({
            signal: attemptController.signal,
            lastEventId: cursor,
            onConnection: () => {
              lastError = undefined;
              if (!signal.aborted) setStatus("live");
            },
            onSseEvent: (event) => {
              if (event.id) cursor = event.id;
              if (!signal.aborted) {
                setStatus("live");
                scheduleInvalidation(event);
              }
            },
            onSseError: (error) => {
              lastError = error;
              const statusCode = getRealtimeHttpStatus(error);
              if (!signal.aborted) {
                setStatus(
                  statusCode === 401 || statusCode === 503
                    ? "unavailable"
                    : "reconnecting",
                );
              }
              if (statusCode === 401) {
                stoppedByAuth = true;
                attemptController.abort();
              }
            },
            sseSleepFn: (ms) => sleepRef.current(ms, attemptController.signal),
          });

          for await (const event of result.stream) {
            // The generated callback carries the opaque ID and event name.
            // Event data is deliberately ignored; REST remains authoritative.
            void event;
          }
        } catch (error) {
          lastError = error;
          const statusCode = getRealtimeHttpStatus(error);
          if (statusCode === 401) stoppedByAuth = true;
          if (!signal.aborted) {
            setStatus(
              statusCode === 401 || statusCode === 503
                ? "unavailable"
                : "reconnecting",
            );
          }
        } finally {
          signal.removeEventListener("abort", abortAttempt);
        }

        if (signal.aborted || stoppedByAuth) break;

        const statusCode = getRealtimeHttpStatus(lastError);
        if (lastError === undefined) {
          // A normal EOF is still a disconnect. Reopen with the latest opaque ID.
          setStatus("reconnecting");
        } else if (statusCode === 503) {
          setStatus("unavailable");
        } else {
          setStatus("reconnecting");
        }

        await sleepRef.current(reconnectDelay, signal);
        reconnectDelay = Math.min(reconnectDelay * 2, MAX_RECONNECT_DELAY_MS);
      }
    };

    void run();

    return () => {
      controller.abort();
      if (coalesceTimer !== undefined) clearTimeout(coalesceTimer);
    };
  }, [coalesceMs]);

  return { status, alertUpdateReceived };
}
