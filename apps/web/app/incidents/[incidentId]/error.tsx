"use client";

import { AppShell } from "../../_components/app-shell";
import { Button } from "../../../components/ui/button";
import { IncidentPageFrame } from "../_components/incident-views";

export default function IncidentDetailError({ reset }: { reset: () => void }) {
  return (
    <AppShell>
      <IncidentPageFrame>
        <section
          className="rounded-xl border border-border bg-card p-5 sm:p-7"
          role="alert"
        >
          <h1 className="text-lg font-semibold">
            Couldn’t load incident details
          </h1>
          <p className="mt-2 text-sm leading-6 text-muted-foreground">
            Try again in a moment. No incident information is available.
          </p>
          <Button className="mt-4 min-h-11 px-4" onClick={reset}>
            Try again
          </Button>
        </section>
      </IncidentPageFrame>
    </AppShell>
  );
}
