import { AppShell } from "../_components/app-shell";
import { IncidentFeed, IncidentPageFrame } from "./_components/incident-views";

export default function LoadingIncidentsPage() {
  return (
    <AppShell>
      <IncidentPageFrame>
        <h1 className="mb-6 text-3xl font-semibold tracking-tight">
          Incidents
        </h1>
        <IncidentFeed state={{ status: "loading" }} />
      </IncidentPageFrame>
    </AppShell>
  );
}
