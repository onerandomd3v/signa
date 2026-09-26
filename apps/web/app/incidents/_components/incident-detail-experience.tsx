"use client";

import { useCallback, useRef, useState } from "react";
import type { PublicIncident } from "@/lib/api/generated";
import { fetchPublicIncident } from "@/lib/api/incidents";
import type { AlertReader } from "./use-alert-reconciliation";
import { useCoalescedRefresh } from "./use-coalesced-refresh";
import {
  IncidentDetail,
  RealtimeConnectionStatusMessage,
  type IncidentDetailState,
  type IncidentView,
} from "./incident-views";
import { RealtimeAlertSnapshots } from "./realtime-alert-snapshots";
import { useAlertReconciliation } from "./use-alert-reconciliation";
import type { RealtimeConnector } from "./use-realtime-updates";
import { useRealtimeUpdates } from "./use-realtime-updates";

function toIncidentView(incident: PublicIncident): IncidentView {
  return {
    id: incident.id,
    eventType: incident.event_type,
    confidenceState: incident.confidence_state,
    severity: incident.severity,
    status: incident.status,
    approximateArea: null,
    lastSignalAt: incident.last_signal_at,
    // The generated public detail contract does not include update history.
    updates: null,
  };
}

export function IncidentDetailExperience({
  incidentId,
  connectRealtime,
  readAlert,
}: {
  incidentId: string;
  connectRealtime?: RealtimeConnector;
  readAlert?: AlertReader;
}) {
  const [state, setState] = useState<IncidentDetailState>({
    status: "loading",
  });
  const [reconciliationFailed, setReconciliationFailed] = useState(false);
  const loadedIncidentIdRef = useRef<string | null>(null);

  const refresh = useCoalescedRefresh({
    key: incidentId,
    load: (signal: AbortSignal) =>
      fetchPublicIncident(incidentId, globalThis.fetch, signal),
    onSuccess: (result) => {
      if (result.status === "ready") {
        loadedIncidentIdRef.current = incidentId;
        setReconciliationFailed(false);
        setState({
          status: "ready",
          incident: toIncidentView(result.incident),
        });
      } else if (result.status === "error") {
        if (loadedIncidentIdRef.current === incidentId) {
          setReconciliationFailed(true);
        } else {
          setState({ status: "error" });
        }
      } else {
        loadedIncidentIdRef.current = null;
        setReconciliationFailed(false);
        setState({ status: result.status });
      }
    },
    onError: () => {
      if (loadedIncidentIdRef.current === incidentId) {
        setReconciliationFailed(true);
      } else {
        setState({ status: "error" });
      }
    },
  });
  const alertReconciliation = useAlertReconciliation({ readAlert });
  const acceptAlertEvents = alertReconciliation.acceptEvents;
  const revalidateAlerts = alertReconciliation.revalidateKnown;
  const clearProtectedAlerts = alertReconciliation.clearProtectedAlerts;
  const onInvalidation = useCallback(
    ({
      incidents: incidentsChanged,
      alerts,
    }: {
      incidents: boolean;
      alerts: { alertId: string; incidentId: string }[];
    }) => {
      if (incidentsChanged) refresh();
      acceptAlertEvents(
        alerts.filter((alert) => alert.incidentId === incidentId),
      );
    },
    [acceptAlertEvents, incidentId, refresh],
  );
  const realtime = useRealtimeUpdates({
    onInvalidation,
    onConnected: revalidateAlerts,
    onUnauthorized: clearProtectedAlerts,
    connect: connectRealtime,
  });

  const visibleState =
    state.status === "ready" && state.incident.id !== incidentId
      ? { status: "loading" as const }
      : state;
  const detailState: IncidentDetailState =
    visibleState.status === "error"
      ? {
          ...visibleState,
          onRetry: () => {
            setState({ status: "loading" });
            refresh();
          },
        }
      : visibleState;
  const isReconciliationFailed =
    reconciliationFailed &&
    state.status === "ready" &&
    state.incident.id === incidentId;

  return (
    <div className="space-y-3">
      <RealtimeConnectionStatusMessage
        alertUpdateReceived={realtime.alertUpdateReceived}
        status={isReconciliationFailed ? "degraded" : realtime.status}
        onRetry={refresh}
      />
      <IncidentDetail state={detailState} />
      <RealtimeAlertSnapshots
        alerts={alertReconciliation.alerts}
        hasOverflow={alertReconciliation.hasOverflow}
        incidentId={incidentId}
        onRetry={alertReconciliation.retry}
      />
    </div>
  );
}
