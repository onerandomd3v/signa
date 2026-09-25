"use client";

import { useRef, useState } from "react";
import type { PublicIncident } from "@/lib/api/generated";
import { fetchPublicIncident } from "@/lib/api/incidents";
import { useCoalescedRefresh } from "./use-coalesced-refresh";
import {
  IncidentDetail,
  RealtimeConnectionStatusMessage,
  type IncidentDetailState,
  type IncidentView,
} from "./incident-views";
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
}: {
  incidentId: string;
  connectRealtime?: RealtimeConnector;
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
  const realtime = useRealtimeUpdates({
    onInvalidation: refresh,
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
    </div>
  );
}
