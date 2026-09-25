import type { Metadata } from "next";
import Link from "next/link";
import { AppShell } from "../../_components/app-shell";
import { IncidentPageFrame } from "../_components/incident-views";
import { IncidentDetailExperience } from "../_components/incident-detail-experience";

export const metadata: Metadata = {
  title: "Incident details | Signa",
  description: "Review an incident’s current status and public details.",
};

export default async function IncidentDetailPage({
  params,
}: {
  params: Promise<{ incidentId: string }>;
}) {
  const { incidentId } = await params;

  return (
    <AppShell>
      <IncidentPageFrame>
        <nav aria-label="Breadcrumb" className="mb-5">
          <Link
            className="inline-flex min-h-11 items-center rounded-md text-sm font-medium text-primary underline-offset-4 hover:underline focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-ring"
            href="/incidents"
          >
            ← <span className="ml-2">Incidents</span>
          </Link>
        </nav>
        <IncidentDetailExperience incidentId={incidentId} />
      </IncidentPageFrame>
    </AppShell>
  );
}
