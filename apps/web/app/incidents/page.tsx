import type { Metadata } from "next";
import { AppShell } from "../_components/app-shell";
import { IncidentFeed, IncidentPageFrame } from "./_components/incident-views";

export const metadata: Metadata = {
  title: "Incidents | Signa",
  description: "Review incident updates and system assessments.",
};

export default function IncidentsPage() {
  return (
    <AppShell>
      <IncidentPageFrame>
        <div className="mb-6 sm:mb-8">
          <p className="text-xs font-bold tracking-[0.14em] text-primary uppercase">
            Community updates
          </p>
          <h1 className="mt-2 text-3xl leading-tight font-semibold tracking-[-0.04em] sm:text-4xl">
            Incidents
          </h1>
          <p className="mt-3 max-w-xl text-base leading-7 text-muted-foreground">
            Follow reported events and how their status changes.
          </p>
        </div>

        <IncidentFeed state={{ status: "unavailable" }} />

        <p className="mt-4 text-sm leading-6 text-muted-foreground">
          Confidence reflects an evolving system assessment, not certainty.
          Severity describes potential impact, not how likely an event is.
        </p>
      </IncidentPageFrame>
    </AppShell>
  );
}
