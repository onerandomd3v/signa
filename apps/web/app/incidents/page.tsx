import type { Metadata } from "next";
import { AppShell } from "../_components/app-shell";
import { IncidentPageFrame } from "./_components/incident-views";
import { IncidentMapRoute } from "./_components/incident-map-experience";

export const metadata: Metadata = {
  title: "Incidents | Signa",
  description: "Review incident updates and system assessments.",
};

export default function IncidentsPage() {
  return (
    <AppShell>
      <IncidentPageFrame>
        <div className="mb-6 sm:mb-8">
          <h1 className="text-3xl leading-tight font-semibold tracking-[-0.04em] sm:text-4xl">
            Incident map
          </h1>
          <p className="mt-2 max-w-xl text-sm leading-6 text-muted-foreground">
            View active incidents and their latest updates.
          </p>
        </div>

        <IncidentMapRoute />
      </IncidentPageFrame>
    </AppShell>
  );
}
