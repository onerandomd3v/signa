"use client";

import { useState } from "react";
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

  const refresh = useCoalescedRefresh({
    key: incidentId,
    load: (signal: AbortSignal) =>
      fetchPublicIncident(incidentId, globalThis.fetch, signal),
    onSuccess: (result) => {
      if (result.status === "ready") {
        setState({
          status: "ready",
          incident: toIncidentView(result.incident),
        });
      } else if (result.status === "error") {
        setState((current) =>
          current.status === "ready" ? current : { status: "error" },
        );
      } else {
        setState({ status: result.status });
      }
    },
    onError: () => {
      setState((current) =>
        current.status === "ready" ? current : { status: "error" },
      );
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
      ? { ...visibleState, onRetry: refresh }
      : visibleState;

  return (
    <div className="space-y-3">
      <RealtimeConnectionStatusMessage
        alertUpdateReceived={realtime.alertUpdateReceived}
        status={realtime.status}
      />
      <IncidentDetail state={detailState} />
    </div>
  );
}
