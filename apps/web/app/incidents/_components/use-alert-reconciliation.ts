"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import type { AlertRead } from "@/lib/api/generated";
import { fetchAuthorizedAlert, type AlertReadResult } from "@/lib/api/alerts";
import type { RealtimeAlertReference } from "./use-realtime-updates";

const MAX_VISIBLE_ALERTS = 3;
const MAX_REMEMBERED_ALERTS = 128;
// Keep replay suppression bounded independently from active pending reads.
const MAX_TERMINAL_VISIBILITY_MARKERS = 128;
const MAX_CONCURRENT_ALERT_READS = 3;
const MAX_QUEUED_ALERT_READS = 12;
// Event-triggered 404s get three retries after the first read: 1s, 2s, then 4s.
const ALERT_VISIBILITY_RETRY_DELAYS_MS = [1_000, 2_000, 4_000] as const;
const UUID_PATTERN =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export type AlertSnapshotEntry =
  | {
      alertId: string;
      order: number;
      status: "authorized";
      alert: AlertRead;
      stale: boolean;
    }
  | {
      alertId: string;
      order: number;
      status: "unavailable";
    };

export type AlertReader = (
  alertId: string,
  signal: AbortSignal,
) => Promise<AlertReadResult>;

function isUuid(value: unknown): value is string {
  return typeof value === "string" && UUID_PATTERN.test(value);
}

function isAlertRead(value: unknown, requestedId: string): value is AlertRead {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return false;
  }
  const alert = value as Record<string, unknown>;
  return (
    alert.alert_id === requestedId &&
    isUuid(alert.alert_id) &&
    isUuid(alert.incident_id) &&
    ["IMMEDIATE", "NEARBY"].includes(String(alert.alert_type)) &&
    [
      "UNVERIFIED",
      "EMERGING",
      "CORROBORATED",
      "HIGH_CONFIDENCE",
      "DISPUTED",
    ].includes(String(alert.confidence_snapshot)) &&
    ["LOW", "MODERATE", "HIGH", "CRITICAL"].includes(
      String(alert.severity_snapshot),
    ) &&
    ["OPEN", "RESOLVING", "RESOLVED", "EXPIRED"].includes(
      String(alert.status_snapshot),
    ) &&
    ["P1", "P2", "P3", "NONE"].includes(String(alert.priority_snapshot)) &&
    ["FRESH", "STALE", "UNKNOWN"].includes(String(alert.freshness_snapshot)) &&
    typeof alert.message === "string" &&
    typeof alert.as_of === "string" &&
    Number.isFinite(Date.parse(alert.as_of)) &&
    typeof alert.created_at === "string" &&
    Number.isFinite(Date.parse(alert.created_at)) &&
    (alert.supersedes_alert_id === null || isUuid(alert.supersedes_alert_id))
  );
}

function compareEntries(a: AlertSnapshotEntry, b: AlertSnapshotEntry): number {
  if (a.status === "unavailable" || b.status === "unavailable") {
    return b.order - a.order;
  }
  const aAsOf = Date.parse(a.alert.as_of);
  const bAsOf = Date.parse(b.alert.as_of);
  if (aAsOf !== bAsOf) return bAsOf - aAsOf;
  const createdDifference =
    Date.parse(b.alert.created_at) - Date.parse(a.alert.created_at);
  return createdDifference || b.order - a.order;
}

type UseAlertReconciliationOptions = {
  readAlert?: AlertReader;
};

export function useAlertReconciliation({
  readAlert = fetchAuthorizedAlert,
}: UseAlertReconciliationOptions = {}): {
  alerts: AlertSnapshotEntry[];
  hasOverflow: boolean;
  acceptEvents: (events: RealtimeAlertReference[]) => void;
  retry: (alertId: string) => void;
  revalidateKnown: () => void;
  clearProtectedAlerts: () => void;
} {
  const [alerts, setAlerts] = useState<AlertSnapshotEntry[]>([]);
  const [hasOverflow, setHasOverflow] = useState(false);
  const alertsRef = useRef<AlertSnapshotEntry[]>([]);
  const readerRef = useRef(readAlert);
  const mountedRef = useRef(false);
  const requestControllersRef = useRef(new Map<string, AbortController>());
  const queuedIdsRef = useRef<string[]>([]);
  const queuedIdSetRef = useRef(new Set<string>());
  const drainQueueRef = useRef<() => void>(() => undefined);
  const rememberedRef = useRef(new Map<string, true>());
  const eventAlertIdsRef = useRef(new Set<string>());
  const pendingVisibilityRef = useRef(
    new Map<
      string,
      {
        retriesUsed: number;
        retryQueued?: boolean;
        timer?: ReturnType<typeof setTimeout>;
      }
    >(),
  );
  const terminalVisibilityRef = useRef(new Map<string, true>());
  const enqueueRef = useRef<(alertId: string, force?: boolean) => void>(
    () => undefined,
  );
  const authBlockedRef = useRef(false);
  const authFailureIdRef = useRef<string | null>(null);
  const orderRef = useRef(0);

  useEffect(() => {
    readerRef.current = readAlert;
  }, [readAlert]);

  useEffect(() => {
    mountedRef.current = true;
    const requestControllers = requestControllersRef.current;
    const queuedIds = queuedIdsRef.current;
    const queuedIdSet = queuedIdSetRef.current;
    const pendingVisibility = pendingVisibilityRef.current;
    const eventAlertIds = eventAlertIdsRef.current;
    const terminalVisibility = terminalVisibilityRef.current;
    return () => {
      mountedRef.current = false;
      for (const controller of requestControllers.values()) {
        controller.abort();
      }
      requestControllers.clear();
      queuedIds.length = 0;
      queuedIdSet.clear();
      for (const pending of pendingVisibility.values()) {
        if (pending.timer !== undefined) clearTimeout(pending.timer);
      }
      pendingVisibility.clear();
      eventAlertIds.clear();
      terminalVisibility.clear();
    };
  }, []);

  const publish = useCallback((next: AlertSnapshotEntry[]) => {
    const limited = [...next].sort(compareEntries).slice(0, MAX_VISIBLE_ALERTS);
    alertsRef.current = limited;
    if (mountedRef.current) setAlerts(limited);
  }, []);

  const clearProtectedAlerts = useCallback(() => {
    authBlockedRef.current = true;
    authFailureIdRef.current = null;
    for (const controller of requestControllersRef.current.values()) {
      controller.abort();
    }
    requestControllersRef.current.clear();
    queuedIdsRef.current = [];
    queuedIdSetRef.current.clear();
    rememberedRef.current.clear();
    for (const pending of pendingVisibilityRef.current.values()) {
      if (pending.timer !== undefined) clearTimeout(pending.timer);
    }
    pendingVisibilityRef.current.clear();
    eventAlertIdsRef.current.clear();
    terminalVisibilityRef.current.clear();
    if (mountedRef.current) setHasOverflow(false);
    publish([]);
  }, [publish]);

  const markVisibilityTerminal = useCallback((alertId: string) => {
    const terminalIds = terminalVisibilityRef.current;
    terminalIds.delete(alertId);
    terminalIds.set(alertId, true);
    while (terminalIds.size > MAX_TERMINAL_VISIBILITY_MARKERS) {
      const oldest = terminalIds.keys().next().value;
      if (oldest === undefined) break;
      terminalIds.delete(oldest);
    }
  }, []);

  const finishPendingVisibility = useCallback(
    (alertId: string, terminal: boolean) => {
      const pending = pendingVisibilityRef.current.get(alertId);
      if (pending?.timer !== undefined) clearTimeout(pending.timer);
      pendingVisibilityRef.current.delete(alertId);
      if (!eventAlertIdsRef.current.has(alertId)) return;
      eventAlertIdsRef.current.delete(alertId);
      if (terminal) markVisibilityTerminal(alertId);
    },
    [markVisibilityTerminal],
  );

  const remember = useCallback(
    (alertId: string) => {
      rememberedRef.current.delete(alertId);
      rememberedRef.current.set(alertId, true);
      while (rememberedRef.current.size > MAX_REMEMBERED_ALERTS) {
        const oldest = rememberedRef.current.keys().next().value;
        if (oldest === undefined) break;
        rememberedRef.current.delete(oldest);
        const pending = pendingVisibilityRef.current.get(oldest);
        if (pending || eventAlertIdsRef.current.has(oldest)) {
          finishPendingVisibility(oldest, true);
        }
      }
    },
    [finishPendingVisibility],
  );

  const fetchSnapshot = useCallback(
    async (alertId: string, controller: AbortController) => {
      try {
        const result = await readerRef.current(alertId, controller.signal);
        if (!mountedRef.current || controller.signal.aborted) return;

        if (result.status === "authorized") {
          finishPendingVisibility(alertId, false);
          terminalVisibilityRef.current.delete(alertId);
          if (!isAlertRead(result.alert, alertId)) {
            const existing = alertsRef.current.find(
              (entry) => entry.alertId === alertId,
            );
            const unavailable: AlertSnapshotEntry =
              existing?.status === "authorized"
                ? { ...existing, stale: true }
                : {
                    alertId,
                    order: ++orderRef.current,
                    status: "unavailable",
                  };
            publish([
              ...alertsRef.current.filter((entry) => entry.alertId !== alertId),
              unavailable,
            ]);
            return;
          }

          authBlockedRef.current = false;
          authFailureIdRef.current = null;
          publish([
            ...alertsRef.current.filter((entry) => entry.alertId !== alertId),
            {
              alertId,
              order: ++orderRef.current,
              status: "authorized",
              alert: result.alert,
              stale: false,
            },
          ]);
          return;
        }

        if (result.status === "unauthorized") {
          clearProtectedAlerts();
          authFailureIdRef.current = alertId;
          return;
        }

        if (
          result.status === "not-found" &&
          eventAlertIdsRef.current.has(alertId)
        ) {
          const pending = pendingVisibilityRef.current.get(alertId) ?? {
            retriesUsed: 0,
          };
          if (pending.retriesUsed < ALERT_VISIBILITY_RETRY_DELAYS_MS.length) {
            const delay = ALERT_VISIBILITY_RETRY_DELAYS_MS[pending.retriesUsed];
            const timer = setTimeout(() => {
              const current = pendingVisibilityRef.current.get(alertId);
              if (!current) return;
              pendingVisibilityRef.current.set(alertId, {
                ...current,
                timer: undefined,
                retryQueued: true,
              });
              enqueueRef.current(alertId, true);
            }, delay);
            pendingVisibilityRef.current.set(alertId, { ...pending, timer });
          } else {
            finishPendingVisibility(alertId, true);
          }
          publish(
            alertsRef.current.filter((entry) => entry.alertId !== alertId),
          );
          return;
        }

        if (result.status === "not-found" || result.status === "invalid") {
          finishPendingVisibility(
            alertId,
            eventAlertIdsRef.current.has(alertId),
          );
          publish(
            alertsRef.current.filter((entry) => entry.alertId !== alertId),
          );
          if (authFailureIdRef.current === alertId) {
            authFailureIdRef.current = null;
          }
          return;
        }

        finishPendingVisibility(alertId, true);
        const existing = alertsRef.current.find(
          (entry) => entry.alertId === alertId,
        );
        const staleEntry: AlertSnapshotEntry =
          existing?.status === "authorized"
            ? { ...existing, stale: true }
            : {
                alertId,
                order: ++orderRef.current,
                status: "unavailable",
              };
        publish([
          ...alertsRef.current.filter((entry) => entry.alertId !== alertId),
          staleEntry,
        ]);
      } catch {
        if (!mountedRef.current || controller.signal.aborted) return;
        finishPendingVisibility(alertId, true);
        const existing = alertsRef.current.find(
          (entry) => entry.alertId === alertId,
        );
        const staleEntry: AlertSnapshotEntry =
          existing?.status === "authorized"
            ? { ...existing, stale: true }
            : {
                alertId,
                order: ++orderRef.current,
                status: "unavailable",
              };
        publish([
          ...alertsRef.current.filter((entry) => entry.alertId !== alertId),
          staleEntry,
        ]);
      } finally {
        if (requestControllersRef.current.get(alertId) === controller) {
          requestControllersRef.current.delete(alertId);
        }
        drainQueueRef.current();
      }
    },
    [clearProtectedAlerts, finishPendingVisibility, publish],
  );

  const drainQueue = useCallback(() => {
    if (!mountedRef.current || authBlockedRef.current) return;
    while (
      requestControllersRef.current.size < MAX_CONCURRENT_ALERT_READS &&
      queuedIdsRef.current.length > 0
    ) {
      const alertId = queuedIdsRef.current.shift();
      if (!alertId) continue;
      queuedIdSetRef.current.delete(alertId);
      const pending = pendingVisibilityRef.current.get(alertId);
      if (pending?.retryQueued) {
        pending.retryQueued = false;
        pending.retriesUsed += 1;
      }
      const controller = new AbortController();
      requestControllersRef.current.set(alertId, controller);
      void fetchSnapshot(alertId, controller);
    }
  }, [fetchSnapshot]);

  useEffect(() => {
    drainQueueRef.current = drainQueue;
  }, [drainQueue]);

  const enqueue = useCallback(
    (alertId: string, force = false) => {
      if (!isUuid(alertId) || !mountedRef.current) return;
      if (authBlockedRef.current && !force) return;
      if (requestControllersRef.current.has(alertId)) return;
      if (queuedIdSetRef.current.has(alertId)) return;
      if (!force && rememberedRef.current.has(alertId)) return;
      if (force) {
        authBlockedRef.current = false;
        authFailureIdRef.current = null;
        rememberedRef.current.delete(alertId);
      }

      if (queuedIdsRef.current.length >= MAX_QUEUED_ALERT_READS) {
        remember(alertId);
        const pending = pendingVisibilityRef.current.get(alertId);
        if (pending?.retryQueued) {
          // Expire this bounded retry rather than leave an unscheduled pending ID.
          finishPendingVisibility(alertId, true);
        }
        setHasOverflow(true);
        return;
      }

      remember(alertId);
      queuedIdsRef.current.push(alertId);
      queuedIdSetRef.current.add(alertId);
      drainQueueRef.current();
    },
    [finishPendingVisibility, remember],
  );

  useEffect(() => {
    enqueueRef.current = enqueue;
  }, [enqueue]);

  const acceptEvents = useCallback(
    (events: RealtimeAlertReference[]) => {
      for (const event of events) {
        if (isUuid(event.alertId) && isUuid(event.incidentId)) {
          if (terminalVisibilityRef.current.has(event.alertId)) continue;
          const pending = pendingVisibilityRef.current.get(event.alertId);
          if (
            pending &&
            pending.retriesUsed >= ALERT_VISIBILITY_RETRY_DELAYS_MS.length
          ) {
            continue;
          }
          eventAlertIdsRef.current.add(event.alertId);
          enqueue(event.alertId);
        }
      }
    },
    [enqueue],
  );

  const retry = useCallback(
    (alertId: string) => {
      const pending = pendingVisibilityRef.current.get(alertId);
      if (pending?.timer !== undefined) {
        clearTimeout(pending.timer);
        pendingVisibilityRef.current.set(alertId, {
          ...pending,
          timer: undefined,
          retryQueued: true,
        });
      }
      if (queuedIdSetRef.current.delete(alertId)) {
        queuedIdsRef.current = queuedIdsRef.current.filter(
          (queuedId) => queuedId !== alertId,
        );
      }
      enqueue(alertId, true);
    },
    [enqueue],
  );

  const revalidateKnown = useCallback(() => {
    const knownIds = new Set(
      alertsRef.current
        .map((entry) => entry.alertId)
        .filter((alertId) => !terminalVisibilityRef.current.has(alertId)),
    );
    for (const [alertId, pending] of pendingVisibilityRef.current) {
      if (pending.retriesUsed >= ALERT_VISIBILITY_RETRY_DELAYS_MS.length) {
        continue;
      }
      if (pending.timer !== undefined) clearTimeout(pending.timer);
      if (pending.retryQueued) continue;
      pendingVisibilityRef.current.set(alertId, {
        ...pending,
        timer: undefined,
        retryQueued: true,
      });
      enqueue(alertId, true);
    }
    if (authFailureIdRef.current) {
      knownIds.add(authFailureIdRef.current);
      authBlockedRef.current = false;
    }
    for (const alertId of knownIds) enqueue(alertId, true);
  }, [enqueue]);

  return {
    alerts,
    hasOverflow,
    acceptEvents,
    retry,
    revalidateKnown,
    clearProtectedAlerts,
  };
}
