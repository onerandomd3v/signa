"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import type { AlertRead } from "@/lib/api/generated";
import { fetchAuthorizedAlert, type AlertReadResult } from "@/lib/api/alerts";
import type { RealtimeAlertReference } from "./use-realtime-updates";

const MAX_VISIBLE_ALERTS = 3;
const MAX_REMEMBERED_ALERTS = 128;
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
  acceptEvents: (events: RealtimeAlertReference[]) => void;
  retry: (alertId: string) => void;
  revalidateKnown: () => void;
} {
  const [alerts, setAlerts] = useState<AlertSnapshotEntry[]>([]);
  const alertsRef = useRef<AlertSnapshotEntry[]>([]);
  const readerRef = useRef(readAlert);
  const mountedRef = useRef(false);
  const requestControllersRef = useRef(new Map<string, AbortController>());
  const rememberedRef = useRef(new Map<string, true>());
  const authBlockedRef = useRef(false);
  const authFailureIdRef = useRef<string | null>(null);
  const orderRef = useRef(0);

  useEffect(() => {
    readerRef.current = readAlert;
  }, [readAlert]);

  useEffect(() => {
    mountedRef.current = true;
    const requestControllers = requestControllersRef.current;
    return () => {
      mountedRef.current = false;
      for (const controller of requestControllers.values()) {
        controller.abort();
      }
      requestControllers.clear();
    };
  }, []);

  const publish = useCallback((next: AlertSnapshotEntry[]) => {
    const limited = [...next].sort(compareEntries).slice(0, MAX_VISIBLE_ALERTS);
    alertsRef.current = limited;
    if (mountedRef.current) setAlerts(limited);
  }, []);

  const remember = useCallback((alertId: string) => {
    rememberedRef.current.delete(alertId);
    rememberedRef.current.set(alertId, true);
    while (rememberedRef.current.size > MAX_REMEMBERED_ALERTS) {
      const oldest = rememberedRef.current.keys().next().value;
      if (oldest === undefined) break;
      rememberedRef.current.delete(oldest);
    }
  }, []);

  const reconcile = useCallback(
    async (alertId: string, force = false) => {
      if (!isUuid(alertId) || !mountedRef.current) return;
      if (authBlockedRef.current && !force) return;
      if (requestControllersRef.current.has(alertId)) return;
      if (!force && rememberedRef.current.has(alertId)) return;

      remember(alertId);
      const controller = new AbortController();
      requestControllersRef.current.set(alertId, controller);

      try {
        const result = await readerRef.current(alertId, controller.signal);
        if (!mountedRef.current || controller.signal.aborted) return;

        if (result.status === "authorized") {
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
          authBlockedRef.current = true;
          authFailureIdRef.current = alertId;
          for (const [
            otherId,
            otherController,
          ] of requestControllersRef.current) {
            if (otherId !== alertId) otherController.abort();
          }
          publish([
            {
              alertId,
              order: ++orderRef.current,
              status: "unavailable",
            },
          ]);
          return;
        }

        if (result.status === "not-found" || result.status === "invalid") {
          publish(
            alertsRef.current.filter((entry) => entry.alertId !== alertId),
          );
          if (authFailureIdRef.current === alertId) {
            authFailureIdRef.current = null;
          }
          return;
        }

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
      }
    },
    [publish, remember],
  );

  const acceptEvents = useCallback(
    (events: RealtimeAlertReference[]) => {
      for (const event of events) {
        if (isUuid(event.alertId) && isUuid(event.incidentId)) {
          void reconcile(event.alertId);
        }
      }
    },
    [reconcile],
  );

  const retry = useCallback(
    (alertId: string) => {
      void reconcile(alertId, true);
    },
    [reconcile],
  );

  const revalidateKnown = useCallback(() => {
    const knownIds = new Set(alertsRef.current.map((entry) => entry.alertId));
    if (authFailureIdRef.current) {
      knownIds.add(authFailureIdRef.current);
      authBlockedRef.current = false;
    }
    for (const alertId of knownIds) void reconcile(alertId, true);
  }, [reconcile]);

  return { alerts, acceptEvents, retry, revalidateKnown };
}
