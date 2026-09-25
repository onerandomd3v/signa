"use client";

import { useEffect, useState } from "react";
import type { PublicIncident } from "@/lib/api/generated";
import { fetchPublicIncident } from "@/lib/api/incidents";
import {
  IncidentDetail,
  type IncidentDetailState,
  type IncidentView,
} from "./incident-views";

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
}: {
  incidentId: string;
}) {
  const [state, setState] = useState<IncidentDetailState>({
    status: "loading",
  });
  const [retryCount, setRetryCount] = useState(0);

  useEffect(() => {
    let active = true;
    fetchPublicIncident(incidentId)
      .then((result) => {
        if (!active) return;
        if (result.status === "ready") {
          setState({
            status: "ready",
            incident: toIncidentView(result.incident),
          });
        } else {
          setState({ status: result.status });
        }
      })
      .catch(() => {
        if (active) setState({ status: "error" });
      });
    return () => {
      active = false;
    };
  }, [incidentId, retryCount]);

  if (state.status === "error") {
    return (
      <IncidentDetail
        state={{
          ...state,
          onRetry: () => {
            setState({ status: "loading" });
            setRetryCount((count) => count + 1);
          },
        }}
      />
    );
  }

  return <IncidentDetail state={state} />;
}
